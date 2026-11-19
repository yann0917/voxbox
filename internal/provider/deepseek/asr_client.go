package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// PathASR 语音识别端点：一次请求发送完整音频，同步返回完整转写文本（不支持流式识别，
// 无时间戳、无说话人分离）。
const PathASR = "/v1/tools/asr"

// asrHTTPClient 同步转写专用客户端：上传 + 识别随响应同步返回（官方上传窗口 10 分钟），
// 复用 60s 短超时会掐断大文件。
var asrHTTPClient = &http.Client{Timeout: 10 * time.Minute}

type asrResponse struct {
	RequestID  string `json:"request_id"`
	Text       string `json:"text"`
	DurationMS int64  `json:"duration_ms"`
}

// Transcribe 转写音频：multipart file 字段直传（官方仅收 16bit PCM WAV / Ogg Opus，
// 格式由文件内容判断）；无语种参数，语种按音频自动识别（zh/yue/en/ja/ko/de/fr/es/
// pt/it/ru/ar/hi/id/vi/th/tr/ms/fil/pl/hu 21 语种）。返回转写文本与音频时长毫秒
// （上游不回时间戳，无法产出分句/SRT）。
func (c *Client) Transcribe(ctx context.Context, audio []byte) (string, int64, error) {
	if len(audio) == 0 {
		return "", 0, fmt.Errorf("DeepSeek ASR 缺少音频输入")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", 0, err
	}
	if _, err := part.Write(audio); err != nil {
		return "", 0, err
	}
	if err := w.Close(); err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+PathASR, &buf)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := asrHTTPClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("请求 DeepSeek 识别服务失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, decodeError(body, resp.StatusCode)
	}
	var out asrResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", 0, fmt.Errorf("解析 DeepSeek 识别响应失败: %w", err)
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", 0, fmt.Errorf("DeepSeek ASR 响应中未找到转写文本（text 为空）")
	}
	return out.Text, out.DurationMS, nil
}
