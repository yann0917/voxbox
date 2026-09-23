package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/provider"
)

// ---- PodcastTool 测试：复用 podcast_client_test.go 的 mock WS helper ----

const podToolSID = "sess-tool"

// podReport 记录一次进度上报，供断言 onRound → report 透传。
type podReport struct {
	progress int
	note     string
	detail   map[string]any
}

// newPodToolWithMock 用指向 mock WS 的 client 构造 PodcastTool（outDir 为临时目录）。
func newPodToolWithMock(t *testing.T, m *podMockServer) *PodcastTool {
	t.Helper()
	cred := SpeechCred{AppID: "app", AccessToken: "tok"}
	return &PodcastTool{client: NewPodcastClientWithURL(cred, m.wsURL()), cred: cred, outDir: t.TempDir()}
}

// podToolSuccessServer 标准 2 轮成功事件流：150 → [360 → 361×2 → 362] → [360 → 361 → 362]
// → 154 → 363 → 152 → 收 FinishConnection → 52。checkPayload 非 nil 时先断言 StartSession payload。
func podToolSuccessServer(t *testing.T, checkPayload func(t *testing.T, s *podMockSession)) *podMockServer {
	t.Helper()
	return newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		if checkPayload != nil {
			checkPayload(t, s)
		}
		podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, podToolSID, []byte("{}")))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, podToolSID,
			podMustJSON(t, map[string]any{"text_type": "dialog", "speaker": "spk_a", "round_id": 1, "text": "你好，欢迎收听。"})))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, podToolSID, []byte{0xFF, 0xFB, 0x90, 0x00}))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, podToolSID, []byte{0x11, 0x22}))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, podToolSID, []byte(`{"audio_duration":8.4,"end_time":38.2,"start_time":25.25}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, podToolSID,
			podMustJSON(t, map[string]any{"text_type": "dialog", "speaker": "spk_b", "round_id": 2, "text": "今天我们聊聊播客。"})))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, podToolSID, []byte{0x33, 0x44, 0x55}))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, podToolSID, []byte(`{"audio_duration":5.5,"end_time":50,"start_time":38.2}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventUsageResponse, podToolSID, []byte(`{"usage":{"input_text_tokens":120,"output_audio_tokens":2400}}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventPodcastEnd, podToolSID, []byte(`{"meta_info":{"audio_url":"https://example.com/pod.mp3"}}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventSessionFinished, podToolSID, []byte("{}")))
		if ev := podReadMockEvent(conn); ev != EventFinishConnection {
			t.Errorf("152 后收到 event = %d, want %d", ev, EventFinishConnection)
		}
		podWriteMockFrame(conn, podServerTextFrame(EventConnectionFinished, podToolSID, []byte("{}")))
	})
}

// readPodArtifact 按产物 Path 读取落盘文件（相对路径基于 tool.outDir，绝对路径直用）。
func readPodArtifact(t *testing.T, tool *PodcastTool, path string) []byte {
	t.Helper()
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(tool.outDir, p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取产物 %s 失败: %v", p, err)
	}
	return b
}

func TestPodcastToolTextMode(t *testing.T) {
	t.Run("绝对_out", func(t *testing.T) {
		m := podToolSuccessServer(t, func(t *testing.T, s *podMockSession) {
			if s.payload["action"] != float64(0) || s.payload["input_text"] != "聊聊今天的天气。" {
				t.Errorf("payload = %v", s.payload)
			}
		})
		tool := newPodToolWithMock(t, m)
		outAbs := filepath.Join(t.TempDir(), "episode.mp3")

		var reports []podReport
		out, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{
				"input_text": "聊聊今天的天气。",
				"speakers":   "spk_a, spk_b", // 含空格，验证 TrimSpace
				"format":     "mp3",
				"_out":       outAbs,
			},
		}, func(progress int, note string, detail map[string]any) {
			reports = append(reports, podReport{progress, note, detail})
		})
		if err != nil {
			t.Fatalf("Run() err = %v", err)
		}

		// 2 个产物：audio + dialog；_out 为绝对路径时两者均为绝对路径，dialog 仅换扩展名。
		if len(out.Artifacts) != 2 {
			t.Fatalf("artifacts = %+v, want 2", out.Artifacts)
		}
		audioArt, dialogArt := out.Artifacts[0], out.Artifacts[1]
		if audioArt.Kind != "audio" || audioArt.Format != "mp3" || audioArt.Path != outAbs {
			t.Errorf("audio artifact = %+v, want path %s", audioArt, outAbs)
		}
		wantDialog := strings.TrimSuffix(outAbs, ".mp3") + ".json"
		if dialogArt.Kind != "dialog" || dialogArt.Format != "json" || dialogArt.Path != wantDialog {
			t.Errorf("dialog artifact = %+v, want path %s", dialogArt, wantDialog)
		}
		// 音频内容 = 361 分片按序拼接。
		wantAudio := []byte{0xFF, 0xFB, 0x90, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		if audioArt.Size != int64(len(wantAudio)) {
			t.Errorf("audio size = %d, want %d", audioArt.Size, len(wantAudio))
		}
		if got := readPodArtifact(t, tool, audioArt.Path); string(got) != string(wantAudio) {
			t.Errorf("audio = % x, want % x", got, wantAudio)
		}

		// 对话稿 JSON：来自 360/362 事件的轮次与时长。
		var dialog struct {
			Rounds []struct {
				RoundID   int     `json:"round_id"`
				Speaker   string  `json:"speaker"`
				Text      string  `json:"text"`
				DurationS float64 `json:"duration_s"`
			} `json:"rounds"`
		}
		if err := json.Unmarshal(readPodArtifact(t, tool, dialogArt.Path), &dialog); err != nil {
			t.Fatalf("dialog JSON 解析失败: %v", err)
		}
		if len(dialog.Rounds) != 2 {
			t.Fatalf("dialog rounds = %+v, want 2", dialog.Rounds)
		}
		if dialog.Rounds[0].RoundID != 1 || dialog.Rounds[0].Speaker != "spk_a" ||
			dialog.Rounds[0].Text != "你好，欢迎收听。" || dialog.Rounds[0].DurationS != 8.4 {
			t.Errorf("dialog rounds[0] = %+v", dialog.Rounds[0])
		}
		if dialog.Rounds[1].RoundID != 2 || dialog.Rounds[1].Speaker != "spk_b" ||
			dialog.Rounds[1].Text != "今天我们聊聊播客。" || dialog.Rounds[1].DurationS != 5.5 {
			t.Errorf("dialog rounds[1] = %+v", dialog.Rounds[1])
		}

		// Summary：轮次/时长/音色/兜底标记/用量。
		if got := out.Summary["rounds"]; got != 2 {
			t.Errorf("summary.rounds = %v, want 2", got)
		}
		if got := out.Summary["duration_s"]; got != 13.9 {
			t.Errorf("summary.duration_s = %v, want 13.9", got)
		}
		if got, ok := out.Summary["speakers"].([]string); !ok || len(got) != 2 || got[0] != "spk_a" || got[1] != "spk_b" {
			t.Errorf("summary.speakers = %v", out.Summary["speakers"])
		}
		if got := out.Summary["audio_url_fallback"]; got != false {
			t.Errorf("summary.audio_url_fallback = %v, want false", got)
		}
		usage, _ := out.Summary["usage"].(map[string]int64)
		if usage == nil || usage["input_text_tokens"] != 120 || usage["output_audio_tokens"] != 2400 {
			t.Errorf("summary.usage = %v", out.Summary["usage"])
		}

		// onRound → report：逐轮 detail（round_id/speaker/text/rounds_done），进度 min(95, 轮数*15)。
		var roundIDs, roundDones []any
		for _, r := range reports {
			if id, ok := r.detail["round_id"]; ok {
				roundIDs = append(roundIDs, id)
				roundDones = append(roundDones, r.detail["rounds_done"])
				if r.detail["speaker"] == "" || r.detail["text"] == "" {
					t.Errorf("round report detail 缺 speaker/text: %v", r.detail)
				}
				if r.progress != min(95, r.detail["rounds_done"].(int)*15) {
					t.Errorf("round report progress = %d, detail = %v", r.progress, r.detail)
				}
				if !strings.Contains(r.note, "轮对话") {
					t.Errorf("round report note = %q", r.note)
				}
			}
		}
		if len(roundIDs) != 2 || roundIDs[0] != 1 || roundIDs[1] != 2 {
			t.Errorf("round_id 顺序 = %v", roundIDs)
		}
		if len(roundDones) != 2 || roundDones[0] != 1 || roundDones[1] != 2 {
			t.Errorf("rounds_done 顺序 = %v", roundDones)
		}
	})

	t.Run("相对_out", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		out, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{
				"input_text": "聊聊今天的天气。",
				"speakers":   "spk_a,spk_b",
				"_out":       "podcast_custom/rel.mp3",
			},
		}, nopReport)
		if err != nil {
			t.Fatalf("Run() err = %v", err)
		}
		if len(out.Artifacts) != 2 {
			t.Fatalf("artifacts = %+v", out.Artifacts)
		}
		if out.Artifacts[0].Path != filepath.Join("podcast_custom", "rel.mp3") {
			t.Errorf("audio path = %s", out.Artifacts[0].Path)
		}
		if out.Artifacts[1].Path != filepath.Join("podcast_custom", "rel.json") {
			t.Errorf("dialog path = %s", out.Artifacts[1].Path)
		}
		readPodArtifact(t, tool, out.Artifacts[0].Path)
		readPodArtifact(t, tool, out.Artifacts[1].Path)
	})
}

func TestPodcastToolScriptMode(t *testing.T) {
	t.Run("合法对话稿", func(t *testing.T) {
		script := `{"rounds":[{"speaker":"zh_a","text":"你好呀"},{"speaker":"zh_b","text":"在的，今天聊什么？"}]}`
		m := podToolSuccessServer(t, func(t *testing.T, s *podMockSession) {
			if s.payload["action"] != float64(3) {
				t.Errorf("action = %v, want 3（对话稿模式）", s.payload["action"])
			}
			if s.payload["input_text"] != nil || s.payload["input_info"] != nil {
				t.Errorf("script 模式不应携带 input_text/input_info: %v", s.payload)
			}
			texts, _ := s.payload["nlp_texts"].([]any)
			if len(texts) != 2 {
				t.Fatalf("nlp_texts = %v", texts)
			}
			t0, _ := texts[0].(map[string]any)
			t1, _ := texts[1].(map[string]any)
			if t0["speaker"] != "zh_a" || t0["text"] != "你好呀" || t1["speaker"] != "zh_b" || t1["text"] != "在的，今天聊什么？" {
				t.Errorf("nlp_texts = %v", texts)
			}
			si, _ := s.payload["speaker_info"].(map[string]any)
			spk, _ := si["speakers"].([]any)
			if len(spk) != 2 || spk[0] != "zh_a" || spk[1] != "zh_b" {
				t.Errorf("speaker_info = %v", si)
			}
		})
		tool := newPodToolWithMock(t, m)
		out, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"script": script, "speakers": "zh_a,zh_b"},
		}, nopReport)
		if err != nil {
			t.Fatalf("Run() err = %v", err)
		}
		if len(out.Artifacts) != 2 || out.Artifacts[0].Kind != "audio" || out.Artifacts[1].Kind != "dialog" {
			t.Fatalf("artifacts = %+v", out.Artifacts)
		}
	})

	t.Run("非法JSON", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"script": `{"rounds":[{bad}`, "speakers": "a,b"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "对话稿格式错误") {
			t.Fatalf("err = %v, want 对话稿格式错误", err)
		}
		if m.count() != 0 {
			t.Errorf("对话稿非法不应建立 WS 连接, count = %d", m.count())
		}
	})

	t.Run("rounds为空", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"script": `{"rounds":[]}`, "speakers": "a,b"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "对话稿格式错误") {
			t.Fatalf("err = %v, want 对话稿格式错误", err)
		}
	})

	t.Run("轮次缺text", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"script": `{"rounds":[{"speaker":"a"},{"speaker":"b","text":"x"}]}`, "speakers": "a,b"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "对话稿格式错误") {
			t.Fatalf("err = %v, want 对话稿格式错误", err)
		}
	})
}

func TestPodcastToolSpeakersValidation(t *testing.T) {
	cases := map[string]string{
		"1个":  "only_a",
		"3个":  "a,b,c",
		"空串":  "",
		"仅逗号": ", ,",
	}
	for name, speakers := range cases {
		t.Run(name, func(t *testing.T) {
			m := podToolSuccessServer(t, nil)
			tool := newPodToolWithMock(t, m)
			_, err := tool.Run(context.Background(), provider.TaskInput{
				Params: map[string]any{"input_text": "x", "speakers": speakers},
			}, nopReport)
			if err == nil || !strings.Contains(err.Error(), "恰好 2 个") {
				t.Fatalf("err = %v, want 恰好 2 个", err)
			}
			if m.count() != 0 {
				t.Errorf("speakers 非法不应建立 WS 连接, count = %d", m.count())
			}
		})
	}
}

func TestPodcastToolInputValidation(t *testing.T) {
	t.Run("皆无", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"speakers": "a,b"}}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "缺少播客内容输入") {
			t.Fatalf("err = %v, want 缺少播客内容输入", err)
		}
		if m.count() != 0 {
			t.Errorf("无输入不应建立 WS 连接, count = %d", m.count())
		}
	})

	t.Run("text+url", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"input_text": "x", "url": "https://example.com", "speakers": "a,b"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "只能提供其一") {
			t.Fatalf("err = %v, want 只能提供其一", err)
		}
	})

	t.Run("text+script", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"input_text": "x", "script": `{"rounds":[{"speaker":"a","text":"y"}]}`, "speakers": "a,b"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "只能提供其一") {
			t.Fatalf("err = %v, want 只能提供其一", err)
		}
	})

	t.Run("format不支持", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		tool := newPodToolWithMock(t, m)
		_, err := tool.Run(context.Background(), provider.TaskInput{
			Params: map[string]any{"input_text": "x", "speakers": "a,b", "format": "wav"},
		}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "暂不支持") {
			t.Fatalf("err = %v, want 暂不支持", err)
		}
		if m.count() != 0 {
			t.Errorf("format 非法不应建立 WS 连接, count = %d", m.count())
		}
	})

	t.Run("参数校验先于凭证校验", func(t *testing.T) {
		m := podToolSuccessServer(t, nil)
		cred := SpeechCred{}
		tool := &PodcastTool{client: NewPodcastClientWithURL(cred, m.wsURL()), cred: cred, outDir: t.TempDir()}
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}}, nopReport)
		if err == nil || !strings.Contains(err.Error(), "缺少播客内容输入") {
			t.Fatalf("err = %v, want 输入错误先于凭证错误", err)
		}
	})
}

// TestPodcastToolAPIKeyOnlyRejected 播客协议只认 APP ID + Access Token：
// 仅配新版 API Key 时必须在凭证校验拦下（不得等 WS 握手失败），且不建立连接。
func TestPodcastToolAPIKeyOnlyRejected(t *testing.T) {
	m := podToolSuccessServer(t, nil)
	cred := SpeechCred{APIKey: "ak-only"}
	tool := &PodcastTool{client: NewPodcastClientWithURL(cred, m.wsURL()), cred: cred, outDir: t.TempDir()}
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"input_text": "你好",
			"speakers":   "zh_female_cancan_mars_bigtts,zh_male_dayixiansheng_v2_saturn_bigtts",
		},
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "播客需要 APP ID 与 Access Token") {
		t.Fatalf("err = %v, want 提示播客需要 APP ID 与 Access Token", err)
	}
	if m.count() != 0 {
		t.Errorf("凭证不足不应建立 WS 连接, count = %d", m.count())
	}
}

// TestPodcastToolAudioFallback 分片为空且 363 携带 audio_url：兜底下载转存，Summary 标记 fallback。
func TestPodcastToolAudioFallback(t *testing.T) {
	const fallbackAudio = "FALLBACK-MP3-CONTENT"
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fallbackAudio))
	}))
	t.Cleanup(httpSrv.Close)

	m := newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, podToolSID, []byte("{}")))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, podToolSID,
			podMustJSON(t, map[string]any{"speaker": "spk_a", "round_id": 1, "text": "只有文本没有分片"})))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, podToolSID, []byte(`{"audio_duration":3.0,"end_time":3,"start_time":0}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventPodcastEnd, podToolSID,
			[]byte(`{"meta_info":{"audio_url":"`+httpSrv.URL+`/pod.mp3"}}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventSessionFinished, podToolSID, []byte("{}")))
		if ev := podReadMockEvent(conn); ev != EventFinishConnection {
			t.Errorf("152 后收到 event = %d, want %d", ev, EventFinishConnection)
		}
		podWriteMockFrame(conn, podServerTextFrame(EventConnectionFinished, podToolSID, []byte("{}")))
	})
	tool := newPodToolWithMock(t, m)
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"input_text": "x", "speakers": "a,b"},
	}, nopReport)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
	if got := readPodArtifact(t, tool, out.Artifacts[0].Path); string(got) != fallbackAudio {
		t.Errorf("audio = %q, want %q（audio_url 兜底转存）", got, fallbackAudio)
	}
	if out.Summary["audio_url_fallback"] != true {
		t.Errorf("summary.audio_url_fallback = %v, want true", out.Summary["audio_url_fallback"])
	}
}

// TestPodcastToolNoAudio 分片与 audio_url 皆为空：报「未收到音频内容」。
func TestPodcastToolNoAudio(t *testing.T) {
	m := newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, podToolSID, []byte("{}")))
		podWriteMockFrame(conn, podServerTextFrame(EventSessionFinished, podToolSID, []byte("{}")))
		if ev := podReadMockEvent(conn); ev != EventFinishConnection {
			t.Errorf("152 后收到 event = %d, want %d", ev, EventFinishConnection)
		}
		podWriteMockFrame(conn, podServerTextFrame(EventConnectionFinished, podToolSID, []byte("{}")))
	})
	tool := newPodToolWithMock(t, m)
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"input_text": "x", "speakers": "a,b"},
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "未收到音频内容") {
		t.Fatalf("err = %v, want 未收到音频内容", err)
	}
}
