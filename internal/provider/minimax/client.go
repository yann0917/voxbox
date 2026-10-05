// Package minimax MiniMax 开放平台（platform.minimax.cn）provider：
// speech-2.8 同步/异步语音合成（同步响应为 JSON 内 hex 编码音频）、asr-1.0 语音识别
// （multipart 直传，≤500 秒/50MB，verbose_json 带说话人分离与句级时间戳）、
// MiniMax-M3/M2.7 文本大模型（OpenAI 兼容 chat/completions，供 assistant 链路复用）。
// Bearer API Key 鉴权，BaseURL 固定官方。
package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BaseURL MiniMax 开放平台固定入口，不提供覆写。
const BaseURL = "https://api.minimax.cn"

const (
	pathT2A          = "/v1/t2a_v2"
	pathT2AAsync     = "/v1/t2a_async_v2"
	pathT2AQuery     = "/v1/query/t2a_async_query_v2"
	pathASR          = "/v1/speech_to_text"
	pathGetVoice     = "/v1/get_voice"
	pathFileRetrieve = "/v1/files/retrieve_content"
)

// httpClient 各端点均为同步请求，耗时随文本/音频长度增长，超时放宽到 300s
// （异步任务创建/查询走短超时的 pollClient）。
var httpClient = &http.Client{Timeout: 300 * time.Second}

// pollClient 异步任务创建/查询/文件下载：单请求轻量，60s 足够。
var pollClient = &http.Client{Timeout: 60 * time.Second}

// baseResp MiniMax 经典响应包络内的状态对象：200 成功响应里 status_code 也可能非 0
// （官方仅承诺 HTTP 200，业务错误走 base_resp.status_code）。
type baseResp struct {
	StatusCode int64  `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

// errText base_resp 业务错误的用户可读形态；status_code=0 返回空串。
func (b baseResp) errText() string {
	if b.StatusCode == 0 {
		return ""
	}
	if b.StatusMsg != "" {
		return fmt.Sprintf("MiniMax API 错误: %s（code %d）", b.StatusMsg, b.StatusCode)
	}
	return fmt.Sprintf("MiniMax API 错误（code %d）", b.StatusCode)
}

// apiError 兼容两类错误体：语音接口的 base_resp 包络（HTTP 200 + 业务码）与
// 识别接口的 OpenAI 风格 {"type":"error","error":{"type","message","http_code"}}
// （HTTP 状态码为真实错误码）。二者都可能在非 2xx 下出现，按字段存在性判定。
type apiError struct {
	BaseResp baseResp `json:"base_resp"`
	Err      *struct {
		Type     string `json:"type"`
		Message  string `json:"message"`
		HTTPCode string `json:"http_code"`
	} `json:"error"`
}

// Error 实现 error：base_resp 业务码优先（语音系），其次 OpenAI 风格错误体（识别系）。
func (e apiError) Error() string {
	if msg := e.BaseResp.errText(); msg != "" {
		return msg
	}
	if e.Err != nil && e.Err.Message != "" {
		return fmt.Sprintf("MiniMax API 错误: %s", e.Err.Message)
	}
	return "MiniMax API 错误（响应无错误详情）"
}

// decodeError 判定失败响应：错误体 → apiError（含 200 包络内的业务错误），无错误体
// → HTTP 状态行。
func decodeError(status int, body []byte) error {
	var ae apiError
	_ = json.Unmarshal(body, &ae)
	if ae.BaseResp.StatusCode != 0 || (ae.Err != nil && ae.Err.Message != "") {
		return ae
	}
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("MiniMax API HTTP %d: %s", status, truncate(body, 200))
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// doJSON 发送 JSON 请求并解码响应体（音色列表/异步任务创建/查询）。
// MiniMax 语音系接口业务错误也回 HTTP 200，这里一并交给 decodeError 判定。
func doJSON(ctx context.Context, client *http.Client, method, url, apiKey string, body any, out any) error {
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
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 MiniMax 平台失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := decodeError(resp.StatusCode, data); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 MiniMax 响应失败: %w", err)
	}
	return nil
}

// doBytes 发送 JSON 请求并返回原始响应体：T2A 成功为 JSON（音频 hex 在 data.audio），
// 失败才可能是错误包络。
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
		return nil, fmt.Errorf("请求 MiniMax 平台失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := decodeError(resp.StatusCode, data); err != nil {
		return nil, err
	}
	return data, nil
}

// doMultipart 构造 multipart/form-data 并发送：fields 为文本字段，headers 为附加
// 请求头（识别的 language 走 header 不走表单），fileField/filePath 为二进制文件字段。
func doMultipart(ctx context.Context, url, apiKey string, fields map[string]string, headers map[string]string, fileField, filePath string, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return err
		}
	}
	if filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("读取文件失败: %w", err)
		}
		defer f.Close()
		part, err := w.CreateFormFile(fileField, filepath.Base(filePath))
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, f); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 MiniMax 平台失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := decodeError(resp.StatusCode, data); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 MiniMax 响应失败: %w", err)
	}
	return nil
}

// doDownload GET 拉取二进制内容（异步合成的 retrieve_content 下载，Bearer 鉴权）。
func doDownload(ctx context.Context, url, apiKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 MiniMax 文件失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 错误时响应为 JSON 包络（base_resp），复用 decodeError 转译
		if err := decodeError(resp.StatusCode, data); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("下载 MiniMax 文件失败: HTTP %d", resp.StatusCode)
	}
	return data, nil
}

// paramString 与 qianwen/xiaomi/zhipu 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}

// paramFloat 取浮点参数（缺失/非法返回 0，由调用方决定默认语义）。
func paramFloat(params map[string]any, key string) float64 {
	switch v := params[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

// secToMS 秒（float64）→ 毫秒，四舍五入并兜底非负（浮点直转会在 12.744 这类值上
// 截断丢 1 毫秒，识别时间轴与 SRT 都依赖此口径）。
func secToMS(sec float64) int64 {
	if sec <= 0 {
		return 0
	}
	return int64(sec*1000 + 0.5)
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
