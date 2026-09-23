package volcengine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---- 播客 mock WS 服务（对照 podframe.go 帧格式手组下行帧）----

func podBeUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// podServerTextFrame 下行文本帧：0x11 0x94 0x10 0x00 + event + session_id + JSON payload。
func podServerTextFrame(event int, sid string, payload []byte) []byte {
	b := append([]byte{0x11, 0x94, 0x10, 0x00}, podBeUint32(uint32(event))...)
	b = append(b, podBeUint32(uint32(len(sid)))...)
	b = append(b, sid...)
	b = append(b, podBeUint32(uint32(len(payload)))...)
	return append(b, payload...)
}

// podServerAudioFrame 下行音频帧：0x11 0xB4 0x00 0x00 + event + session_id + 原始音频。
func podServerAudioFrame(event int, sid string, audio []byte) []byte {
	b := append([]byte{0x11, 0xB4, 0x00, 0x00}, podBeUint32(uint32(event))...)
	b = append(b, podBeUint32(uint32(len(sid)))...)
	b = append(b, sid...)
	b = append(b, podBeUint32(uint32(len(audio)))...)
	return append(b, audio...)
}

// podServerErrorFrame 错误帧：0x11 0xF0 0x10 0x00 + code + 消息 JSON。
func podServerErrorFrame(code uint32, msg string) []byte {
	m, _ := json.Marshal(map[string]string{"message": msg})
	b := append([]byte{0x11, 0xF0, 0x10, 0x00}, podBeUint32(code)...)
	b = append(b, podBeUint32(uint32(len(m)))...)
	return append(b, m...)
}

func podMustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("构造 JSON 失败: %v", err)
	}
	return b
}

// podMockSession 记录一次连接中 mock 收到的 StartSession 信息与请求头，供断言。
type podMockSession struct {
	requestID   string // X-Api-Request-Id
	appID       string
	accessKey   string
	appKey      string
	connectID   string
	resourceID  string
	sessionID   string
	payload     map[string]any
	finishMu    sync.Mutex
	gotFinishEv bool // 收到客户端 FinishConnection(event 2)
}

func (s *podMockSession) setFinishEv() { s.finishMu.Lock(); s.gotFinishEv = true; s.finishMu.Unlock() }
func (s *podMockSession) finishEv() bool {
	s.finishMu.Lock()
	defer s.finishMu.Unlock()
	return s.gotFinishEv
}

// podMockServer 播客 WS mock：读出每条连接的 StartSession 后调用 handler（handler 返回即断开连接）。
type podMockServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	sessions []*podMockSession
}

func newPodMockServer(t *testing.T, handler func(t *testing.T, conn *websocket.Conn, s *podMockSession)) *podMockServer {
	t.Helper()
	m := &podMockServer{}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		s := &podMockSession{
			requestID:  r.Header.Get("X-Api-Request-Id"),
			appID:      r.Header.Get("X-Api-App-Id"),
			accessKey:  r.Header.Get("X-Api-Access-Key"),
			appKey:     r.Header.Get("X-Api-App-Key"),
			connectID:  r.Header.Get("X-Api-Connect-Id"),
			resourceID: r.Header.Get("X-Api-Resource-Id"),
		}
		// 读 StartSession 帧并反向解析（event 应为 100，session_id 非空）。
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		f, err := ParsePodFrame(msg)
		if err != nil || f.Event != EventStartSession {
			t.Errorf("StartSession 帧非法: f=%+v err=%v", f, err)
			return
		}
		if f.SessionID == "" {
			t.Error("StartSession session_id 为空")
			return
		}
		s.sessionID = f.SessionID
		_ = json.Unmarshal(f.Payload, &s.payload)
		m.mu.Lock()
		m.sessions = append(m.sessions, s)
		m.mu.Unlock()
		handler(t, conn, s)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *podMockServer) wsURL() string { return "ws" + strings.TrimPrefix(m.srv.URL, "http") }

func (m *podMockServer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *podMockServer) session(i int) *podMockSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[i]
}

// podWriteMockFrame 供 handler 向客户端发帧。
func podWriteMockFrame(conn *websocket.Conn, frame []byte) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteMessage(websocket.BinaryMessage, frame)
}

// podReadMockEvent 读一帧返回 event（-1 表示读失败/解析失败）。
func podReadMockEvent(conn *websocket.Conn) int {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return -1
	}
	f, err := ParsePodFrame(msg)
	if err != nil {
		return -1
	}
	return f.Event
}

func TestPodcastGenerateSuccess(t *testing.T) {
	const sid = "sess-1"
	audioA1 := []byte{0xFF, 0xFB, 0x90, 0x00}
	audioA2 := []byte{0x11, 0x22}
	audioB1 := []byte{0x33, 0x44, 0x55}
	roundA := map[string]any{"text_type": "dialog", "speaker": "spk_a", "round_id": 1, "text": "你好，欢迎收听。"}
	roundB := map[string]any{"text_type": "dialog", "speaker": "spk_b", "round_id": 2, "text": "今天我们聊聊播客。"}

	m := newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		// 校验上行 StartSession：payload 关键字段与鉴权头。
		if s.payload["action"] != float64(0) || s.payload["input_text"] != "聊聊今天的天气。" {
			t.Errorf("payload = %v", s.payload)
		}
		if s.payload["use_head_music"] != false {
			t.Errorf("use_head_music = %v", s.payload["use_head_music"])
		}
		ac, _ := s.payload["audio_config"].(map[string]any)
		if ac == nil || ac["format"] != "mp3" || ac["sample_rate"] != float64(24000) || ac["speech_rate"] != float64(0) {
			t.Errorf("audio_config = %v", ac)
		}
		si, _ := s.payload["speaker_info"].(map[string]any)
		spk, _ := si["speakers"].([]any)
		if len(spk) != 2 || spk[0] != "spk_a" || spk[1] != "spk_b" || si["random_order"] != false {
			t.Errorf("speaker_info = %v", si)
		}
		if s.appID != "app" || s.accessKey != "tok" || s.appKey != podAppKey {
			t.Errorf("鉴权头 = %s/%s/%s", s.appID, s.accessKey, s.appKey)
		}
		if s.resourceID != podResourceID {
			t.Errorf("X-Api-Resource-Id = %q, want %q", s.resourceID, podResourceID)
		}
		if s.requestID == "" || s.connectID == "" {
			t.Errorf("X-Api-Request-Id/Connect-Id 缺失: %q/%q", s.requestID, s.connectID)
		}
		// 官方语义：首连 session_id 即任务 task_id（=X-Api-Request-Id），retry_task_id 以此检索任务。
		if s.sessionID != s.requestID {
			t.Errorf("StartSession session_id = %q, want 与 X-Api-Request-Id 同值", s.sessionID)
		}
		if s.payload["retry_info"] != nil {
			t.Errorf("首连不应携带 retry_info: %v", s.payload["retry_info"])
		}

		// 150 → [360 → 361×2 → 362] → [360 → 361 → 362] → 154 → 363 → 152 → 收 FinishConnection(2) → 52。
		podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, sid, []byte("{}")))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, podMustJSON(t, roundA)))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, audioA1))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, audioA2))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, sid, []byte(`{"audio_duration":8.4,"end_time":38.2,"start_time":25.25}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, podMustJSON(t, roundB)))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, audioB1))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, sid, []byte(`{"audio_duration":5.5,"end_time":50,"start_time":38.2}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventUsageResponse, sid, []byte(`{"usage":{"input_text_tokens":120,"output_audio_tokens":2400}}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventPodcastEnd, sid, []byte(`{"meta_info":{"audio_url":"https://example.com/pod.mp3"}}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventSessionFinished, sid, []byte("{}")))
		if ev := podReadMockEvent(conn); ev != EventFinishConnection {
			t.Errorf("152 后收到 event = %d, want FinishConnection(%d)", ev, EventFinishConnection)
		} else {
			s.setFinishEv()
		}
		podWriteMockFrame(conn, podServerTextFrame(EventConnectionFinished, sid, []byte("{}")))
	})

	var onRoundTexts []string
	c := NewPodcastClientWithURL(SpeechCred{AppID: "app", AccessToken: "tok"}, m.wsURL())
	res, err := c.Generate(context.Background(), PodcastRequest{
		InputText: "聊聊今天的天气。",
		Speakers:  [2]string{"spk_a", "spk_b"},
		Format:    "mp3",
	}, func(r PodcastRound) { onRoundTexts = append(onRoundTexts, r.Text) })
	if err != nil {
		t.Fatalf("Generate() err = %v", err)
	}
	if res.Format != "mp3" || res.AudioURL != "https://example.com/pod.mp3" {
		t.Errorf("format/audio_url = %s/%s", res.Format, res.AudioURL)
	}
	wantAudio := append(append(append([]byte{}, audioA1...), audioA2...), audioB1...)
	if !bytes.Equal(res.Audio, wantAudio) {
		t.Errorf("audio = % x, want % x", res.Audio, wantAudio)
	}
	if len(res.Rounds) != 2 {
		t.Fatalf("rounds = %+v, want 2 轮", res.Rounds)
	}
	if res.Rounds[0].RoundID != 1 || res.Rounds[0].Speaker != "spk_a" ||
		res.Rounds[0].Text != "你好，欢迎收听。" || res.Rounds[0].DurationS != 8.4 {
		t.Errorf("rounds[0] = %+v", res.Rounds[0])
	}
	if res.Rounds[1].RoundID != 2 || res.Rounds[1].Speaker != "spk_b" ||
		res.Rounds[1].Text != "今天我们聊聊播客。" || res.Rounds[1].DurationS != 5.5 {
		t.Errorf("rounds[1] = %+v", res.Rounds[1])
	}
	if res.Usage["input_text_tokens"] != 120 || res.Usage["output_audio_tokens"] != 2400 {
		t.Errorf("usage = %v", res.Usage)
	}
	if len(onRoundTexts) != 2 || onRoundTexts[0] != "你好，欢迎收听。" || onRoundTexts[1] != "今天我们聊聊播客。" {
		t.Errorf("onRound 顺序 = %v", onRoundTexts)
	}
	if !m.session(0).finishEv() {
		t.Error("152 后客户端未发送 FinishConnection")
	}
}

func TestPodcastReconnect(t *testing.T) {
	const sid = "sess-r"
	audioA := []byte{0xAA, 0xBB}
	audioB := []byte{0xCC}

	var m *podMockServer
	m = newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		// 每次连接 session_id 均与 X-Api-Request-Id 同值：retry_task_id 与首连 session_id 恒等。
		if s.sessionID != s.requestID {
			t.Errorf("session_id = %q, want 与 X-Api-Request-Id 同值", s.sessionID)
		}
		if m.count() == 1 {
			// 第一次连接：完成轮次 1 后直接断开（不回 152），触发客户端续传。
			podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, sid, []byte("{}")))
			podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, []byte(`{"speaker":"spk_a","round_id":1,"text":"第一轮"}`)))
			podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, audioA))
			podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, sid, []byte(`{"audio_duration":3.2,"end_time":10,"start_time":6.8}`)))
			return // handler 返回 → 关闭连接
		}
		// 第二次连接：校验同一 X-Api-Request-Id 与 retry_info 断点。
		first := m.session(0)
		if s.requestID != first.requestID {
			t.Errorf("续传 X-Api-Request-Id = %q, 首连 = %q", s.requestID, first.requestID)
		}
		if s.connectID == first.connectID {
			t.Error("X-Api-Connect-Id 应每次连接重新生成")
		}
		ri, _ := s.payload["retry_info"].(map[string]any)
		if ri == nil || ri["retry_task_id"] != first.requestID || ri["last_finished_round_id"] != float64(1) {
			t.Errorf("retry_info = %v, want retry_task_id=%q last_finished_round_id=1", ri, first.requestID)
		}
		if s.payload["input_text"] == "" {
			t.Error("续传 payload 应携带原始 input_text")
		}
		// 150 省略亦须正常处理：[360(B) → 361 → 362] → 152 → 收 FinishConnection → 52。
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, []byte(`{"speaker":"spk_b","round_id":2,"text":"第二轮"}`)))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, audioB))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, sid, []byte(`{"audio_duration":4.1,"end_time":20,"start_time":15.9}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventSessionFinished, sid, []byte("{}")))
		if ev := podReadMockEvent(conn); ev != EventFinishConnection {
			t.Errorf("152 后收到 event = %d, want %d", ev, EventFinishConnection)
		}
		podWriteMockFrame(conn, podServerTextFrame(EventConnectionFinished, sid, []byte("{}")))
	})

	var onRoundIDs []int
	c := NewPodcastClientWithURL(SpeechCred{AppID: "app", AccessToken: "tok"}, m.wsURL())
	res, err := c.Generate(context.Background(), PodcastRequest{
		InputText: "聊聊断点续传。",
		Speakers:  [2]string{"spk_a", "spk_b"},
	}, func(r PodcastRound) { onRoundIDs = append(onRoundIDs, r.RoundID) })
	if err != nil {
		t.Fatalf("Generate() err = %v", err)
	}
	if m.count() != 2 {
		t.Errorf("连接次数 = %d, want 2", m.count())
	}
	if len(res.Rounds) != 2 || res.Rounds[0].Text != "第一轮" || res.Rounds[1].Text != "第二轮" {
		t.Errorf("rounds = %+v, want 含第一、第二轮", res.Rounds)
	}
	if res.Rounds[0].DurationS != 3.2 || res.Rounds[1].DurationS != 4.1 {
		t.Errorf("durations = %v/%v, want 3.2/4.1", res.Rounds[0].DurationS, res.Rounds[1].DurationS)
	}
	wantAudio := append(append([]byte{}, audioA...), audioB...)
	if !bytes.Equal(res.Audio, wantAudio) {
		t.Errorf("audio = % x, want % x（跨连接累积）", res.Audio, wantAudio)
	}
	if len(onRoundIDs) != 2 || onRoundIDs[0] != 1 || onRoundIDs[1] != 2 {
		t.Errorf("onRound rounds = %v", onRoundIDs)
	}
}

// TestPodcastReconnectMidRound 轮次中间断开的续传：重发轮次的音频分片不得与断线前已收分片重复拼接。
func TestPodcastReconnectMidRound(t *testing.T) {
	const sid = "sess-m"
	var m *podMockServer
	m = newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		if m.count() == 1 {
			// 首连：轮次 1 开始并只收到部分音频，随后直接断开（不回 362/152），触发续传。
			podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, sid, []byte("{}")))
			podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, []byte(`{"speaker":"spk_a","round_id":1,"text":"轮次一"}`)))
			podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, []byte{0x01, 0x02}))
			return // 断开连接
		}
		// 续传：服务端从轮次 1 重发完整音频（[0x01,0x02,0x03]）。
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, []byte(`{"speaker":"spk_a","round_id":1,"text":"轮次一"}`)))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, []byte{0x01, 0x02, 0x03}))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, sid, []byte(`{"audio_duration":2.0,"end_time":2.0,"start_time":0}`)))
		podWriteMockFrame(conn, podServerTextFrame(EventSessionFinished, sid, []byte("{}")))
		if ev := podReadMockEvent(conn); ev != EventFinishConnection {
			t.Errorf("152 后收到 event = %d, want %d", ev, EventFinishConnection)
		}
		podWriteMockFrame(conn, podServerTextFrame(EventConnectionFinished, sid, []byte("{}")))
	})

	c := NewPodcastClientWithURL(SpeechCred{AppID: "app", AccessToken: "tok"}, m.wsURL())
	res, err := c.Generate(context.Background(), PodcastRequest{
		InputText: "x",
		Speakers:  [2]string{"a", "b"},
	}, nil)
	if err != nil {
		t.Fatalf("Generate() err = %v", err)
	}
	if m.count() != 2 {
		t.Errorf("连接次数 = %d, want 2", m.count())
	}
	wantAudio := []byte{0x01, 0x02, 0x03}
	if !bytes.Equal(res.Audio, wantAudio) {
		t.Errorf("audio = % x, want % x（重发轮次应先截断已收的不完整分片）", res.Audio, wantAudio)
	}
	if len(res.Rounds) != 1 {
		t.Errorf("rounds = %+v, want 1 条", res.Rounds)
	}
}

// TestPodcastRoundError 362 事件 is_error 变体：该轮生成失败（如内容审核拦截）为致命错误，
// 必须立即终止且不重试续传——否则错误轮次被计入已完成轮次，续传静默跳过该轮。
func TestPodcastRoundError(t *testing.T) {
	const sid = "sess-e"
	m := newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		podWriteMockFrame(conn, podServerTextFrame(EventSessionStarted, sid, []byte("{}")))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundStart, sid, []byte(`{"speaker":"spk_a","round_id":1,"text":"触发审核的文本"}`)))
		podWriteMockFrame(conn, podServerAudioFrame(EventRoundResponse, sid, []byte{0x01}))
		podWriteMockFrame(conn, podServerTextFrame(EventRoundEnd, sid, []byte(`{"is_error":true,"error_msg":"content review failed"}`)))
		// 客户端收到 is_error 应立即断开，此处不再发 152。
	})

	c := NewPodcastClientWithURL(SpeechCred{AppID: "app", AccessToken: "tok"}, m.wsURL())
	_, err := c.Generate(context.Background(), PodcastRequest{
		InputText: "x",
		Speakers:  [2]string{"a", "b"},
	}, nil)
	if err == nil {
		t.Fatal("期望返回错误, 实际 nil")
	}
	if !strings.Contains(err.Error(), "content review failed") {
		t.Errorf("错误信息应包含服务端 error_msg: %v", err)
	}
	if !strings.Contains(err.Error(), "播客轮次生成失败") {
		t.Errorf("错误信息应包含中文包装: %v", err)
	}
	if m.count() != 1 {
		t.Errorf("连接次数 = %d, want 1（362 is_error 为致命错误，重连注定失败不应重试）", m.count())
	}
}

func TestPodcastErrorFrame(t *testing.T) {
	m := newPodMockServer(t, func(t *testing.T, conn *websocket.Conn, s *podMockSession) {
		podWriteMockFrame(conn, podServerErrorFrame(45000001, "invalid param"))
	})

	c := NewPodcastClientWithURL(SpeechCred{AppID: "app", AccessToken: "tok"}, m.wsURL())
	_, err := c.Generate(context.Background(), PodcastRequest{
		InputText: "x",
		Speakers:  [2]string{"a", "b"},
	}, nil)
	if err == nil {
		t.Fatal("期望返回错误, 实际 nil")
	}
	if !strings.Contains(err.Error(), "invalid param") {
		t.Errorf("错误信息应包含服务端 message: %v", err)
	}
	if !strings.Contains(err.Error(), "45000001") {
		t.Errorf("错误信息应包含 code: %v", err)
	}
	if m.count() != 1 {
		t.Errorf("连接次数 = %d, want 1（服务端错误帧不应重试续传）", m.count())
	}
}

func TestPodcastPayloadDispatch(t *testing.T) {
	// Script/URL 模式的 payload 分派（不经 WS，直接验证 StartSession payload 构造）。
	script, err := buildPodStartPayload(PodcastRequest{
		Script:   []PodcastTurn{{Speaker: "a", Text: "你好"}, {Speaker: "b", Text: "在的"}},
		Speakers: [2]string{"a", "b"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(script, &m); err != nil {
		t.Fatal(err)
	}
	if m["action"] != float64(3) || m["input_text"] != nil || m["input_info"] != nil {
		t.Errorf("script payload = %v", m)
	}
	texts, _ := m["nlp_texts"].([]any)
	if len(texts) != 2 || texts[0].(map[string]any)["speaker"] != "a" {
		t.Errorf("nlp_texts = %v", texts)
	}

	urlMode, err := buildPodStartPayload(PodcastRequest{
		URL:      "https://example.com/post",
		Speakers: [2]string{"a", "b"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m = nil
	if err := json.Unmarshal(urlMode, &m); err != nil {
		t.Fatal(err)
	}
	if m["action"] != float64(0) || m["input_text"] != nil || m["nlp_texts"] != nil {
		t.Errorf("url payload = %v", m)
	}
	ii, _ := m["input_info"].(map[string]any)
	if ii == nil || ii["input_url"] != "https://example.com/post" {
		t.Errorf("input_info = %v", ii)
	}

	if _, err := buildPodStartPayload(PodcastRequest{Speakers: [2]string{"a", ""}}, nil); err == nil {
		t.Error("speakers 含空串应报错")
	}
	if _, err := buildPodStartPayload(PodcastRequest{
		Script:   []PodcastTurn{{Speaker: "a", Text: ""}},
		Speakers: [2]string{"a", "b"},
	}, nil); err == nil {
		t.Error("对话稿轮次缺 text 应报错")
	}
}
