package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireServerFFmpeg 真依赖 ffmpeg 的端到端用例守卫：CI 无 ffmpeg 时跳过（测试可移植）。
func requireServerFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("本机无 ffprobe，跳过")
	}
}

// genSineWav 生成指定秒数的 440Hz 正弦 wav（入库输入样本）。
func genSineWav(t *testing.T, seconds float64) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sine.wav")
	if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=2", p).CombinedOutput(); err != nil {
		t.Fatalf("生成正弦失败: %v: %s", err, out)
	}
	return p
}

// voiceMultipart 构造音色入库 multipart 表单（字段 name + file）发起 POST，返回业务包络。
func voiceMultipart(t *testing.T, ac *http.Client, url, name, filename string, content []byte) envelope {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("name", name); err != nil {
		t.Fatal(err)
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e envelope
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return e
}

// postVoiceFile 读磁盘 wav 后走 multipart 入库（真 ffmpeg 用例）。
func postVoiceFile(t *testing.T, ac *http.Client, url, name, wavPath string) envelope {
	t.Helper()
	data, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	return voiceMultipart(t, ac, url, name, filepath.Base(wavPath), data)
}

// TestVoiceLibraryFlow 端到端：入库 → 列表 → stream → PATCH 改名 → DELETE → 404 语义。
func TestVoiceLibraryFlow(t *testing.T) {
	requireServerFFmpeg(t)
	ts, _, ac := newTestServer(t)

	// 入库（multipart: name + file）
	e := postVoiceFile(t, ac, ts.URL+"/api/voice-library", "参考甲", genSineWav(t, 2))
	if e.Code != CodeOK {
		t.Fatalf("入库 code = %d (%s)", e.Code, e.Message)
	}
	added, _ := e.Data.(map[string]any)
	id, _ := added["id"].(string)
	if id == "" {
		t.Fatalf("入库返回缺 id: %v", e.Data)
	}
	if ms, _ := added["duration_ms"].(float64); ms < 1500 || ms > 2500 {
		t.Errorf("duration_ms = %v, want 2000±500", added["duration_ms"])
	}

	// 列表可见
	list := getEnvelope(t, ac, ts.URL+"/api/voice-library")
	items, _ := list.Data.(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items = %v, want 1 条", list.Data)
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "参考甲" || first["id"] != id {
		t.Errorf("list[0] = %v, want id=%s name=参考甲", first, id)
	}

	// stream 200（二进制流端点，不套 JSON 包络）
	resp, err := ac.Get(ts.URL + "/api/voice-library/" + id + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	n, _ := resp.Body.Read(head)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(head[:n]) != "RIFF" {
		t.Errorf("stream status = %d head = %q, want 200 + RIFF(wav)", resp.StatusCode, string(head[:n]))
	}

	// PATCH 改名 → 列表读到新名
	status, e2 := doJSON(t, ac, http.MethodPatch, ts.URL+"/api/voice-library/"+id, `{"name":"参考乙"}`)
	if status != 200 || e2.Code != CodeOK {
		t.Fatalf("PATCH code = %d (%s) status=%d", e2.Code, e2.Message, status)
	}
	list2 := getEnvelope(t, ac, ts.URL+"/api/voice-library")
	items2, _ := list2.Data.(map[string]any)["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("list2 items = %v", list2.Data)
	}
	if name, _ := items2[0].(map[string]any)["name"].(string); name != "参考乙" {
		t.Errorf("改名后 list name = %q, want 参考乙", name)
	}

	// DELETE → 列表清空
	status, e3 := doJSON(t, ac, http.MethodDelete, ts.URL+"/api/voice-library/"+id, "")
	if status != 200 || e3.Code != CodeOK {
		t.Fatalf("DELETE code = %d (%s) status=%d", e3.Code, e3.Message, status)
	}
	list3 := getEnvelope(t, ac, ts.URL+"/api/voice-library")
	if items3, _ := list3.Data.(map[string]any)["items"].([]any); len(items3) != 0 {
		t.Errorf("删除后 list items = %v, want 空", items3)
	}

	// 404 语义：PATCH/DELETE 未知 id → 业务码 6；stream 未知 id → 真实 HTTP 404
	_, e4 := doJSON(t, ac, http.MethodPatch, ts.URL+"/api/voice-library/00000000", `{"name":"x"}`)
	if e4.Code != CodeNotFound {
		t.Errorf("PATCH 未知 id code = %d (%s), want %d", e4.Code, e4.Message, CodeNotFound)
	}
	_, e5 := doJSON(t, ac, http.MethodDelete, ts.URL+"/api/voice-library/00000000", "")
	if e5.Code != CodeNotFound {
		t.Errorf("DELETE 未知 id code = %d (%s), want %d", e5.Code, e5.Message, CodeNotFound)
	}
	miss, err := ac.Get(ts.URL + "/api/voice-library/00000000/stream")
	if err != nil {
		t.Fatal(err)
	}
	_ = miss.Body.Close()
	if miss.StatusCode != http.StatusNotFound {
		t.Errorf("stream 未知 id status = %d, want 404", miss.StatusCode)
	}
}

// tempDirSnapshot 记录系统临时目录当前文件名集合（泄漏断言用的快照）。
func tempDirSnapshot() map[string]bool {
	out := map[string]bool{}
	if entries, err := os.ReadDir(os.TempDir()); err == nil {
		for _, e := range entries {
			out[e.Name()] = true
		}
	}
	return out
}

// TestVoiceLibraryValidation 无需 ffmpeg 的参数校验（校验先于转码）：
// name 缺失 → code 2；超 20MB → code 2 且临时目录不残留解析临时文件
// （20MB+1 走 fh.Size 兜底路径，22MB 触发 MaxBytesReader 解析期截断路径）。
func TestVoiceLibraryValidation(t *testing.T) {
	ts, _, ac := newTestServer(t)

	// name 为空：code 2，且不触发 ffmpeg（假文件内容即可）
	e := voiceMultipart(t, ac, ts.URL+"/api/voice-library", "  ", "a.wav", []byte("FAKE"))
	if e.Code != CodeBadRequest || !strings.Contains(e.Message, "name") {
		t.Errorf("空 name code = %d (%s), want %d 且提示 name", e.Code, e.Message, CodeBadRequest)
	}

	before := tempDirSnapshot()
	// 20MB+1：正文 < 21MB 读限，multipart 解析成功，由 fh.Size 兜底拒收
	e2 := voiceMultipart(t, ac, ts.URL+"/api/voice-library", "大文件", "big.wav", bytes.Repeat([]byte{0}, 20<<20+1))
	if e2.Code != CodeBadRequest || !strings.Contains(e2.Message, "20MB") {
		t.Errorf("超限文件 code = %d (%s), want %d 且提示 20MB", e2.Code, e2.Message, CodeBadRequest)
	}
	// 22MB：正文超 21MB 读限，multipart 解析期即被 MaxBytesReader 截断拒收
	e3 := voiceMultipart(t, ac, ts.URL+"/api/voice-library", "大文件", "big.wav", bytes.Repeat([]byte{0}, 22<<20))
	if e3.Code != CodeBadRequest {
		t.Errorf("超读限文件 code = %d (%s), want %d", e3.Code, e3.Message, CodeBadRequest)
	}
	// 泄漏断言：两次超限请求不得在系统临时目录留下本 handler 相关的新文件
	//（multipart 解析落盘临时文件名前缀 "multipart-"；本 handler 临时文件前缀
	// "voxbox-voice-"。只比对这两类前缀，系统临时目录由多进程共享，全量比对会误伤他方文件）。
	for name := range tempDirSnapshot() {
		if !before[name] && (strings.HasPrefix(name, "multipart-") || strings.HasPrefix(name, "voxbox-voice-")) {
			t.Errorf("超限请求在临时目录残留文件: %s", name)
		}
	}
}
