package server

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// uploadMultipart 构造 multipart 表单（字段名 file）发起上传，返回业务包络。
func uploadMultipart(t *testing.T, ac *http.Client, url, filename, content string) envelope {
	t.Helper()
	var buf strings.Builder
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", url, strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP status = %d, want 200 (envelope)", resp.StatusCode)
	}
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return e
}

func postJSON(t *testing.T, ac *http.Client, url, body string) envelope {
	t.Helper()
	resp, err := ac.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return e
}

// TestUploadAndCreateTaskWithFile 全链路：上传 → file_id → createTask 携带
// file_ids → 任务创建成功（无凭证会 failed，不阻塞断言）。
func TestUploadAndCreateTaskWithFile(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := uploadMultipart(t, ac, ts.URL+"/api/uploads", "a.mp3", "FAKE")
	if e.Code != 0 {
		t.Fatalf("upload code = %d (%s)", e.Code, e.Message)
	}
	data, _ := e.Data.(map[string]any)
	fileID, _ := data["file_id"].(string)
	if fileID == "" {
		t.Fatalf("file_id 为空: %v", e.Data)
	}
	if _, err := uuid.Parse(fileID); err != nil {
		t.Errorf("file_id 应为合法 UUID: %q", fileID)
	}

	body := `{"provider":"volcengine","tool":"asr","params":{},"file_ids":["` + fileID + `"]}`
	created := postJSON(t, ac, ts.URL+"/api/tasks", body)
	if created.Code != 0 {
		t.Fatalf("createTask code = %d (%s)", created.Code, created.Message)
	}
	createdData, _ := created.Data.(map[string]any)
	taskID, _ := createdData["task_id"].(string)
	if taskID == "" {
		t.Fatalf("created = %v", created)
	}

	detail := getEnvelope(t, ac, ts.URL+"/api/tasks/"+taskID)
	dData, _ := detail.Data.(map[string]any)
	if task, _ := dData["task"].(map[string]any); task == nil {
		t.Fatalf("task 应存在: %v", detail.Data)
	}
}

// TestUploadUnknownFileID createTask 携带不存在的 file_id → code 6（HTTP 200）。
func TestUploadUnknownFileID(t *testing.T) {
	ts, _, ac := newTestServer(t)
	body := `{"provider":"volcengine","tool":"asr","params":{},"file_ids":["` + uuid.NewString() + `"]}`
	e := postJSON(t, ac, ts.URL+"/api/tasks", body)
	if e.Code != CodeNotFound {
		t.Fatalf("code = %d (%s), want %d", e.Code, e.Message, CodeNotFound)
	}
}

// TestUploadStream 上传后可按 file_id 拉取二进制流；不存在的 id → 真实 HTTP 404。
func TestUploadStream(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := uploadMultipart(t, ac, ts.URL+"/api/uploads", "a.wav", "FAKE")
	if e.Code != 0 {
		t.Fatalf("upload code = %d (%s)", e.Code, e.Message)
	}
	data, _ := e.Data.(map[string]any)
	fileID, _ := data["file_id"].(string)

	resp, err := ac.Get(ts.URL + "/api/uploads/" + fileID + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 16)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "FAKE" {
		t.Fatalf("stream body = %q, want %q", string(buf[:n]), "FAKE")
	}

	miss, err := ac.Get(ts.URL + "/api/uploads/" + uuid.NewString() + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer miss.Body.Close()
	if miss.StatusCode != 404 {
		t.Fatalf("missing stream status = %d, want 404", miss.StatusCode)
	}
}

// TestUploadFileIDPathInjection file_id 非法（路径注入形态）→ 业务码报错，不允许穿越。
func TestUploadFileIDPathInjection(t *testing.T) {
	ts, _, ac := newTestServer(t)
	body := `{"provider":"volcengine","tool":"asr","params":{},"file_ids":["../evil"]}`
	e := postJSON(t, ac, ts.URL+"/api/tasks", body)
	if e.Code != CodeNotFound && e.Code != CodeBadRequest {
		t.Fatalf("code = %d (%s), want %d 或 %d", e.Code, e.Message, CodeNotFound, CodeBadRequest)
	}
	if e.Data != nil {
		t.Errorf("出错时 data 应为 nil: %v", e.Data)
	}
}

// TestUploadNoExtension 无扩展名文件 → code 2（file_id 按 Glob "<uuid>.*" 解析，
// 无扩展名落盘后必然解析不到），且 uploads 目录不产生新文件（不落盘）。
func TestUploadNoExtension(t *testing.T) {
	ts, s, ac := newTestServer(t)
	dir := s.uploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	e := uploadMultipart(t, ac, ts.URL+"/api/uploads", "noext", "FAKE")
	if e.Code != CodeBadRequest {
		t.Fatalf("code = %d (%s), want %d", e.Code, e.Message, CodeBadRequest)
	}
	after, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("uploads 文件数 %d → %d，拒绝时应不落盘", len(before), len(after))
	}
}

// TestUploadStreamSecurityHeaders stream 响应必须携带 nosniff 与 attachment
// Content-Disposition（同源直出二进制流的 XSS 防护面）。
func TestUploadStreamSecurityHeaders(t *testing.T) {
	ts, _, ac := newTestServer(t)
	e := uploadMultipart(t, ac, ts.URL+"/api/uploads", "a.wav", "FAKE")
	if e.Code != 0 {
		t.Fatalf("upload code = %d (%s)", e.Code, e.Message)
	}
	data, _ := e.Data.(map[string]any)
	fileID, _ := data["file_id"].(string)

	resp, err := ac.Get(ts.URL + "/api/uploads/" + fileID + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="`) || !strings.HasSuffix(cd, `.wav"`) {
		t.Fatalf("Content-Disposition = %q, want attachment; filename=\"<uuid>.wav\"", cd)
	}
}
