package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

const (
	// asrAUCBaseURL 大模型录音文件识别（异步 submit/query）REST 服务地址。
	asrAUCBaseURL = "https://openspeech.bytedance.com"
	// asrAUCSubmitPath 提交识别任务路径。
	asrAUCSubmitPath = "/api/v3/auc/bigmodel/submit"
	// asrAUCQueryPath 查询识别结果路径。
	asrAUCQueryPath = "/api/v3/auc/bigmodel/query"
	// asrAUCResourceID 异步录音文件识别资源 ID（官方文档 6561/1354868）。
	asrAUCResourceID = "volc.seedasr.auc"
	// asrAUCCodeOK 成功状态码（响应头 X-Api-Status-Code）。
	asrAUCCodeOK = "20000000"

	// 闲时版（官方文档 6561/2608618 提交、6561/2608619 查询）：闲时算力执行，任务通常 24h 内完成。
	asrIdleSubmitPath = "/api/v3/auc/bigmodel/idle/submit"
	asrIdleQueryPath  = "/api/v3/auc/bigmodel/idle/query"
	asrIdleResourceID = "volc.bigasr.auc_idle"
	// 极速版（官方文档 6561/2608628）：同步返回完整识别结果，无需轮询。
	asrFlashPath       = "/api/v3/auc/bigmodel/recognize/flash"
	asrFlashResourceID = "volc.bigasr.auc_turbo"
)

// ASRAUCClient 火山引擎大模型录音文件识别（异步 submit/query）REST 客户端。
// 鉴权同 WS 客户端：SpeechCred（新版 X-Api-Key 或老版 X-Api-App-Key + X-Api-Access-Key）。
// 用法：Submit 提交音频 URL 得到任务 ID，轮询 Query 直到 status 为 Completed/Failed。
type ASRAUCClient struct {
	resty *resty.Client
	cred  SpeechCred
}

func NewASRAUCClient(cred SpeechCred) *ASRAUCClient {
	return NewASRAUCClientWithBaseURL(cred, asrAUCBaseURL)
}

// NewASRAUCClientWithBaseURL 供测试注入 mock 地址。
func NewASRAUCClientWithBaseURL(cred SpeechCred, baseURL string) *ASRAUCClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(60 * time.Second)
	return &ASRAUCClient{resty: r, cred: cred}
}

// asrAUCQueryResp 查询任务响应（与 WS 响应同构；utterances 缺失时 Segments 为空，上层跳过 SRT）。
type asrAUCQueryResp struct {
	ID     string `json:"id"`
	Status string `json:"status"` // Queuing|Running|Completed|Failed
	Result struct {
		Text       string `json:"text"`
		Utterances []struct {
			Text      string `json:"text"`
			StartTime int64  `json:"start_time"`
			EndTime   int64  `json:"end_time"`
		} `json:"utterances"`
	} `json:"result"`
	AudioInfo struct {
		Duration int64 `json:"duration"`
	} `json:"audio_info"`
}

// Submit 提交录音文件识别任务：POST submit，body {"audio_url":...}，任务 ID 即客户端生成的
// X-Api-Request-Id（UUID）。成功判定：响应头 X-Api-Status-Code == 20000000。
func (c *ASRAUCClient) Submit(ctx context.Context, audioURL string) (string, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrAUCResourceID)
	taskID := headers.Get("X-Api-Request-Id")
	headers.Set("X-Api-Sequence", "-1")

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(map[string]string{"audio_url": audioURL}).
		Post(asrAUCSubmitPath)
	if err != nil {
		return "", fmt.Errorf("提交火山 ASR 识别任务失败: %w", err)
	}
	if err := checkAUCStatusCode("提交任务", httpResp.Header().Get("X-Api-Status-Code")); err != nil {
		return "", err
	}
	return taskID, nil
}

// Query 查询识别任务：POST query，body {"id":taskID}。status 原样返回（Queuing|Running|Completed|Failed）；
// Completed 时解析 result（text/utterances）与 audio_info.duration，utterances 缺失时 Segments 为空。
func (c *ASRAUCClient) Query(ctx context.Context, taskID string) (ASRNostreamResp, string, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrAUCResourceID)

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(map[string]string{"id": taskID}).
		Post(asrAUCQueryPath)
	if err != nil {
		return ASRNostreamResp{}, "", fmt.Errorf("查询火山 ASR 任务失败: %w", err)
	}
	// Query 响应头未完全核实：X-Api-Status-Code 非空时校验（鉴权类 45xxxx → ErrAuth），
	// 为空则跳过，交由 body 任务状态表达。
	if err := checkAUCStatusCode("查询任务", httpResp.Header().Get("X-Api-Status-Code")); err != nil {
		return ASRNostreamResp{}, "", err
	}

	var apiResp asrAUCQueryResp
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return ASRNostreamResp{}, "", fmt.Errorf("解析火山 ASR 查询响应失败: %w", err)
	}
	out := ASRNostreamResp{
		Text:       apiResp.Result.Text,
		DurationMS: apiResp.AudioInfo.Duration,
	}
	for _, u := range apiResp.Result.Utterances {
		out.Segments = append(out.Segments, ASRSegment{
			Text:    u.Text,
			StartMS: u.StartTime,
			EndMS:   u.EndTime,
		})
	}
	return out, apiResp.Status, nil
}

// aucTaskRequest 闲时版/极速版提交请求体：audio.url 必填、request.model_name 固定 bigmodel。
type aucTaskRequest struct {
	Audio   aucAudioMeta  `json:"audio"`
	Request aucTaskOption `json:"request"`
}

// aucAudioMeta 协议文档 6561/2608618 将 language 归入 audio 对象（同 sauc WS 协议的 audio.language）。
type aucAudioMeta struct {
	URL      string `json:"url"`
	Format   string `json:"format"`
	Language string `json:"language,omitempty"` // 留空时服务端自动识别中文/英文及多种方言
}

// aucTaskOption 识别选项：标点/ITN/分句默认全开（与 WS 通道行为一致），供 SRT 生成使用。
type aucTaskOption struct {
	ModelName      string     `json:"model_name"`
	EnableITN      bool       `json:"enable_itn"`
	EnablePunc     bool       `json:"enable_punc"`
	ShowUtterances bool       `json:"show_utterances"`
	Corpus         *aucCorpus `json:"corpus,omitempty"`
}

// aucCorpus 热词经 corpus.context（JSON 字符串）直传，形如 {"hotwords":[{"word":"..."}]}。
// 注意协议约束：corpus 与 enable_auto_lang 互斥，本项目不启用后者。
type aucCorpus struct {
	Context string `json:"context,omitempty"`
}

// aucUtterance 分句时间戳。
type aucUtterance struct {
	Text      string `json:"text"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

// aucResultResp 闲时版查询响应与极速版同步响应共用的结果结构。
// Result 用指针区分「未返回（任务未完成）」与「空结果」。
type aucResultResp struct {
	TaskID string `json:"task_id"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Result *struct {
		Text       string         `json:"text"`
		Utterances []aucUtterance `json:"utterances"`
	} `json:"result"`
	AudioInfo struct {
		Duration int64 `json:"duration"`
	} `json:"audio_info"`
}

// aucResultToResp 将共用结果结构转为工具层统一响应。
func aucResultToResp(apiResp aucResultResp) ASRNostreamResp {
	out := ASRNostreamResp{
		Text:       apiResp.Result.Text,
		DurationMS: apiResp.AudioInfo.Duration,
	}
	for _, u := range apiResp.Result.Utterances {
		out.Segments = append(out.Segments, ASRSegment{
			Text:    u.Text,
			StartMS: u.StartTime,
			EndMS:   u.EndTime,
		})
	}
	return out
}

// SubmitIdle 提交闲时版识别任务（6561/2608618）：POST idle/submit，头带 X-Api-Resource-Id
// （volc.bigasr.auc_idle）与 X-Api-Sequence=-1。任务 ID 优先取响应体 task_id，
// 缺失时回退为客户端 X-Api-Request-Id（与标准版「任务 ID 即请求 ID」同语义）。
func (c *ASRAUCClient) SubmitIdle(ctx context.Context, req aucTaskRequest) (string, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrIdleResourceID)
	taskID := headers.Get("X-Api-Request-Id")
	headers.Set("X-Api-Sequence", "-1")

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(req).
		Post(asrIdleSubmitPath)
	if err != nil {
		return "", fmt.Errorf("提交火山闲时识别任务失败: %w", err)
	}
	if err := checkAUCStatusCode("提交闲时任务", httpResp.Header().Get("X-Api-Status-Code")); err != nil {
		return "", err
	}
	var ack struct {
		TaskID string `json:"task_id"`
		ID     string `json:"id"`
	}
	if len(httpResp.Body()) > 0 {
		_ = json.Unmarshal(httpResp.Body(), &ack)
	}
	switch {
	case ack.TaskID != "":
		return ack.TaskID, nil
	case ack.ID != "":
		return ack.ID, nil
	}
	return taskID, nil
}

// QueryIdle 查询闲时版任务（6561/2608619）：POST idle/query，请求体为空 JSON {}，
// 任务 ID 通过 X-Api-Request-Id 头回传（覆盖新生的请求 ID）。
// 状态判定：响应体给出 status 时优先（Queuing|Running|Completed|Failed）；缺失时
// result 非空即 Completed，X-Api-Status-Code 2/5 开头非成功码视为任务中间态继续轮询，
// 4 开头（鉴权/参数类）为终态错误立即失败。
func (c *ASRAUCClient) QueryIdle(ctx context.Context, taskID string) (ASRNostreamResp, string, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrIdleResourceID)
	headers.Set("X-Api-Request-Id", taskID)

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(map[string]any{}).
		Post(asrIdleQueryPath)
	if err != nil {
		return ASRNostreamResp{}, "", fmt.Errorf("查询火山闲时识别任务失败: %w", err)
	}
	if httpResp.StatusCode() != http.StatusOK {
		return ASRNostreamResp{}, "", fmt.Errorf("查询火山闲时识别任务失败(HTTP %d): %s",
			httpResp.StatusCode(), aucBodyExcerpt(httpResp.Body()))
	}
	code := httpResp.Header().Get("X-Api-Status-Code")
	switch {
	case code == "" || code == asrAUCCodeOK || strings.HasPrefix(code, "2") || strings.HasPrefix(code, "5"):
		// 成功、未知或服务端/中间态码：交由响应体判定任务状态。
	default:
		return ASRNostreamResp{}, "", checkAUCStatusCode("查询闲时任务", code)
	}

	var apiResp aucResultResp
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return ASRNostreamResp{}, "", fmt.Errorf("解析火山闲时识别查询响应失败: %w", err)
	}
	if apiResp.Result != nil {
		return aucResultToResp(apiResp), "Completed", nil
	}
	if apiResp.Status == "Failed" {
		return ASRNostreamResp{}, "Failed", nil
	}
	if apiResp.Status == "Completed" {
		return ASRNostreamResp{}, "", fmt.Errorf("闲时任务已完成但未返回识别结果")
	}
	// 中间态：无结果可取。服务端给过 status（Queuing/Running）就原样透传，否则统一报 Running。
	if apiResp.Status != "" {
		return ASRNostreamResp{}, apiResp.Status, nil
	}
	return ASRNostreamResp{}, "Running", nil
}

// RecognizeFlash 极速版同步识别（6561/2608628）：POST recognize/flash，完整结果随响应返回。
// 成功判定：X-Api-Status-Code == 20000000 且响应体含 result。
func (c *ASRAUCClient) RecognizeFlash(ctx context.Context, req aucTaskRequest) (ASRNostreamResp, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrFlashResourceID)
	headers.Set("X-Api-Sequence", "-1")

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(req).
		Post(asrFlashPath)
	if err != nil {
		return ASRNostreamResp{}, fmt.Errorf("火山极速识别请求失败: %w", err)
	}
	if httpResp.StatusCode() != http.StatusOK {
		return ASRNostreamResp{}, fmt.Errorf("火山极速识别失败(HTTP %d): %s",
			httpResp.StatusCode(), aucBodyExcerpt(httpResp.Body()))
	}
	if err := checkAUCStatusCode("极速识别", httpResp.Header().Get("X-Api-Status-Code")); err != nil {
		return ASRNostreamResp{}, err
	}
	var apiResp aucResultResp
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return ASRNostreamResp{}, fmt.Errorf("解析火山极速识别响应失败: %w", err)
	}
	if apiResp.Result == nil {
		return ASRNostreamResp{}, fmt.Errorf("火山极速识别未返回识别结果")
	}
	return aucResultToResp(apiResp), nil
}

// aucBodyExcerpt 截取响应体前 200 字节用于错误消息（防超长二进制/HTML 刷屏）。
func aucBodyExcerpt(body []byte) string {
	const n = 200
	if len(body) > n {
		return string(body[:n]) + "…"
	}
	return string(body)
}

// aucHeaderMap 将 http.Header 转为 resty SetHeaders 需要的 map[string]string（每键取首值）。
func aucHeaderMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

// checkAUCStatusCode 校验响应头 X-Api-Status-Code（沿用 tts_client 错误映射风格）：
// 45xxxx 鉴权类 → ErrAuth 包装；其余非 20000000 → 中文错误含 code；空（未返回）视为通过。
func checkAUCStatusCode(op, code string) error {
	if code == "" || code == asrAUCCodeOK {
		return nil
	}
	if strings.HasPrefix(code, "45") {
		return fmt.Errorf("%w: 火山 ASR %s被拒绝(X-Api-Status-Code %s)，请检查语音凭证配置", ErrAuth, op, code)
	}
	return fmt.Errorf("火山 ASR %s失败(X-Api-Status-Code %s)", op, code)
}
