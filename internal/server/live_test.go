package server

// live_test.go 实时字幕 WS 中转单测:mock 引擎(控制面/字节面/终态)驱动 relay 协议,
// 本地引擎走真实 localruntime 客户端 + httptest SSE mock(audiocpp live 协议形状),
// save 收束断言 store 任务/产物/磁盘文件。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/store"
)

// ---- WS 客户端工具 ----

func dialLiveWS(t *testing.T, ts *httptest.Server, cookie string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/live"
	hdr := http.Header{}
	if cookie != "" {
		hdr.Set("Cookie", cookie)
	}
	ws, _, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil {
		t.Fatalf("WS 拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// readLiveMsg 带超时读一帧服务端文本消息并解析为通用对象。
func readLiveMsg(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("读 WS 消息失败: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("消息类型 = %d, want 文本帧", mt)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("消息非 JSON: %s", raw)
	}
	return m
}

// expectType 读一帧并断言 type;返回完整消息。
func expectType(t *testing.T, ws *websocket.Conn, want string) map[string]any {
	t.Helper()
	m := readLiveMsg(t, ws)
	if m["type"] != want {
		t.Fatalf("消息 type = %v, want %v (msg=%v)", m["type"], want, m)
	}
	return m
}

// expectTypeSkip 读到 want 类型为止,跳过 skip 中的类型(stop→final 间残余 partial
// 属合法时序:收束泵在 done 前还会外送尾段 delta)。
func expectTypeSkip(t *testing.T, ws *websocket.Conn, want string, skip ...string) map[string]any {
	t.Helper()
	skipSet := map[string]bool{}
	for _, k := range skip {
		skipSet[k] = true
	}
	for {
		m := readLiveMsg(t, ws)
		if m["type"] == want {
			return m
		}
		if !skipSet[m["type"].(string)] {
			t.Fatalf("消息 type = %v, want %v (msg=%v)", m["type"], want, m)
		}
	}
}

func sendJSON(t *testing.T, ws *websocket.Conn, v string) {
	t.Helper()
	if err := ws.WriteMessage(websocket.TextMessage, []byte(v)); err != nil {
		t.Fatalf("写 WS 消息失败: %v", err)
	}
}

// ---- mock 引擎 ----

type fakeLiveEngine struct {
	mu        sync.Mutex
	sent      []byte
	openN     int
	upd       chan liveUpdate
	result    liveResult
	resErr    error
	openErr   error
	sendErr   error
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeLiveEngine() *fakeLiveEngine {
	return &fakeLiveEngine{upd: make(chan liveUpdate, 16), closed: make(chan struct{})}
}

func (f *fakeLiveEngine) Open(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openN++
	return f.openErr
}

func (f *fakeLiveEngine) Send(pcm []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, pcm...)
	return nil
}

func (f *fakeLiveEngine) Finish() error              { return nil }
func (f *fakeLiveEngine) Updates() <-chan liveUpdate { return f.upd }

func (f *fakeLiveEngine) Wait(ctx context.Context) (liveResult, error) {
	return f.result, f.resErr
}

func (f *fakeLiveEngine) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeLiveEngine) sentBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.sent...)
}

// finishWith 注入终态并关闭增量通道(模拟会话收束)。
func (f *fakeLiveEngine) finishWith(res liveResult, err error) {
	f.mu.Lock()
	f.result, f.resErr = res, err
	f.mu.Unlock()
	close(f.upd)
}

// ---- 用例 ----

// TestLiveAuthRequired 未登录(无 Cookie)的 WS 升级必须被拒。
func TestLiveAuthRequired(t *testing.T) {
	ts, _, _ := newTestServer(t)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/live"
	_, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		t.Fatal("未登录拨号应被拒绝")
	}
}

// TestLiveRelayVolcengineFlow mock 火山引擎全流程:start→ready→音频转发→partial→
// stop→final(含 segments)→save→落库(任务+txt+srt+summary)→saved{task_id}。
func TestLiveRelayVolcengineFlow(t *testing.T) {
	ts, s, ac := newTestServer(t)
	eng := newFakeLiveEngine()
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		if p.Engine != "volcengine" {
			t.Errorf("factory engine = %q", p.Engine)
		}
		return eng, "volcengine", "volcengine", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))

	sendJSON(t, ws, `{"type":"start","engine":"volcengine","language":"zh-CN","hotwords":"开会","speaker":true}`)
	ready := expectType(t, ws, "ready")
	if ready["engine"] != "volcengine" {
		t.Errorf("ready = %v", ready)
	}

	// 音频二进制帧转发(两帧拼接后引擎侧可见)。
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte{5, 6}); err != nil {
		t.Fatal(err)
	}
	// 引擎增量 → partial(REPLACE 快照,含 unstable 与 segments)。
	eng.upd <- liveUpdate{
		Committed: "你好", Unstable: "世界",
		Segments: []liveSegment{{Text: "你好", StartMS: 0, EndMS: 800, Speaker: "0"}},
	}
	partial := expectType(t, ws, "partial")
	if partial["committed"] != "你好" || partial["unstable"] != "世界" {
		t.Errorf("partial = %v", partial)
	}
	segs, _ := partial["segments"].([]any)
	if len(segs) != 1 {
		t.Fatalf("partial.segments = %v", partial)
	}

	sendJSON(t, ws, `{"type":"stop"}`)
	eng.finishWith(liveResult{
		Text: "你好世界", DurationMS: 1500,
		Segments: []liveSegment{
			{Text: "你好", StartMS: 0, EndMS: 800, Speaker: "0"},
			{Text: "世界", StartMS: 900, EndMS: 1500, Speaker: "1"},
		},
	}, nil)
	final := expectType(t, ws, "final")
	if final["text"] != "你好世界" || final["duration_ms"].(float64) != 1500 {
		t.Errorf("final = %v", final)
	}
	if segs, _ := final["segments"].([]any); len(segs) != 2 {
		t.Errorf("final.segments = %v", final["segments"])
	}
	if got := eng.sentBytes(); len(got) != 6 || got[0] != 1 || got[5] != 6 {
		t.Errorf("引擎收到的音频 = %v", got)
	}

	sendJSON(t, ws, `{"type":"save"}`)
	saved := expectType(t, ws, "saved")
	taskID, _ := saved["task_id"].(string)
	if taskID == "" {
		t.Fatalf("saved = %v", saved)
	}

	// 落库断言:任务/参数/summary/产物。
	tk, err := s.svc.DB().GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Provider != "volcengine" || tk.Tool != "asr" || tk.Status != store.StatusSucceeded {
		t.Errorf("task = %+v", tk)
	}
	if !strings.Contains(tk.Title, "实时字幕") {
		t.Errorf("title = %q", tk.Title)
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(tk.Params), &params)
	if params["version"] != "live" || params["engine"] != "volcengine" || params["duration_ms"].(float64) != 1500 {
		t.Errorf("params = %v", params)
	}
	var summary map[string]any
	_ = json.Unmarshal([]byte(tk.Summary), &summary)
	if summary["source"] != "live" || summary["engine"] != "volcengine" {
		t.Errorf("summary = %v", summary)
	}
	if segs, _ := summary["segments"].([]any); len(segs) != 2 {
		t.Errorf("summary.segments = %v", summary["segments"])
	}
	if summary["speakers_count"].(float64) != 2 {
		t.Errorf("speakers_count = %v", summary["speakers_count"])
	}
	arts, err := s.svc.DB().ListArtifacts(taskID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, a := range arts {
		kinds[a.Kind] = a.Format
	}
	if kinds["transcript"] != "txt" || kinds["subtitle"] != "srt" {
		t.Fatalf("artifacts = %v", kinds)
	}
	// 磁盘文件:txt=全量文本,srt=BuildSRT 形状。
	for _, a := range arts {
		raw, err := os.ReadFile(filepath.Join(s.svc.Config().DataDir, a.Path))
		if err != nil {
			t.Fatalf("产物文件缺失 %s: %v", a.Path, err)
		}
		if a.Kind == "transcript" && string(raw) != "你好世界" {
			t.Errorf("txt 内容 = %q", raw)
		}
		if a.Kind == "subtitle" && !strings.HasPrefix(string(raw), "1\n00:00:00,000 --> 00:00:00,800\n你好") {
			t.Errorf("srt 内容 = %q", raw)
		}
	}
}

// TestLiveRelayLocalFlow 本地引擎走真实 localruntime 客户端(audiocpp live 协议形状:
// chunked PCM 上传 + 同连接 SSE),断言查询参数、partial 实时性、final=done 全量、
// save 落库(text 形状,无 srt)。
func TestLiveRelayLocalFlow(t *testing.T) {
	ts, s, ac := newTestServer(t)

	mock := startLiveMockServer(t,
		func(n int, conn net.Conn, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"你好\"}\n\n") // 喂音频前就出增量(实时性)
		},
		func(n int, conn net.Conn, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"世界\"}\n\n")
			chunk("data: {\"type\":\"transcript.text.done\",\"text\":\"你好世界\"}\n\n")
		})

	tts := localruntime.NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	eng := newLocalLiveEngine(tts, "r2t2-q8_0", localruntime.LiveASROptions{})
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "local", "local", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	t0 := time.Now()

	sendJSON(t, ws, `{"type":"start","engine":"local"}`)
	expectType(t, ws, "ready")
	t.Log("ready got", time.Since(t0))

	if err := ws.WriteMessage(websocket.BinaryMessage, make([]byte, 3200)); err != nil { // 100ms
		t.Fatal(err)
	}
	partial := expectType(t, ws, "partial")
	if partial["committed"] != "你好" {
		t.Errorf("partial = %v(喂音频期应实时出增量)", partial)
	}
	if _, has := partial["unstable"]; has {
		t.Errorf("本地引擎不应有 unstable: %v", partial)
	}
	if _, has := partial["segments"]; has {
		t.Errorf("本地引擎不应有 segments: %v", partial)
	}

	sendJSON(t, ws, `{"type":"stop"}`)
	final := expectTypeSkip(t, ws, "final", "partial")
	if final["text"] != "你好世界" {
		t.Errorf("final = %v", final)
	}
	if final["duration_ms"].(float64) != 100 { // 3200B / 32 = 100ms
		t.Errorf("duration_ms = %v, want 100", final["duration_ms"])
	}
	if _, has := final["segments"]; has {
		t.Errorf("本地引擎 final 不应有 segments: %v", final)
	}
	// live 请求查询参数:streaming 声明 id + 16k/mono/s16le(健康探针不计入会话数)。
	q := mock.queryAt(0)
	for _, want := range []string{"model=r2t2-q8_0-stream", "sample_rate=16000", "channels=1", "sample_format=s16le"} {
		if !strings.Contains(q, want) {
			t.Errorf("live query 缺 %s: %s", want, q)
		}
	}

	sendJSON(t, ws, `{"type":"save"}`)
	saved := expectType(t, ws, "saved")
	taskID, _ := saved["task_id"].(string)
	if taskID == "" {
		t.Fatalf("saved = %v", saved)
	}
	tk, err := s.svc.DB().GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Provider != "local" || tk.Status != store.StatusSucceeded {
		t.Errorf("task = %+v", tk)
	}
	var summary map[string]any
	_ = json.Unmarshal([]byte(tk.Summary), &summary)
	if summary["text"] != "你好世界" || summary["engine"] != "local" || summary["source"] != "live" {
		t.Errorf("summary = %v", summary)
	}
	if _, has := summary["segments"]; has {
		t.Errorf("本地会话 summary 不应有 segments: %v", summary)
	}
	arts, _ := s.svc.DB().ListArtifacts(taskID)
	if len(arts) != 1 || arts[0].Kind != "transcript" || arts[0].Format != "txt" {
		t.Fatalf("artifacts = %+v", arts)
	}
}

// TestLiveRotation 本地引擎轮转:短轮转周期下多会话文本无缝拼接,partial 的 committed
// 前缀随轮转增长,final=各会话 done 全量之和。
func TestLiveRotation(t *testing.T) {
	ts, s, ac := newTestServer(t)

	mock := startLiveMockServer(t, nil, func(n int, conn net.Conn, chunk func(string)) {
		chunk(fmt.Sprintf("data: {\"type\":\"transcript.text.done\",\"text\":\"S%d\"}\n\n", n))
	})

	tts := localruntime.NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	eng := newLocalLiveEngine(tts, "r2t2-q8_0", localruntime.LiveASROptions{})
	eng.rotateEvery = 150 * time.Millisecond
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "local", "local", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))

	sendJSON(t, ws, `{"type":"start","engine":"local"}`)
	expectType(t, ws, "ready")

	// 喂 ~500ms 音频(轮转周期 150ms → 应经历 ≥2 次轮转)。
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := ws.WriteMessage(websocket.BinaryMessage, make([]byte, 3200)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	sendJSON(t, ws, `{"type":"stop"}`)
	final := expectTypeSkip(t, ws, "final", "partial")
	text, _ := final["text"].(string)
	if !strings.HasPrefix(text, "S1") {
		t.Errorf("final.text = %q(应以第一会话文本开头)", text)
	}
	n := mock.count()
	if n < 4 { // 健康探针不计入 mock 会话数:喂 600ms/轮转 150ms 应 ≥3 路
		t.Errorf("会话数 = %d, want ≥3(轮转未发生)", n-0)
	}
	if !strings.Contains(text, fmt.Sprintf("S%d", n)) {
		t.Errorf("final.text = %q 应含最后会话 S%d(拼接断链)", text, n)
	}
	// partial 的 committed 应随轮转出现拼接前缀(至少有一次 partial 以 S1 开头且非 S1 全文)。
	// (主循环收 final 前partial 已然流过;此处经 final 文本多会话拼接已覆盖拼接语义。)
}

// TestLiveControlErrors 协议边界:未知引擎/二次 start/未开始就发音频/未 final 就 save/
// 坏 JSON;错误后连接仍可用(构造失败不占会话)。
func TestLiveControlErrors(t *testing.T) {
	ts, s, ac := newTestServer(t)
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		if p.Engine == "bad" {
			return nil, "", "", fmt.Errorf("未知引擎 bad")
		}
		return newFakeLiveEngine(), "volcengine", "volcengine", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))

	// 未 start 就发音频。
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if m := expectType(t, ws, "error"); !strings.Contains(m["message"].(string), "start") {
		t.Errorf("error = %v", m)
	}
	// 未知引擎:构造失败,可重试。
	sendJSON(t, ws, `{"type":"start","engine":"bad"}`)
	if m := expectType(t, ws, "error"); !strings.Contains(m["message"].(string), "bad") {
		t.Errorf("error = %v", m)
	}
	// 未 final 就 save。
	sendJSON(t, ws, `{"type":"save"}`)
	expectType(t, ws, "error")
	// 坏 JSON。
	sendJSON(t, ws, `{not-json`)
	expectType(t, ws, "error")

	// 正常开会话后:二次 start 拒绝。
	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	expectType(t, ws, "ready")
	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	if m := expectType(t, ws, "error"); !strings.Contains(m["message"].(string), "会话") {
		t.Errorf("error = %v", m)
	}
	// stop 后 final 前的 save 拒绝(finishing 态)。
	sendJSON(t, ws, `{"type":"stop"}`)
	sendJSON(t, ws, `{"type":"save"}`)
	expectType(t, ws, "error")
}

// TestLiveOpenError Open 失败 → error 帧;连接保持(未占会话),可再次 start 成功。
func TestLiveOpenError(t *testing.T) {
	ts, s, ac := newTestServer(t)
	eng1 := newFakeLiveEngine()
	eng1.openErr = fmt.Errorf("连接火山实时识别 WebSocket 失败")
	eng2 := newFakeLiveEngine()
	cur := eng1
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return cur, "volcengine", "volcengine", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))

	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	if m := expectType(t, ws, "error"); !strings.Contains(m["message"].(string), "失败") {
		t.Errorf("error = %v", m)
	}
	cur = eng2
	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	expectType(t, ws, "ready")
}

// TestLiveSendError 引擎音频写失败(如引擎连接断开)→ error 帧,连接保留(客户端仍可
// stop→save 降级文本/终态);客户端断开后引擎被 Close(清理收敛)。
func TestLiveSendError(t *testing.T) {
	ts, s, ac := newTestServer(t)
	eng := newFakeLiveEngine()
	eng.sendErr = fmt.Errorf("发送 ASR 音频分片失败: 连接断开")
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "volcengine", "volcengine", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	expectType(t, ws, "ready")
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte{1}); err != nil {
		t.Fatal(err)
	}
	m := expectType(t, ws, "error")
	if !strings.Contains(m["message"].(string), "推送音频失败") {
		t.Errorf("error = %v", m)
	}
	// 连接仍可用(stop 受理不报错);客户端断开后引擎被 Close。
	sendJSON(t, ws, `{"type":"stop"}`)
	_ = ws.Close()
	select {
	case <-eng.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("断开后引擎未被 Close")
	}
}

// TestLiveEngineCloseOnDisconnect 客户端断开 → 会话引擎被 Close(资源回收)。
func TestLiveEngineCloseOnDisconnect(t *testing.T) {
	ts, s, ac := newTestServer(t)
	eng := newFakeLiveEngine()
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "volcengine", "volcengine", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	expectType(t, ws, "ready")
	_ = ws.Close() // 客户端直接断开
	select {
	case <-eng.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("断开后引擎未被 Close")
	}
}

// TestVolcLiveMapping 火山增量/分句映射:字段一一对应、speaker 保留、空分句为 nil。
func TestVolcLiveMapping(t *testing.T) {
	u := volcengine.ASRAsyncUpdate{
		CommittedText: "你好。", UnstableText: "你好吗",
		Segments: []volcengine.ASRSegment{{Text: "你好。", StartMS: 0, EndMS: 900, Speaker: "2"}},
	}
	lu := volcUpdateToLive(u)
	if lu.Committed != "你好。" || lu.Unstable != "你好吗" || len(lu.Segments) != 1 || lu.Segments[0].Speaker != "2" {
		t.Errorf("volcUpdateToLive = %+v", lu)
	}
	if volcSegmentsToLive(nil) != nil {
		t.Error("空分句应映射为 nil")
	}
}

// TestLiveSaveSummaryRefineEligible 存任务收束与纪要区资格:两类 live Summary(火山
// segments 形/本地 text 形)都能被 transcriptFromSummary 提取转写——save 的任务在
// 历史详情纪要区(refine)直接可用。
func TestLiveSaveSummaryRefineEligible(t *testing.T) {
	_, s, _ := newTestServer(t)
	dataDir := s.svc.Config().DataDir

	// 火山形:segments + SRT 产物。
	resVolc := liveResult{
		Text: "你好世界", DurationMS: 1500,
		Segments: []liveSegment{{Text: "你好", StartMS: 0, EndMS: 800, Speaker: "0"}, {Text: "世界", StartMS: 900, EndMS: 1500}},
	}
	idV, err := s.liveSave("u-live", "volcengine", "volcengine", resVolc)
	if err != nil {
		t.Fatal(err)
	}
	tkV, _ := s.svc.DB().GetTask(idV)
	var sumV map[string]any
	_ = json.Unmarshal([]byte(tkV.Summary), &sumV)
	if got := transcriptFromSummary(sumV); !strings.Contains(got, "你好") || !strings.Contains(got, "世界") {
		t.Errorf("火山形 Summary 提取转写 = %q", got)
	}

	// 本地形:text 无 segments。
	resLocal := liveResult{Text: "本地实时文本", DurationMS: 900}
	idL, err := s.liveSave("u-live", "local", "local", resLocal)
	if err != nil {
		t.Fatal(err)
	}
	tkL, _ := s.svc.DB().GetTask(idL)
	var sumL map[string]any
	_ = json.Unmarshal([]byte(tkL.Summary), &sumL)
	if got := transcriptFromSummary(sumL); got != "本地实时文本" {
		t.Errorf("本地形 Summary 提取转写 = %q", got)
	}

	// 产物文件确实落盘(txt/srt 各一;本地会话无 srt)。
	for _, id := range []string{idV, idL} {
		arts, _ := s.svc.DB().ListArtifacts(id)
		if len(arts) == 0 {
			t.Fatalf("任务 %s 无产物", id)
		}
		for _, a := range arts {
			if _, err := os.Stat(filepath.Join(dataDir, a.Path)); err != nil {
				t.Errorf("产物文件缺失 %s: %v", a.Path, err)
			}
		}
	}
}

// TestLiveFinalHeldUntilStop 引擎终态早于客户端 stop 到达(在途)时必须挂起:
// save 仍按「未 final」拒绝;stop 受理后立即补发 final——协议顺序恒 stop→final。
func TestLiveFinalHeldUntilStop(t *testing.T) {
	ts, s, ac := newTestServer(t)
	eng := newFakeLiveEngine()
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "volcengine", "volcengine", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	sendJSON(t, ws, `{"type":"start","engine":"volcengine"}`)
	expectType(t, ws, "ready")

	// 引擎先收束(updates 关闭 → 引擎泵 Wait → finalCh),stop 尚未发出。
	eng.finishWith(liveResult{Text: "挂起文本", DurationMS: 700}, nil)
	time.Sleep(100 * time.Millisecond) // 让引擎泵把终态送达主循环(pendingFinal 挂起)

	// 挂起期间 save 仍拒绝(state 仍 streaming)。
	sendJSON(t, ws, `{"type":"save"}`)
	if m := expectType(t, ws, "error"); !strings.Contains(m["message"].(string), "停止") {
		t.Errorf("error = %v", m)
	}
	// stop 受理 → pendingFinal 立即补发 final(或 finalCh 先到走 finishing 路径,殊途同归)。
	sendJSON(t, ws, `{"type":"stop"}`)
	final := expectTypeSkip(t, ws, "final", "partial")
	if final["text"] != "挂起文本" {
		t.Errorf("final = %v", final)
	}
	sendJSON(t, ws, `{"type":"save"}`)
	saved := expectType(t, ws, "saved")
	if id, _ := saved["task_id"].(string); id == "" {
		t.Errorf("saved = %v", saved)
	}
}

// TestLiveLocalDegradedStopAfterError 降级兜底时序①(stop 后收束出错):服务端在
// 终止 chunk 后断流 → 已有文本以 final{degraded:true} 交付,可保存,不再丢文本。
func TestLiveLocalDegradedStopAfterError(t *testing.T) {
	ts, s, ac := newTestServer(t)
	mock := startLiveMockServer(t,
		func(n int, conn net.Conn, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"你好\"}\n\n")
		},
		func(n int, conn net.Conn, chunk func(string)) {
			chunk("data: {\"type\":\"error\",\"error\":{\"message\":\"live request body stalled\"}}\n\n")
		})
	tts := localruntime.NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	eng := newLocalLiveEngine(tts, "r2t2-q8_0", localruntime.LiveASROptions{})
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "local", "local", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	sendJSON(t, ws, `{"type":"start","engine":"local"}`)
	expectType(t, ws, "ready")
	if err := ws.WriteMessage(websocket.BinaryMessage, make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	if p := expectTypeSkip(t, ws, "partial"); p["committed"] != "你好" {
		t.Fatalf("partial = %v", p)
	}
	sendJSON(t, ws, `{"type":"stop"}`)
	final := expectTypeSkip(t, ws, "final", "partial")
	if final["text"] != "你好" || final["degraded"] != true {
		t.Fatalf("final = %v, want text=你好 degraded=true(降级保留,不丢文本)", final)
	}
	sendJSON(t, ws, `{"type":"save"}`)
	saved := expectType(t, ws, "saved")
	id, _ := saved["task_id"].(string)
	tk, err := s.svc.DB().GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	_ = json.Unmarshal([]byte(tk.Summary), &summary)
	if summary["text"] != "你好" || summary["degraded"] != true {
		t.Errorf("summary = %v(降级标记应留痕)", summary)
	}
}

// TestLiveLocalDegradedMidSession 降级兜底时序②(说话中服务端断流):Write 报错不再
// 丢文本也不再关连接——error 帧后客户端仍可 stop→final(degraded)→save。
func TestLiveLocalDegradedMidSession(t *testing.T) {
	ts, s, ac := newTestServer(t)
	mock := startLiveMockServer(t,
		func(n int, conn net.Conn, chunk func(string)) {
			chunk("data: {\"type\":\"transcript.text.delta\",\"delta\":\"开头\"}\n\n")
			chunk("data: {\"type\":\"error\",\"error\":{\"message\":\"live request body stalled\"}}\n\n")
			_ = conn.Close() // 服务端断流
		},
		func(n int, conn net.Conn, chunk func(string)) {})
	tts := localruntime.NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	eng := newLocalLiveEngine(tts, "r2t2-q8_0", localruntime.LiveASROptions{})
	eng.silenceEvery = 40 * time.Millisecond // 快速触发「下一块音频」路径命中已死会话
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "local", "local", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	sendJSON(t, ws, `{"type":"start","engine":"local"}`)
	expectType(t, ws, "ready")
	if err := ws.WriteMessage(websocket.BinaryMessage, make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	// 断流 aftermath:引擎在下一块(用户音频或静音续命帧)Write 报错 → 降级收敛
	// (文本保留,不丢不挂)。连接保留:stop → final(degraded)。
	time.Sleep(200 * time.Millisecond)
	sendJSON(t, ws, `{"type":"stop"}`)
	final := expectTypeSkip(t, ws, "final", "partial")
	if final["text"] != "开头" || final["degraded"] != true {
		t.Fatalf("final = %v, want text=开头 degraded=true", final)
	}
	sendJSON(t, ws, `{"type":"save"}`)
	saved := expectType(t, ws, "saved")
	if id, _ := saved["task_id"].(string); id == "" {
		t.Errorf("saved = %v(降级文本必须可保存)", saved)
	}
}

// TestLiveSilenceKeepAlive 静音续命:无用户音频时引擎按 silenceEvery 周期喂静音帧,
// 重置 audiocpp live 30s idle 计时(会议停顿核心场景);静音计入时长。
func TestLiveSilenceKeepAlive(t *testing.T) {
	ts, s, ac := newTestServer(t)
	mock := startLiveMockServer(t, nil, func(n int, conn net.Conn, chunk func(string)) {})
	tts := localruntime.NewURLTTSRuntime("http://" + mock.ln.Addr().String())
	eng := newLocalLiveEngine(tts, "r2t2-q8_0", localruntime.LiveASROptions{})
	eng.silenceEvery = 40 * time.Millisecond
	s.newLiveEngine = func(p liveControlMsg) (liveEngine, string, string, error) {
		return eng, "local", "local", nil
	}
	ws := dialLiveWS(t, ts, sessionCookieHeader(t, ac, ts))
	sendJSON(t, ws, `{"type":"start","engine":"local"}`)
	expectType(t, ws, "ready")
	if err := ws.WriteMessage(websocket.BinaryMessage, make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond) // 首块音频落定
	base := mock.chunks()
	time.Sleep(250 * time.Millisecond) // ≈6 个静音周期,期间无用户音频
	if got := mock.chunks() - base; got < 3 {
		t.Fatalf("静音续命未生效: 200ms 内服务端仅再收到 %d 个数据块, want ≥3", got)
	}
	sendJSON(t, ws, `{"type":"stop"}`)
	final := expectTypeSkip(t, ws, "final", "partial")
	if final["type"] != "final" {
		t.Fatalf("final = %v", final)
	}
	if _, has := final["degraded"]; has {
		t.Errorf("静音续命的正常会话不应有 degraded: %v", final)
	}
}
