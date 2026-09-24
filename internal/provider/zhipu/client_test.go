package zhipu

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TTS 全链路：Bearer 鉴权、JSON body（model/input/voice/response_format/stream）、
// 响应为音频二进制（非 JSON）。
func TestTTSClientSynthesize(t *testing.T) {
	var auth, body, ctype string
	audio := []byte("fake-wav-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathTTS {
			t.Errorf("请求路径 = %q", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		ctype = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(audio)
	}))
	defer srv.Close()

	c := NewTTSClient("zp-test", srv.URL)
	got, err := c.Synthesize(context.Background(), TTSSynthesizeReq{
		Text: "你好", Voice: "tongtong", Speed: 1.2, Volume: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer zp-test" {
		t.Errorf("Authorization = %q", auth)
	}
	if ctype != "application/json" {
		t.Errorf("Content-Type = %q", ctype)
	}
	for _, want := range []string{
		`"model":"glm-tts"`, `"input":"你好"`, `"voice":"tongtong"`,
		`"response_format":"wav"`, `"stream":false`, `"speed":1.2`, `"volume":2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	if string(got) != string(audio) {
		t.Errorf("音频字节不符: %q", got)
	}
}

// speed/volume 为 0 时不随请求下发（走上游默认 1.0）。
func TestTTSClientDefaultsOmitted(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte("audio"))
	}))
	defer srv.Close()
	if _, err := NewTTSClient("zp", srv.URL).Synthesize(context.Background(), TTSSynthesizeReq{Text: "hi", Voice: "jam"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "speed") || strings.Contains(body, "volume") {
		t.Errorf("默认语速/音量不应下发:\n%s", body)
	}
}

// TTS 上游错误（JSON 错误体）转中文错误。
func TestTTSClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"1002","message":"Invalid Authorization"}}`))
	}))
	defer srv.Close()
	_, err := NewTTSClient("bad", srv.URL).Synthesize(context.Background(), TTSSynthesizeReq{Text: "hi", Voice: "jam"})
	if err == nil || !strings.Contains(err.Error(), "Invalid Authorization") {
		t.Fatalf("上游错误应透出 message, got %v", err)
	}
}

// ASR multipart 直传：file 字段 + model/stream 文本字段、热词数组展开为重复字段、
// 响应 text 提取。
func TestASRClientTranscribe(t *testing.T) {
	var auth, ctype, rawForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		ctype = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		rawForm = string(raw)
		_, _ = w.Write([]byte(`{"id":"t1","text":"转写结果","model":"glm-asr-2512"}`))
	}))
	defer srv.Close()

	src := filepath.Join(t.TempDir(), "clip.mp3")
	if err := os.WriteFile(src, []byte("fake-mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := NewASRClient("zp-test", srv.URL).Transcribe(context.Background(), src, "上一次的转写", []string{"热词A", "热词B"})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer zp-test" {
		t.Errorf("Authorization = %q", auth)
	}
	if !strings.HasPrefix(ctype, "multipart/form-data") {
		t.Errorf("Content-Type = %q", ctype)
	}
	for _, want := range []string{`name="model"`, `name="stream"`, `name="prompt"`, `name="file"; filename="clip.mp3"`} {
		if !strings.Contains(rawForm, want) {
			t.Errorf("表单缺少 %s", want)
		}
	}
	if strings.Count(rawForm, `name="hotwords"`) != 2 || !strings.Contains(rawForm, "热词A") {
		t.Errorf("热词应展开为重复字段:\n%s", rawForm)
	}
	if text != "转写结果" {
		t.Errorf("text = %q", text)
	}
}

// 音色列表：查询参数透传（voiceType/voiceName）、voice_list 解析。
func TestVoiceClientList(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.String()
		_, _ = w.Write([]byte(`{"voice_list":[{"voice":"tongtong","voice_name":"彤彤","voice_type":"OFFICIAL",
			"download_url":"https://cdn/tt.mp3","create_time":"2026-09-25 10:00:00"},
			{"voice":"voice_clone_001","voice_name":"我的复刻","voice_type":"PRIVATE"}]}`))
	}))
	defer srv.Close()
	voices, err := NewVoiceClient("zp", srv.URL).List(context.Background(), "OFFICIAL", "彤")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "voiceType=OFFICIAL") || !strings.Contains(path, "voiceName=") {
		t.Errorf("查询参数不符: %s", path)
	}
	if len(voices) != 2 || voices[1].VoiceType != "PRIVATE" {
		t.Errorf("voice_list 解析不符: %+v", voices)
	}
}

// 复刻 → 删除全链路：clone 请求体（model/voice_name/file_id/input）、delete 请求体。
func TestVoiceCloneAndDelete(t *testing.T) {
	var cloneBody, delBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case pathFiles:
			_, _ = w.Write([]byte(`{"id":"file-123","purpose":"voice-clone-input"}`))
		case pathVoiceClone:
			cloneBody = string(raw)
			_, _ = w.Write([]byte(`{"voice":"voice_clone_001","file_id":"file-out"}`))
		case pathVoiceDel:
			delBody = string(raw)
			_, _ = w.Write([]byte(`{"voice":"voice_clone_001","update_time":"2026-09-25"}`))
		}
	}))
	defer srv.Close()
	vc := NewVoiceClient("zp", srv.URL)

	sample := filepath.Join(t.TempDir(), "sample.wav")
	if err := os.WriteFile(sample, []byte("wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileID, err := vc.UploadVoiceSample(context.Background(), sample)
	if err != nil {
		t.Fatal(err)
	}
	if fileID != "file-123" {
		t.Errorf("file_id = %q", fileID)
	}
	voice, err := vc.VoiceClone(context.Background(), "my_voice", fileID, "试听文本", "示例文本")
	if err != nil {
		t.Fatal(err)
	}
	if voice != "voice_clone_001" {
		t.Errorf("voice = %q", voice)
	}
	for _, want := range []string{`"model":"glm-tts-clone"`, `"voice_name":"my_voice"`, `"file_id":"file-123"`, `"input":"试听文本"`, `"text":"示例文本"`} {
		if !strings.Contains(cloneBody, want) {
			t.Errorf("clone 请求体缺少 %s:\n%s", want, cloneBody)
		}
	}
	if err := vc.VoiceDelete(context.Background(), "voice_clone_001"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(delBody, `"voice":"voice_clone_001"`) {
		t.Errorf("delete 请求体不符:\n%s", delBody)
	}
}
