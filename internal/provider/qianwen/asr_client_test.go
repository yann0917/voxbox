package qianwen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 提交：X-DashScope-Async 头、qwen3 单 URL / qwen-audio 数组两种 input 形态、参数透传。
func TestASRClientSubmit(t *testing.T) {
	var async, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		async = r.Header.Get("X-DashScope-Async")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"output":{"task_id":"tid-1","task_status":"PENDING"}}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk-test", srv.URL)
	itn := true
	id, err := c.SubmitTranscription(context.Background(), "qwen3-asr-flash-filetrans",
		"https://a.b/x.mp3", ASRParams{LanguageHints: []string{"zh"}, EnableITN: &itn})
	if err != nil {
		t.Fatal(err)
	}
	if id != "tid-1" {
		t.Errorf("task_id = %q", id)
	}
	if async != "enable" {
		t.Errorf("X-DashScope-Async = %q", async)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "qwen3-asr-flash-filetrans" {
		t.Errorf("model = %v", m["model"])
	}
	in := m["input"].(map[string]any)
	if in["file_url"] != "https://a.b/x.mp3" {
		t.Errorf("file_url = %v", in["file_url"])
	}
	p := m["parameters"].(map[string]any)
	if _, ok := p["language_hints"].([]any); !ok {
		t.Errorf("language_hints 缺失: %v", p)
	}
	if p["enable_itn"] != true {
		t.Errorf("enable_itn = %v", p["enable_itn"])
	}
}

// qwen-audio 模型走 file_urls 数组。
func TestASRClientSubmitQwenAudio(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"output":{"task_id":"t","task_status":"PENDING"}}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk", srv.URL)
	if _, err := c.SubmitTranscription(context.Background(), "qwen-audio-3.1-asr-flash-filetrans",
		"https://a.b/x.mp3", ASRParams{}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	in := m["input"].(map[string]any)
	if urls, ok := in["file_urls"].([]any); !ok || len(urls) != 1 || urls[0] != "https://a.b/x.mp3" {
		t.Errorf("file_urls = %v", in["file_urls"])
	}
}

// 轮询查询：SUCCEEDED 时带出 transcription_url；任务查询带 Bearer 凭证，
// 拉取转录结果（跨域预签名 URL）不带 Authorization（避免凭证外泄）。
func TestASRClientQueryTask(t *testing.T) {
	var srv *httptest.Server
	var taskAuth, fetchAuth string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tasks/tid-1" {
			taskAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"output":{"task_id":"tid-1","task_status":"SUCCEEDED",
				"results":[{"transcription_url":"` + srv.URL + `/trans.json"}]}}`))
			return
		}
		fetchAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"transcripts":[{"sentences":[
			{"begin_time":0,"end_time":1500,"text":"你好","speaker_id":"1"},
			{"begin_time":1500,"end_time":3000,"text":"世界"}]}]}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk", srv.URL)
	task, err := c.QueryTask(context.Background(), "tid-1")
	if err != nil {
		t.Fatal(err)
	}
	if taskAuth != "Bearer sk" {
		t.Errorf("任务查询 Authorization = %q, want Bearer sk", taskAuth)
	}
	if task.Status != StatusSucceeded || len(task.TranscriptionURLs) != 1 {
		t.Fatalf("task = %+v", task)
	}
	tr, err := c.FetchTranscription(context.Background(), task.TranscriptionURLs[0])
	if err != nil {
		t.Fatal(err)
	}
	if fetchAuth != "" {
		t.Errorf("拉取转录结果不应附带 Authorization 头, got %q", fetchAuth)
	}
	if len(tr.Transcripts) != 1 || len(tr.Transcripts[0].Sentences) != 2 {
		t.Fatalf("transcription = %+v", tr)
	}
	if tr.Transcripts[0].Sentences[0].Text != "你好" || tr.Transcripts[0].Sentences[0].SpeakerID != "1" {
		t.Errorf("sentence[0] = %+v", tr.Transcripts[0].Sentences[0])
	}
}

// FAILED 状态查询：带出上游 message 供工具层转述。
func TestASRClientQueryTaskFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"output":{"task_id":"t","task_status":"FAILED","message":"音频解码失败"}}`))
	}))
	defer srv.Close()
	c := NewASRClient("sk", srv.URL)
	task, err := c.QueryTask(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusFailed {
		t.Fatalf("task.Status = %q, want FAILED", task.Status)
	}
	if !strings.Contains(task.Message, "音频解码失败") {
		t.Errorf("task.Message = %q, 应含上游 message", task.Message)
	}
}
