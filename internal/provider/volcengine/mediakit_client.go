package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

const (
	// MediaKitBaseURL AI MediaKit REST 服务地址（凭证体系独立于语音三件套，仅一个 apiKey）。
	MediaKitBaseURL = "https://mediakit.cn-beijing.volces.com"
	// MediaKitSubmitPath 提交人声背景音分离任务路径。
	MediaKitSubmitPath = "/api/v1/tools/separate-voice"
	// MediaKitQueryPathFmt 查询任务路径（task_id 拼接）。
	MediaKitQueryPathFmt = "/api/v1/tasks/%s"
	// mediaKitDownloadTimeout 产物转存下载超时（产物 URL 为 24 小时有效的临时直链，文件较大）。
	mediaKitDownloadTimeout = 10 * time.Minute
)

// MediaKitTrack 分离出的单条音轨。Kind: voice|background|music|sfx。
type MediaKitTrack struct {
	Kind string
	URL  string
}

// MediaKitResult 分离结果：音轨按 voice→background→music→sfx 固定顺序动态收集（仅收非空 URL 字段，
// 随场景而异：Audio/Music 双轨，Drama/Narrate 三轨）。
type MediaKitResult struct {
	Tracks    []MediaKitTrack
	DurationS float64
}

// MediaKitClient AI MediaKit 人声背景音分离 REST 客户端。
// 鉴权与语音凭证（SpeechCred）独立：仅一个 apiKey，请求头 Authorization: Bearer <apiKey>。
// 用法：Submit 提交音视频 URL 得到任务 ID，轮询 Query 直到 status 为 completed/failed，
// completed 后逐轨 Download 转存（产物 URL 24 小时有效）。
type MediaKitClient struct {
	resty    *resty.Client
	download *resty.Client
	apiKey   string
}

func NewMediaKitClient(apiKey string) *MediaKitClient {
	return NewMediaKitClientWithBaseURL(apiKey, MediaKitBaseURL)
}

// NewMediaKitClientWithBaseURL 供测试注入 mock 地址。
func NewMediaKitClientWithBaseURL(apiKey, baseURL string) *MediaKitClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(60 * time.Second)
	// 产物下载走 24h 临时直链（绝对 URL，不走 baseURL），文件较大单独放宽超时。
	d := resty.New().SetTimeout(mediaKitDownloadTimeout)
	return &MediaKitClient{resty: r, download: d, apiKey: apiKey}
}

// mediaKitSubmitReq 提交分离任务请求；非空字段才进 body（omitempty）。
type mediaKitSubmitReq struct {
	VideoURL     string `json:"video_url,omitempty"`
	AudioURL     string `json:"audio_url,omitempty"`
	Scene        string `json:"scene,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

// mediaKitError 上游业务错误对象（提交/查询响应共用）。
type mediaKitError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
	Type    string `json:"type"`
}

// mediaKitResultResp 查询 result（仅 completed 出现；字段按存在性容错）。
type mediaKitResultResp struct {
	VoiceAudioURL      string  `json:"voice_audio_url"`
	BackgroundAudioURL string  `json:"background_audio_url"`
	MusicAudioURL      string  `json:"music_audio_url"`
	SFXAudioURL        string  `json:"sfx_audio_url"`
	Duration           float64 `json:"duration"`
}

// mediaKitResp 提交/查询响应外壳（result 仅 completed 出现，error 仅 failed/success=false 出现）。
type mediaKitResp struct {
	Success bool                `json:"success"`
	TaskID  string              `json:"task_id"`
	Status  string              `json:"status"` // running|completed|failed
	Result  *mediaKitResultResp `json:"result"`
	Error   *mediaKitError      `json:"error"`
}

// Submit 提交人声背景音分离任务：POST /api/v1/tools/separate-voice。
// mediaField 指定媒体进 body 的字段名："video_url" 或 "audio_url"（接口二选一，由调用方决定）；
// scene/outputFormat 非空才进 body。成功返回 task_id。
func (c *MediaKitClient) Submit(ctx context.Context, mediaField, mediaURL, scene, outputFormat string) (string, error) {
	if mediaField != "video_url" && mediaField != "audio_url" {
		return "", fmt.Errorf("mediaField 必须为 video_url 或 audio_url，得到 %q", mediaField)
	}
	if mediaURL == "" {
		return "", fmt.Errorf("缺少媒体地址：请提供公网可访问的音视频 URL")
	}
	req := mediaKitSubmitReq{Scene: scene, OutputFormat: outputFormat}
	if mediaField == "video_url" {
		req.VideoURL = mediaURL
	} else {
		req.AudioURL = mediaURL
	}

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+c.apiKey).
		SetBody(req).
		Post(MediaKitSubmitPath)
	if err != nil {
		return "", fmt.Errorf("提交人声分离任务失败: %w", err)
	}
	if err := checkMediaKitHTTPStatus("提交任务", httpResp); err != nil {
		return "", err
	}

	var apiResp mediaKitResp
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return "", fmt.Errorf("解析人声分离提交响应失败: %w", err)
	}
	if !apiResp.Success {
		return "", mediaKitAPIError("提交任务", apiResp.Error)
	}
	if apiResp.TaskID == "" {
		return "", fmt.Errorf("人声分离提交响应缺少 task_id")
	}
	return apiResp.TaskID, nil
}

// Query 查询分离任务：GET /api/v1/tasks/{task_id}。status 原样返回（running|completed|failed）；
// completed 时解析音轨（voice→background→music→sfx 顺序动态收集非空 URL）与时长；
// failed 是任务终态而非传输错误：err 为 nil，errMsg = "code: message" 交由调用方决定处理。
func (c *MediaKitClient) Query(ctx context.Context, taskID string) (string, *MediaKitResult, string, error) {
	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+c.apiKey).
		Get(fmt.Sprintf(MediaKitQueryPathFmt, url.PathEscape(taskID)))
	if err != nil {
		return "", nil, "", fmt.Errorf("查询人声分离任务失败: %w", err)
	}
	if err := checkMediaKitHTTPStatus("查询任务", httpResp); err != nil {
		return "", nil, "", err
	}

	var apiResp mediaKitResp
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return "", nil, "", fmt.Errorf("解析人声分离查询响应失败: %w", err)
	}
	if !apiResp.Success {
		return "", nil, "", mediaKitAPIError("查询任务", apiResp.Error)
	}
	if apiResp.Status == "failed" {
		errMsg := ""
		if apiResp.Error != nil {
			errMsg = apiResp.Error.Code + ": " + apiResp.Error.Message
		}
		return apiResp.Status, nil, errMsg, nil
	}
	if apiResp.Status != "completed" || apiResp.Result == nil {
		// running 等中间态：无结果。
		return apiResp.Status, nil, "", nil
	}
	return apiResp.Status, parseMediaKitResult(apiResp.Result), "", nil
}

// Download 下载产物音轨：24 小时有效的临时直链（绝对 URL 直连，不走 baseURL，不携带
// apiKey——凭证仅用于 MediaKit API 域）。10 分钟超时；非 2xx 报错（直链过期等，与凭证无关）。
func (c *MediaKitClient) Download(ctx context.Context, rawURL string) ([]byte, error) {
	httpResp, err := c.download.R().
		SetContext(ctx).
		Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("下载人声分离产物失败: %w", err)
	}
	if !httpResp.IsSuccess() {
		return nil, fmt.Errorf("下载人声分离产物失败(HTTP %d): %s",
			httpResp.StatusCode(), mediaKitBodySnippet(httpResp))
	}
	return httpResp.Body(), nil
}

// parseMediaKitResult 按 voice→background→music→sfx 固定顺序收集非空音轨。
func parseMediaKitResult(r *mediaKitResultResp) *MediaKitResult {
	res := &MediaKitResult{DurationS: r.Duration}
	for _, t := range []struct {
		kind, rawURL string
	}{
		{"voice", r.VoiceAudioURL},
		{"background", r.BackgroundAudioURL},
		{"music", r.MusicAudioURL},
		{"sfx", r.SFXAudioURL},
	} {
		if t.rawURL != "" {
			res.Tracks = append(res.Tracks, MediaKitTrack{Kind: t.kind, URL: t.rawURL})
		}
	}
	return res
}

// checkMediaKitHTTPStatus 校验 HTTP 状态码（沿用 tts_client 错误映射风格）：
// 2xx 通过；401/403 → ErrAuth 包装；其余非 2xx → 中文错误含状态码与上游 body 摘要。
func checkMediaKitHTTPStatus(op string, resp *resty.Response) error {
	if resp.IsSuccess() {
		return nil
	}
	code := resp.StatusCode()
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return fmt.Errorf("%w: MediaKit %s被拒绝(HTTP %d)，请检查 volc.mediakit.api_key 配置", ErrAuth, op, code)
	}
	return fmt.Errorf("MediaKit %s失败(HTTP %d): %s", op, code, mediaKitBodySnippet(resp))
}

// mediaKitAPIError success=false 的业务错误：中文错误含上游 code/message。
func mediaKitAPIError(op string, e *mediaKitError) error {
	if e == nil {
		return fmt.Errorf("MediaKit %s失败: success=false", op)
	}
	return fmt.Errorf("MediaKit %s失败(%s): %s", op, e.Code, e.Message)
}

// mediaKitBodySnippet 截取响应 body 摘要（按 rune 截断，避免错误文案过长或截出坏字符）。
func mediaKitBodySnippet(resp *resty.Response) string {
	s := strings.TrimSpace(string(resp.Body()))
	if r := []rune(s); len(r) > 128 {
		s = string(r[:128]) + "…(截断)"
	}
	return s
}
