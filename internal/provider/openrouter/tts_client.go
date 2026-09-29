package openrouter

import (
	"context"
	"fmt"
	"strings"
)

// TTSReq 非流式合成请求。wire 结构（OpenRouter 文本转语音，2026-09-29 校准）：
// JSON body，成功 200 为音频二进制（非 JSON，勿按 JSON 解析）；失败才返回 JSON 错误体。
// response_format 可选 mp3/pcm，默认 pcm 裸流——无文件头不可播，voxbox 固定 mp3
// （自带封装，浏览器可直接试听）。
type TTSReq struct {
	Text  string
	Voice string // provider 预置音色名（Zephyr 等 30 个）
}

// Synthesize 合成并返回音频字节（mp3）。
func (c *TTSClient) Synthesize(ctx context.Context, req TTSReq) ([]byte, error) {
	if strings.TrimSpace(req.Voice) == "" {
		return nil, fmt.Errorf("OpenRouter TTS 缺少必填参数: voice")
	}
	body := map[string]any{
		"model":           ModelTTS,
		"input":           req.Text,
		"voice":           req.Voice,
		"response_format": "mp3",
	}
	data, err := doBytes(ctx, c.baseURL+pathTTS, c.apiKey, body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("OpenRouter TTS 响应为空")
	}
	return data, nil
}

// TTSClient OpenRouter TTS 客户端。
type TTSClient struct{ apiKey, baseURL string }

func NewTTSClient(apiKey, baseURL string) *TTSClient {
	return &TTSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}
