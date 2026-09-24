package xiaomi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseURL 小米 MiMo 开放平台固定入口（OpenAI 兼容协议），不提供覆写。
const BaseURL = "https://api.xiaomimimo.com"

const pathChatCompletions = "/v1/chat/completions"

var httpClient = &http.Client{Timeout: 60 * time.Second}

// apiError OpenAI 风格错误体（字段名 Err 避让 error 接口方法 Error）。
type apiError struct {
	Err struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (e apiError) Error() string {
	if e.Err.Message == "" {
		return "小米 API 错误（响应无错误详情）"
	}
	if e.Err.Code != "" {
		return fmt.Sprintf("小米 API 错误: %s（%s）", e.Err.Message, e.Err.Code)
	}
	return fmt.Sprintf("小米 API 错误: %s", e.Err.Message)
}

// TTSReq 非流式合成请求。wire 结构（官方 static/docs/api/audio/tts.md，2026-09-24 校准）：
// OpenAI 兼容 chat/completions——合成文本放 assistant 消息，自然语言风格指令/音色描述
// 放 user 消息，audio.format 与 audio.voice 平铺在顶层 audio 对象。
type TTSReq struct {
	Model        string // mimo-v2.5-tts | mimo-v2.5-tts-voicedesign
	Text         string // 合成文本（assistant 消息）
	Voice        string // 预置音色 ID，仅 mimo-v2.5-tts 生效（voicedesign 上游不支持该字段）
	Instructions string // user 消息：风格指令；voicedesign 模型下为必填的音色描述
	Format       string // wav | mp3（pcm16 为流式通道专用，非流式不出）
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ttsAudio struct {
	Format string `json:"format,omitempty"`
	Voice  string `json:"voice,omitempty"`
}

type ttsRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Audio    ttsAudio      `json:"audio"`
	Stream   bool          `json:"stream"`
}

// ttsResponse 非流式响应：音频 base64 在 choices[0].message.audio.data。
type ttsResponse struct {
	Choices []struct {
		Message struct {
			Audio struct {
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"message"`
	} `json:"choices"`
	apiError
}

// TTSClient 小米 MiMo TTS 客户端（OpenAI 兼容，非流式）。
type TTSClient struct{ apiKey, baseURL string }

func NewTTSClient(apiKey, baseURL string) *TTSClient {
	return &TTSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// Synthesize 合成并返回音频字节：非流式响应 message.audio.data 为 base64 音频。
func (c *TTSClient) Synthesize(ctx context.Context, req TTSReq) ([]byte, error) {
	messages := []chatMessage{}
	if strings.TrimSpace(req.Instructions) != "" {
		messages = append(messages, chatMessage{Role: "user", Content: req.Instructions})
	}
	messages = append(messages, chatMessage{Role: "assistant", Content: req.Text})
	var resp ttsResponse
	if err := doOpenAI(ctx, httpClient, http.MethodPost, c.baseURL+pathChatCompletions, c.apiKey, ttsRequest{
		Model:    req.Model,
		Messages: messages,
		Audio:    ttsAudio{Format: req.Format, Voice: req.Voice},
		Stream:   false,
	}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Audio.Data == "" {
		return nil, fmt.Errorf("小米 TTS 响应中未找到音频（choices[0].message.audio.data 为空）")
	}
	audio, err := base64.StdEncoding.DecodeString(resp.Choices[0].Message.Audio.Data)
	if err != nil {
		return nil, fmt.Errorf("解码音频失败: %w", err)
	}
	return audio, nil
}

// doOpenAI 发送 OpenAI 兼容 JSON 请求并解码响应：非 2xx 按 error 体转译为 apiError，
// 2xx 解码进 out。hc 由调用方选定（TTS 短超时 / ASR 上传+转写长超时），TTS 与 ASR 共用。
func doOpenAI(ctx context.Context, hc *http.Client, method, url, apiKey string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("请求小米 MiMo 平台失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var ae apiError
		_ = json.Unmarshal(data, &ae)
		if ae.Err.Message == "" {
			return fmt.Errorf("小米 API HTTP %d: %s", resp.StatusCode, truncate(data, 200))
		}
		return ae
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析小米响应失败: %w", err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
