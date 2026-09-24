package zhipu

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

// BaseURL 智谱开放平台固定入口，不提供覆写。
const BaseURL = "https://open.bigmodel.cn/api"

const (
	pathASR        = "/paas/v4/audio/transcriptions"
	pathTTS        = "/paas/v4/audio/speech"
	pathVoiceList  = "/paas/v4/voice/list"
	pathVoiceClone = "/paas/v4/voice/clone"
	pathVoiceDel   = "/paas/v4/voice/delete"
	pathFiles      = "/paas/v4/files"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// apiError 智谱错误体：{"error":{"code","message"}}。
type apiError struct {
	Err struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e apiError) Error() string {
	if e.Err.Message == "" {
		return "智谱 API 错误（响应无错误详情）"
	}
	if e.Err.Code != "" {
		return fmt.Sprintf("智谱 API 错误: %s（%s）", e.Err.Message, e.Err.Code)
	}
	return fmt.Sprintf("智谱 API 错误: %s", e.Err.Message)
}

// decodeError 判定非 2xx 响应：智谱错误体 → apiError，无错误体 → HTTP 状态行。
func decodeError(status int, body []byte) error {
	var ae apiError
	_ = json.Unmarshal(body, &ae)
	if ae.Err.Message != "" {
		return ae
	}
	return fmt.Errorf("智谱 API HTTP %d: %s", status, truncate(body, 200))
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// doJSON 发送 JSON 请求并解码响应（音色复刻/删除等）。
func doJSON(ctx context.Context, method, url, apiKey string, body any, out any) error {
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
	return do(req, out)
}

// doMultipart 构造 multipart/form-data 并发送：fields 为文本字段，extra 为追加的
// 重复键值对（数组字段按 form 语义展开），fileField/filePath 为二进制文件字段
// （ASR 与文件上传共用）。
func doMultipart(ctx context.Context, url, apiKey string, fields map[string]string, fileField, filePath string, out any, extra ...[2]string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return err
		}
	}
	for _, kv := range extra {
		if err := w.WriteField(kv[0], kv[1]); err != nil {
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
	return do(req, out)
}

// do 发请求/收 JSON：非 2xx 按智谱错误体转译。
func do(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求智谱平台失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析智谱响应失败: %w", err)
	}
	return nil
}

// doBytes 发送 JSON 请求并返回原始响应体（TTS 音频二进制；错误时响应仍为 JSON 错误体）。
func doBytes(ctx context.Context, method, url, apiKey string, body any) ([]byte, string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("请求智谱平台失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", decodeError(resp.StatusCode, data)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// paramString 与 qianwen/xiaomi 同款小工具（包内私有）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}

// resolveOut 相对路径锚定 outDir 并回算相对形态；绝对 _out 仅当落在 outDir 内时
// 归一为相对展示路径，outDir 外保持绝对（与 qianwen/xiaomi 同款实现）。
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

// paramBool 取布尔参数（缺失用默认；兼容字符串 "false"）。
func paramBool(params map[string]any, key string, def bool) bool {
	switch v := params[key].(type) {
	case bool:
		return v
	case string:
		if strings.EqualFold(v, "false") {
			return false
		}
		if strings.EqualFold(v, "true") {
			return true
		}
	}
	return def
}
