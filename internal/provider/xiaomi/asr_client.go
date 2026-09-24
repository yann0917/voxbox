package xiaomi

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// asrHTTPClient ASR 同步转写专用客户端：请求体携带 base64 音频（≤10MB）且转写文本随
// 响应同步返回，耗时远高于普通 JSON 调用，复用 TTS 的 60s 短超时会掐断大文件。
var asrHTTPClient = &http.Client{Timeout: 10 * time.Minute}

// asrModel 语音识别模型（官方当前仅此一款，不做枚举参数）。
const asrModel = "mimo-v2.5-asr"

// ASRInput 同步转写入参。wire 结构（官方 static/docs/api/audio/Speech-Recognition.md，
// 2026-09-25 校准）：音频以 input_audio 内容部件进 user 消息，data 为 data URI 自描述
// MIME（裸 base64 须另附 format 字段，两种形态上游均收）；asr_options.language 平铺顶层。
type ASRInput struct {
	Audio    []byte // 原始音频字节（mp3/wav，base64 后 ≤10MB）
	MIME     string // audio/mpeg | audio/wav（sniffAudioMIME 推断）
	Language string // ""|auto → 不发送（上游默认自动识别）；zh|en 显式指定
}

type asrContentPart struct {
	Type       string     `json:"type"` // 固定 input_audio（官方仅支持单音频输入）
	InputAudio asrAudioIn `json:"input_audio"`
}

type asrAudioIn struct {
	Data string `json:"data"`
}

type asrMessage struct {
	Role    string           `json:"role"`
	Content []asrContentPart `json:"content"`
}

type asrOptions struct {
	Language string `json:"language,omitempty"`
}

type asrRequest struct {
	Model      string       `json:"model"`
	Messages   []asrMessage `json:"messages"`
	ASROptions *asrOptions  `json:"asr_options,omitempty"`
	Stream     bool         `json:"stream"`
}

type asrResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"` // 转写文本（无时间戳结构）
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokensDetails struct {
			Seconds int `json:"seconds"` // 音频时长（秒）
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	apiError
}

// ASRClient 小米 MiMo ASR 客户端（OpenAI 兼容，同步直返文本）。
type ASRClient struct{ apiKey, baseURL string }

func NewASRClient(apiKey, baseURL string) *ASRClient {
	return &ASRClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// Transcribe 转写音频：返回文本与音频时长秒数（usage.prompt_tokens_details.seconds，
// 上游不回时间戳，无法产出分句/SRT）。
func (c *ASRClient) Transcribe(ctx context.Context, in ASRInput) (string, int, error) {
	if len(in.Audio) == 0 {
		return "", 0, fmt.Errorf("小米 ASR 缺少音频输入")
	}
	body := asrRequest{
		Model: asrModel,
		Messages: []asrMessage{{Role: "user", Content: []asrContentPart{{
			Type:       "input_audio",
			InputAudio: asrAudioIn{Data: "data:" + in.MIME + ";base64," + base64.StdEncoding.EncodeToString(in.Audio)},
		}}}},
		Stream: false,
	}
	if lang := strings.TrimSpace(in.Language); lang != "" && lang != "auto" {
		body.ASROptions = &asrOptions{Language: lang}
	}
	var resp asrResponse
	if err := doOpenAI(ctx, asrHTTPClient, http.MethodPost, c.baseURL+pathChatCompletions, c.apiKey, body, &resp); err != nil {
		return "", 0, err
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return "", 0, fmt.Errorf("小米 ASR 响应中未找到转写文本（choices[0].message.content 为空）")
	}
	return resp.Choices[0].Message.Content, resp.Usage.PromptTokensDetails.Seconds, nil
}

// sniffAudioMIME 按魔数判别音频格式（官方仅收 mp3/wav）：mp3 = ID3 头或 MPEG 帧同步，
// wav = RIFF…WAVE。识别不出返回空串，交由调用方报格式错误。
func sniffAudioMIME(b []byte) string {
	switch {
	case len(b) >= 3 && string(b[:3]) == "ID3":
		return "audio/mpeg"
	case len(b) >= 2 && b[0] == 0xFF && b[1]&0xE6 == 0xE2: // MPEG 音频帧同步（Layer I/II/III）
		return "audio/mpeg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return "audio/wav"
	}
	return ""
}
