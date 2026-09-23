package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

const (
	// ttsLongSubmitPath 异步长文本合成任务提交路径（官方文档 /api/v3/tts/submit）。
	ttsLongSubmitPath = "/api/v3/tts/submit"
	// ttsLongQueryPath 异步长文本合成任务查询路径（官方文档 /api/v3/tts/query）。
	ttsLongQueryPath = "/api/v3/tts/query"
	// ttsLongCodeOK 成功状态码（响应体 code 字段）。
	ttsLongCodeOK = 20000000

	// 资源 ID：普通音色走合成大模型 2.0，声音复刻音色走复刻大模型 2.0。
	ttsLongResourceSeed = "seed-tts-2.0"
	ttsLongResourceICL  = "seed-icl-2.0"
)

// 任务终态枚举（query 响应 data.task_status）。
const (
	ttsLongStatusRunning = 1
	ttsLongStatusSuccess = 2
	ttsLongStatusFailure = 3
)

// TTSLongSubmitReq 提交长文本合成任务的参数（Tool 层完成校验与默认值）。
type TTSLongSubmitReq struct {
	Text             string // 待合成文本（≤10 万字符）
	Speaker          string // 音色 ID
	Resource         string // seed-tts-2.0（普通音色）| seed-icl-2.0（复刻音色）
	Model            string // 复刻音色时透传 req_params.model，空则不传
	Format           string // mp3|pcm|ogg_opus
	SampleRate       int    // Hz；ogg_opus 仅 48000
	BitRate          int    // bps，0 不传（服务端默认）
	SpeechRate       int    // -50~100，100=2 倍速
	LoudnessRate     int    // -50~100，100=2 倍音量
	EnableTimestamp  bool   // 开启后 query 返回分句/字级时间戳
	ExplicitLanguage string // 空不传：zh-cn|en|es-mx|id|pt-br
	Pitch            int    // -12~12，0 不传 post_process
	AIGCWatermark    bool   // 音频结尾 AIGC 节奏标识
}

// TTSLongSentence 分句时间戳（秒 → 毫秒归一，供 BuildSRT 直接消费）。
type TTSLongSentence struct {
	Text    string
	StartMS int64
	EndMS   int64
}

// TTSLongQueryResult 查询结果：状态 + 成功时的音频链接与时间戳。
type TTSLongQueryResult struct {
	TaskID               string
	Status               string // Running|Success|Failure
	Message              string
	AudioURL             string
	Sentences            []TTSLongSentence
	ReqTextLength        int
	SynthesizeTextLength int
	URLExpireTime        int64 // audio_url 过期时间戳（秒）
}

// TTSLongClient 火山引擎异步长文本语音合成（submit/query）REST 客户端。
// 鉴权同 ASR v3 异步：SpeechCred（新版 X-Api-Key 或旧版 X-Api-App-Key + X-Api-Access-Key）。
// 用法：Submit 得到 task_id → 轮询 Query 至 Success（audio_url 1 小时有效，及时 Download）。
type TTSLongClient struct {
	resty *resty.Client
	cred  SpeechCred
}

func NewTTSLongClient(cred SpeechCred) *TTSLongClient {
	return NewTTSLongClientWithBaseURL(cred, ttsBaseURL)
}

// NewTTSLongClientWithBaseURL 供测试注入 mock 地址。
func NewTTSLongClientWithBaseURL(cred SpeechCred, baseURL string) *TTSLongClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(60 * time.Second)
	return &TTSLongClient{resty: r, cred: cred}
}

// ttsLongAudioParams 协议 audio_params：比特率/语速/音量为 0 时省略（即服务端默认值）。
type ttsLongAudioParams struct {
	Format          string `json:"format"`
	SampleRate      int    `json:"sample_rate"`
	BitRate         int    `json:"bit_rate,omitempty"`
	SpeechRate      int    `json:"speech_rate,omitempty"`
	LoudnessRate    int    `json:"loudness_rate,omitempty"`
	EnableTimestamp bool   `json:"enable_timestamp,omitempty"`
}

type ttsLongReqParams struct {
	Text             string              `json:"text"`
	Model            string              `json:"model,omitempty"` // 仅复刻音色需指定
	Speaker          string              `json:"speaker"`
	AudioParams      ttsLongAudioParams  `json:"audio_params"`
	ExplicitLanguage string              `json:"explicit_language,omitempty"`
	AIGCWatermark    bool                `json:"aigc_watermark,omitempty"`
	PostProcess      *ttsLongPostProcess `json:"post_process,omitempty"`
}

// ttsLongPostProcess 后处理：pitch 为 0（默认音调）时不携带该对象。
type ttsLongPostProcess struct {
	Pitch int `json:"pitch,omitempty"`
}

type ttsLongSubmitAPIReq struct {
	User struct {
		UID      string `json:"uid"`
		UniqueID string `json:"unique_id"` // 传入后响应 task_id 即取该值
	} `json:"user"`
	ReqParams ttsLongReqParams `json:"req_params"`
}

// ttsLongEnvelope submit/query 共用响应包络：code=20000000 成功，其余为错误。
type ttsLongEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		TaskID     string `json:"task_id"`
		TaskStatus int    `json:"task_status"`
		Message    string `json:"message"`
		// submit 专属
		ReqTextLength int `json:"req_text_length"`
		// query 专属
		AudioURL             string            `json:"audio_url"`
		Sentences            []ttsLongSentence `json:"sentences"`
		SynthesizeTextLength int               `json:"synthesize_text_length"`
		URLExpireTime        int64             `json:"url_expire_time"`
	} `json:"data"`
}

// ttsLongSentence 协议时间戳：秒（float64），归一为毫秒。
type ttsLongSentence struct {
	Text      string        `json:"text"`
	StartTime float64       `json:"startTime"`
	EndTime   float64       `json:"endTime"`
	Words     []ttsLongWord `json:"words"`
}

type ttsLongWord struct {
	Word       string  `json:"word"`
	StartTime  float64 `json:"startTime"`
	EndTime    float64 `json:"endTime"`
	Confidence float64 `json:"confidence"`
}

// Submit 提交长文本合成任务：POST submit，成功返回 task_id（unique_id 回传语义下
// 等于客户端生成的请求 ID，缺失时回退客户端 ID）。
func (c *TTSLongClient) Submit(ctx context.Context, req TTSLongSubmitReq) (string, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, req.Resource)
	uniqueID := headers.Get("X-Api-Request-Id")

	var apiReq ttsLongSubmitAPIReq
	apiReq.User.UID = "voxbox"
	apiReq.User.UniqueID = uniqueID
	apiReq.ReqParams = ttsLongReqParams{
		Text:    req.Text,
		Model:   req.Model,
		Speaker: req.Speaker,
		AudioParams: ttsLongAudioParams{
			Format:          req.Format,
			SampleRate:      req.SampleRate,
			BitRate:         req.BitRate,
			SpeechRate:      req.SpeechRate,
			LoudnessRate:    req.LoudnessRate,
			EnableTimestamp: req.EnableTimestamp,
		},
		ExplicitLanguage: req.ExplicitLanguage,
		AIGCWatermark:    req.AIGCWatermark,
	}
	if req.Pitch != 0 {
		apiReq.ReqParams.PostProcess = &ttsLongPostProcess{Pitch: req.Pitch}
	}

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(apiReq).
		Post(ttsLongSubmitPath)
	if err != nil {
		return "", fmt.Errorf("提交火山长文本合成任务失败: %w", err)
	}
	if httpResp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("提交火山长文本合成任务失败(HTTP %d): %s%s",
			httpResp.StatusCode(), aucBodyExcerpt(httpResp.Body()), ttsLogidSuffix(httpResp))
	}

	var env ttsLongEnvelope
	if err := json.Unmarshal(httpResp.Body(), &env); err != nil {
		return "", fmt.Errorf("解析火山长文本合成响应失败: %w", err)
	}
	if env.Code != ttsLongCodeOK {
		return "", fmt.Errorf("火山长文本合成提交失败(%d): %s%s", env.Code, env.Message, ttsLogidSuffix(httpResp))
	}
	if env.Data.TaskID != "" {
		return env.Data.TaskID, nil
	}
	return uniqueID, nil
}

// Query 查询任务状态：Running|Success|Failure。resource 必须与 Submit 一致
// （seed-tts-2.0 / seed-icl-2.0）。Success 时不校验 audio_url（由 Tool 层决定
// 「成功但无音频」的处理），sentences 秒级时间戳归一为毫秒。
func (c *TTSLongClient) Query(ctx context.Context, taskID, resource string) (TTSLongQueryResult, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, resource)

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(map[string]string{"task_id": taskID}).
		Post(ttsLongQueryPath)
	if err != nil {
		return TTSLongQueryResult{}, fmt.Errorf("查询火山长文本合成任务失败: %w", err)
	}
	if httpResp.StatusCode() != http.StatusOK {
		return TTSLongQueryResult{}, fmt.Errorf("查询火山长文本合成任务失败(HTTP %d): %s%s",
			httpResp.StatusCode(), aucBodyExcerpt(httpResp.Body()), ttsLogidSuffix(httpResp))
	}

	var env ttsLongEnvelope
	if err := json.Unmarshal(httpResp.Body(), &env); err != nil {
		return TTSLongQueryResult{}, fmt.Errorf("解析火山长文本合成查询响应失败: %w", err)
	}
	if env.Code != ttsLongCodeOK {
		return TTSLongQueryResult{}, fmt.Errorf("火山长文本合成查询失败(%d): %s%s",
			env.Code, env.Message, ttsLogidSuffix(httpResp))
	}

	out := TTSLongQueryResult{
		TaskID:               env.Data.TaskID,
		Message:              env.Data.Message,
		AudioURL:             env.Data.AudioURL,
		ReqTextLength:        env.Data.ReqTextLength,
		SynthesizeTextLength: env.Data.SynthesizeTextLength,
		URLExpireTime:        env.Data.URLExpireTime,
	}
	switch env.Data.TaskStatus {
	case ttsLongStatusSuccess:
		out.Status = "Success"
	case ttsLongStatusFailure:
		out.Status = "Failure"
	default:
		out.Status = "Running"
	}
	for _, s := range env.Data.Sentences {
		out.Sentences = append(out.Sentences, TTSLongSentence{
			Text:    s.Text,
			StartMS: secToMS(s.StartTime),
			EndMS:   secToMS(s.EndTime),
		})
	}
	return out, nil
}

// Download 下载合成音频（audio_url 有效期 1 小时，查询到 Success 后立即取回）。
// audio_url 是服务端签发的独立下载地址，不走 base URL；DoNotParseResponse 下
// resty 不会预读响应体，需从 RawBody 手动读全。
func (c *TTSLongClient) Download(ctx context.Context, audioURL string) ([]byte, error) {
	resp, err := c.resty.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		Get(audioURL)
	if err != nil {
		return nil, fmt.Errorf("下载合成音频失败: %w", err)
	}
	defer resp.RawBody().Close()
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("下载合成音频失败(HTTP %d)", resp.StatusCode())
	}
	return io.ReadAll(resp.RawBody())
}

// ttsLogidSuffix 从响应头取 X-Tt-Logid 拼进错误消息，便于用户反馈时定位问题。
func ttsLogidSuffix(resp *resty.Response) string {
	logid := resp.Header().Get("X-Tt-Logid")
	if logid == "" {
		return ""
	}
	return fmt.Sprintf("（logid %s）", logid)
}

// secToMS 秒（float64）→ 毫秒，四舍五入并兜底非负。
func secToMS(sec float64) int64 {
	if sec <= 0 {
		return 0
	}
	return int64(sec*1000 + 0.5)
}
