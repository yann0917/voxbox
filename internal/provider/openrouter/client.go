package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// BaseURL OpenRouter 官方 API 固定入口，不提供覆写。
const BaseURL = "https://openrouter.ai/api/v1"

const pathTTS = "/audio/speech"

// httpClient TTS 为整段生成（非流式），耗时随文本长度增长，超时放宽到 120s。
var httpClient = &http.Client{Timeout: 120 * time.Second}

// apiError OpenRouter 错误体：{"error":{"code","message"}}，code 为数字。
type apiError struct {
	Err struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e apiError) Error() string {
	if e.Err.Code != nil {
		return fmt.Sprintf("OpenRouter API 错误: %s（code %v）", e.Err.Message, e.Err.Code)
	}
	return fmt.Sprintf("OpenRouter API 错误: %s", e.Err.Message)
}

// decodeError 判定非 2xx 响应：错误体 → apiError，无错误体 → HTTP 状态行。
func decodeError(status int, body []byte) error {
	var ae apiError
	_ = json.Unmarshal(body, &ae)
	if ae.Err.Message != "" {
		return ae
	}
	return fmt.Errorf("OpenRouter API HTTP %d: %s", status, truncate(body, 200))
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// doBytes 发送 JSON 请求并返回原始响应体：TTS 成功为音频二进制，失败为 JSON 错误体。
func doBytes(ctx context.Context, url, apiKey string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 OpenRouter 失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeError(resp.StatusCode, data)
	}
	return data, nil
}

// paramString 与 qianwen/xiaomi/zhipu 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}

// resolveOut 相对路径锚定 outDir 并回算相对形态；绝对 _out 仅当落在 outDir 内时
// 归一为相对展示路径，outDir 外保持绝对（与 qianwen/xiaomi/zhipu 同款实现）。
func resolveOut(outDir, p string) (abs, rel string) {
	outDir = filepath.Clean(outDir)
	abs = p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(outDir, p)
	}
	if !strings.HasPrefix(abs, outDir+string(filepath.Separator)) {
		return abs, abs
	}
	rel, _ = filepath.Rel(outDir, abs)
	return abs, rel
}
