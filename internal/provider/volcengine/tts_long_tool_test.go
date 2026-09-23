package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

// speedUpTTSLongPoll 轮询节奏切到测试节奏，测试结束后恢复。
func speedUpTTSLongPoll(t *testing.T) {
	t.Helper()
	oldInterval, oldMax, oldTimeout := ttsLongPollInterval, ttsLongPollMax, ttsLongPollTimeout
	ttsLongPollInterval, ttsLongPollMax, ttsLongPollTimeout = time.Millisecond, 2*time.Millisecond, 5*time.Second
	t.Cleanup(func() {
		ttsLongPollInterval, ttsLongPollMax, ttsLongPollTimeout = oldInterval, oldMax, oldTimeout
	})
}

func TestValidateLongText(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantErr string
	}{
		{name: "正常文本含制表换行", text: "你好\n世界\t测试"},
		{name: "空串", text: "", wantErr: ""},
		{name: "超10万字符", text: strings.Repeat("字", ttsLongMaxLength+1),
			wantErr: "超出长度限制"},
		{name: "非法控制字符占比超限", text: "\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0b你好",
			wantErr: "非法字符"},
		{name: "回车算非法字符", text: strings.Repeat("\r", 2) + strings.Repeat("字", 8),
			wantErr: "非法字符"},
		{name: "低占比非法字符放行", text: "\r" + strings.Repeat("字", 99)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateLongText(c.text)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want 含 %q", err, c.wantErr)
			}
		})
	}
}

func TestTTSLongToolRunParamErrors(t *testing.T) {
	tool := NewTTSLongTool(SpeechCred{APIKey: "k"}, t.TempDir())
	cases := []struct {
		name    string
		params  map[string]any
		wantErr string
	}{
		{"缺文本", map[string]any{}, "缺少必填参数"},
		{"空文本", map[string]any{"text": ""}, "缺少必填参数"},
		{"超限", map[string]any{"text": strings.Repeat("字", ttsLongMaxLength+1)}, "超出长度限制"},
		{"非法字符", map[string]any{"text": "\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0b你好"}, "非法字符"},
		{"格式不支持", map[string]any{"text": "你好", "format": "wav"}, "仅支持"},
		{"采样率非法", map[string]any{"text": "你好", "sample_rate": "12345"}, "不支持的采样率"},
		{"pcm指定比特率", map[string]any{"text": "你好", "format": "pcm", "bit_rate": "160000"}, "不支持指定比特率"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tool.Run(context.Background(), provider.TaskInput{Params: c.params}, func(int, string, map[string]any) {})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want 含 %q", err, c.wantErr)
			}
		})
	}
}

func TestTTSLongToolRunNoCred(t *testing.T) {
	tool := NewTTSLongTool(SpeechCred{}, t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "你好"}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "凭证") {
		t.Fatalf("err = %v, want 凭证错误", err)
	}
}

// TestTTSLongToolRunSuccess 全流程：submit → 两次 Running → Success(带分句) → 下载落盘。
func TestTTSLongToolRunSuccess(t *testing.T) {
	speedUpTTSLongPoll(t)

	var submitBody map[string]any
	var audioURL string
	queryCalls := 0
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			submitBody = body
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "up-task-1"}})
		},
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			queryCalls++
			if queryCalls < 3 {
				writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "up-task-1", "task_status": 1}})
				return
			}
			writeJSON(w, map[string]any{
				"code": 20000000,
				"data": map[string]any{
					"task_id": "up-task-1", "task_status": 2,
					"audio_url":       audioURL,
					"req_text_length": 8, "synthesize_text_length": 8,
					"sentences": []map[string]any{
						{"text": "第一句。", "startTime": 0.0, "endTime": 1.0},
						{"text": "第二句。", "startTime": 1.2, "endTime": 2.34},
					},
				},
			})
		})

	outDir := t.TempDir()
	audioURL = m.srv.URL + "/audio/abc.mp3"
	tool := NewTTSLongToolWithBaseURL(SpeechCred{APIKey: "key-1"}, outDir, m.srv.URL)

	var notes []string
	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "一二三四五六七八", "voice": "zh_female_vv_uranus_bigtts",
		"format": "mp3", "sample_rate": "44100", "speech_rate": 20, "timestamps": true,
	}}, func(p int, note string, _ map[string]any) { notes = append(notes, note) })
	if err != nil {
		t.Fatal(err)
	}

	// 提交体：默认值与归一参数
	params := submitBody["req_params"].(map[string]any)
	if params["speaker"] != "zh_female_vv_uranus_bigtts" {
		t.Errorf("speaker = %v", params["speaker"])
	}
	audio := params["audio_params"].(map[string]any)
	if audio["format"] != "mp3" || audio["sample_rate"] != float64(44100) || audio["speech_rate"] != float64(20) {
		t.Errorf("audio_params = %v", audio)
	}

	// 产物：音频 + SRT
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2（audio+subtitle）", len(out.Artifacts))
	}
	a, srt := out.Artifacts[0], out.Artifacts[1]
	if a.Kind != "audio" || a.Format != "mp3" || a.Size == 0 || a.DurationMS != 2340 {
		t.Errorf("audio artifact = %+v", a)
	}
	if !strings.HasPrefix(a.Path, "tts_long"+string(filepath.Separator)) {
		t.Errorf("audio path = %q", a.Path)
	}
	if srt.Kind != "subtitle" || srt.Format != "srt" || !strings.HasSuffix(srt.Path, ".srt") {
		t.Errorf("srt artifact = %+v", srt)
	}

	// 音频与 SRT 内容落盘
	audioBytes, err := os.ReadFile(filepath.Join(outDir, a.Path))
	if err != nil || string(audioBytes) != "FAKE_MP3_BYTES" {
		t.Fatalf("音频落盘失败: %v %q", err, audioBytes)
	}
	srtBytes, err := os.ReadFile(filepath.Join(outDir, srt.Path))
	if err != nil {
		t.Fatal(err)
	}
	srtText := string(srtBytes)
	if !strings.Contains(srtText, "00:00:00,000 --> 00:00:01,000") ||
		!strings.Contains(srtText, "00:00:01,200 --> 00:00:02,340") ||
		!strings.Contains(srtText, "第一句。") {
		t.Errorf("SRT 内容不符: %s", srtText)
	}

	// Summary 契约
	raw, _ := json.Marshal(out.Summary)
	summary := string(raw)
	for _, want := range []string{`"char_count":8`, `"sentence_count":2`, `"upstream_task_id":"up-task-1"`, `"synthesized_chars":8`} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary 缺少 %s: %s", want, summary)
		}
	}
	// 进度链路完整：提交 → 等待 → 合成中 → 下载
	if !strings.Contains(notes[len(notes)-1], "下载") || !strings.Contains(notes[1], "任务已提交") {
		t.Errorf("notes = %v", notes)
	}
}

// TestTTSLongToolRunOGGOpus ogg_opus 采样率强制 48000，且不开时间戳时无 SRT 产物。
func TestTTSLongToolRunOGGOpus(t *testing.T) {
	speedUpTTSLongPoll(t)
	var submitBody map[string]any
	var audioURL string
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			submitBody = body
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t"}})
		},
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t", "task_status": 2,
				"audio_url": audioURL}})
		})
	audioURL = m.srv.URL + "/audio/a.ogg"
	tool := NewTTSLongToolWithBaseURL(SpeechCred{APIKey: "k"}, t.TempDir(), m.srv.URL)
	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "你好", "format": "ogg_opus", "sample_rate": "24000",
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	audio := submitBody["req_params"].(map[string]any)["audio_params"].(map[string]any)
	if audio["sample_rate"] != float64(48000) {
		t.Errorf("ogg_opus 采样率应强制 48000: %v", audio)
	}
	if audio["bit_rate"] != nil {
		t.Errorf("bit_rate 空值不应下发: %v", audio)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "audio" {
		t.Errorf("未开时间戳应只有音频产物: %+v", out.Artifacts)
	}
}

// TestTTSLongToolRunUpstreamFailure 上游 Failure 透传 message。
func TestTTSLongToolRunUpstreamFailure(t *testing.T) {
	speedUpTTSLongPoll(t)
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t"}})
		},
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t", "task_status": 3, "message": "bad text"}})
		})
	tool := NewTTSLongToolWithBaseURL(SpeechCred{APIKey: "k"}, t.TempDir(), m.srv.URL)
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "你好"}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "bad text") {
		t.Fatalf("err = %v, want 上游失败透传", err)
	}
}

// TestTTSLongToolRunOutRedirect _out 重定向：音频与 SRT 跟随重定向路径。
func TestTTSLongToolRunOutRedirect(t *testing.T) {
	speedUpTTSLongPoll(t)
	var audioURL string
	m := newTTSLongMockServer(t,
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t"}})
		},
		func(w http.ResponseWriter, r *http.Request, body map[string]any, n int) {
			writeJSON(w, map[string]any{"code": 20000000, "data": map[string]any{"task_id": "t", "task_status": 2,
				"audio_url": audioURL,
				"sentences": []map[string]any{{"text": "你好。", "startTime": 0, "endTime": 0.5}}}})
		})
	audioURL = m.srv.URL + "/audio/a.mp3"
	tool := NewTTSLongToolWithBaseURL(SpeechCred{APIKey: "k"}, t.TempDir(), m.srv.URL)
	outPath := filepath.Join(t.TempDir(), "book.mp3")
	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "你好", "timestamps": true, "_out": outPath,
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if out.Artifacts[0].Path != outPath {
		t.Errorf("audio path = %q, want %q", out.Artifacts[0].Path, outPath)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatal(err)
	}
	srtPath := strings.TrimSuffix(outPath, ".mp3") + ".srt"
	if out.Artifacts[1].Path != srtPath {
		t.Errorf("srt path = %q, want %q", out.Artifacts[1].Path, srtPath)
	}
	if _, err := os.Stat(srtPath); err != nil {
		t.Fatal(err)
	}
}
