package qianwen

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

// BaseURL 千问平台开放 API 固定入口（DashScope 兼容协议），不提供覆写。
const BaseURL = "https://maas.qianwenaiapi.com"

const (
	pathTTSGen  = "/api/v1/services/aigc/multimodal-generation/generation"
	pathASRSub  = "/api/v1/services/audio/asr/transcription"
	pathTaskFmt = "/api/v1/tasks/%s"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// apiError DashScope 风格错误体。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Request string `json:"request_id"`
}

func (e apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("千问 API 错误: %s", e.Code)
	}
	return fmt.Sprintf("千问 API 错误: %s（%s）", e.Message, e.Code)
}

// doJSON 发请求/收 JSON：非 2xx 或 body 带 code/message 错误时返回 apiError。
func doJSON(ctx context.Context, method, url, apiKey string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求千问平台失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var ae apiError
		_ = json.Unmarshal(raw, &ae)
		if ae.Code == "" {
			return fmt.Errorf("千问 API HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
		}
		return ae
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析千问响应失败: %w", err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// ---- TTS ----

// TTSReq 非流式合成请求（工具层语义字段，client 负责映射为 messages 协议）。
type TTSReq struct {
	Model        string // qwen3-tts-flash | qwen3-tts-instruct-flash
	Text         string
	Voice        string
	LanguageType string // 可空=不指定
	Instructions string // 仅 instruct 模型
}

// TTSResult 合成产物：音频字节与容器格式（按 data URI mime 或 URL 扩展名推断）。
type TTSResult struct {
	Audio  []byte
	Format string
}

type ttsRequest struct {
	Model      string         `json:"model"`
	Input      ttsInput       `json:"input"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type ttsInput struct {
	Messages []ttsMessage `json:"messages"`
}

type ttsMessage struct {
	Role    string           `json:"role"`
	Content []ttsContentItem `json:"content"`
}

type ttsContentItem struct {
	Text         string `json:"text,omitempty"`
	Voice        string `json:"voice,omitempty"`
	LanguageType string `json:"language_type,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type ttsResponse struct {
	Output struct {
		Choices []struct {
			Message struct {
				Content []map[string]any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"output"`
	apiError
}

// TTSClient 千问非流式 TTS 客户端。
type TTSClient struct{ apiKey, baseURL string }

func NewTTSClient(apiKey, baseURL string) *TTSClient {
	return &TTSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// Synthesize 合成并取回音频字节：响应里的 audio 项为公网 URL（24h 有效）时下载，
// 为 data URI 时直接 base64 解码。
func (c *TTSClient) Synthesize(ctx context.Context, req TTSReq) (TTSResult, error) {
	content := []ttsContentItem{{Text: req.Text}}
	if req.Voice != "" {
		content = append(content, ttsContentItem{Voice: req.Voice})
	}
	if req.LanguageType != "" {
		content = append(content, ttsContentItem{LanguageType: req.LanguageType})
	}
	if req.Instructions != "" {
		content = append(content, ttsContentItem{Instructions: req.Instructions})
	}
	body := ttsRequest{
		Model:      req.Model,
		Input:      ttsInput{Messages: []ttsMessage{{Role: "user", Content: content}}},
		Parameters: map[string]any{"stream": false},
	}
	var resp ttsResponse
	if err := doJSON(ctx, http.MethodPost, c.baseURL+pathTTSGen, c.apiKey, nil, body, &resp); err != nil {
		return TTSResult{}, err
	}
	for _, ch := range resp.Output.Choices {
		for _, item := range ch.Message.Content {
			v, ok := item["audio"].(string)
			if !ok || v == "" {
				continue
			}
			return fetchAudio(ctx, v)
		}
	}
	return TTSResult{}, fmt.Errorf("千问 TTS 响应中未找到音频（content 项无 audio 键）")
}

// fetchAudio audio 载体两种形态：data URI 直接解码；URL 下载（格式按扩展名推断）。
func fetchAudio(ctx context.Context, v string) (TTSResult, error) {
	if strings.HasPrefix(v, "data:") {
		// data:audio/mpeg;base64,XXXX
		semi := strings.Index(v, ";")
		if semi < 0 || !strings.HasPrefix(v[semi:], ";base64,") {
			return TTSResult{}, fmt.Errorf("无法解析的 data URI 音频")
		}
		mime := strings.TrimPrefix(v[5:semi], "audio/")
		raw, err := base64.StdEncoding.DecodeString(v[semi+len(";base64,"):])
		if err != nil {
			return TTSResult{}, fmt.Errorf("解码音频失败: %w", err)
		}
		return TTSResult{Audio: raw, Format: audioFormatOfMime(mime)}, nil
	}
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		return TTSResult{}, fmt.Errorf("音频地址不合法: %s", truncate([]byte(v), 80))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v, nil)
	if err != nil {
		return TTSResult{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return TTSResult{}, fmt.Errorf("下载合成音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TTSResult{}, fmt.Errorf("下载合成音频失败: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return TTSResult{}, err
	}
	format := strings.TrimPrefix(extOfURL(v), ".")
	if format == "" {
		format = audioFormatOfMime(strings.TrimPrefix(resp.Header.Get("Content-Type"), "audio/"))
	}
	return TTSResult{Audio: raw, Format: format}, nil
}

func audioFormatOfMime(mime string) string {
	switch mime {
	case "mpeg", "mp3":
		return "mp3"
	case "wav", "x-wav", "wave":
		return "wav"
	case "pcm":
		return "pcm"
	}
	return mime
}

func extOfURL(raw string) string {
	u := strings.SplitN(raw, "?", 2)[0]
	if i := strings.LastIndex(u, "."); i >= 0 {
		return strings.ToLower(u[i:])
	}
	return ""
}
