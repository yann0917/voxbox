package zhipu

import (
	"context"
	"fmt"
	"strings"
)

// TTSSynthesizeReq 非流式合成请求。wire 结构（官方 api-reference/模型-api/文本转语音，
// 2026-09-25 校准）：JSON body，响应 200 为音频二进制（非 JSON）；
// response_format 默认 pcm，流式仅 pcm——voxbox 固定 wav（可播）。
type TTSSynthesizeReq struct {
	Text   string  // ≤1024 字符
	Voice  string  // 官方音色（tongtong 等）或复刻音色（voice_clone_*）
	Speed  float64 // [0.5, 2]，0 = 上游默认 1.0
	Volume float64 // (0, 10]，0 = 上游默认 1.0
}

// Synthesize 合成并返回音频字节（wav）：响应为二进制，错误时才是 JSON 错误体。
func (c *TTSClient) Synthesize(ctx context.Context, req TTSSynthesizeReq) ([]byte, error) {
	if strings.TrimSpace(req.Voice) == "" {
		return nil, fmt.Errorf("智谱 TTS 缺少必填参数: voice")
	}
	body := map[string]any{
		"model":           "glm-tts",
		"input":           req.Text,
		"voice":           req.Voice,
		"response_format": "wav",
		"stream":          false,
	}
	if req.Speed > 0 {
		body["speed"] = req.Speed
	}
	if req.Volume > 0 {
		body["volume"] = req.Volume
	}
	data, ctype, err := doBytes(ctx, "POST", c.baseURL+pathTTS, c.apiKey, body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("智谱 TTS 响应为空")
	}
	_ = ctype // 200 成功时固定音频二进制（audio/wav），格式以请求 response_format 为准
	return data, nil
}

// TTSClient 智谱 TTS 客户端。
type TTSClient struct{ apiKey, baseURL string }

func NewTTSClient(apiKey, baseURL string) *TTSClient {
	return &TTSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}
