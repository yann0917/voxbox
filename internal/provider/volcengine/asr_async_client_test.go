package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

// serverPartialResponseFrame 增量响应帧：SERVER_FULL_RESPONSE + POS_SEQUENCE + JSON + GZIP，
// is_last=false（与 serverLastResponseFrame 的 NEG_SEQUENCE 区分）。
func serverPartialResponseFrame(payloadJSON string) []byte {
	gz := sauc.GzipCompress([]byte(payloadJSON))
	frame := append([]byte{0x11, 0x91, 0x11, 0x00}, beUint32(2)...) // seq=2（占位）
	frame = append(frame, beUint32(uint32(len(gz)))...)
	return append(frame, gz...)
}

// readFullRequestAsync mock 服务端读取并解析 full client request 的 audio/request 段。
func readFullRequestAsync(t *testing.T, conn *websocket.Conn) (audio, request map[string]any) {
	t.Helper()
	_, frame, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读 full request 失败: %v", err)
	}
	if frame[1]>>4 != 0x01 {
		t.Fatalf("full request 帧类型 = %#x, 期望 0x1 (CLIENT_FULL_REQUEST)", frame[1]>>4)
	}
	_, _, payload := parseClientFrame(t, frame)
	var full struct {
		Audio   map[string]any `json:"audio"`
		Request map[string]any `json:"request"`
	}
	if err := json.Unmarshal(payload, &full); err != nil {
		t.Fatalf("解析 full request JSON 失败: %v", err)
	}
	return full.Audio, full.Request
}

// readAudioFrameAsync mock 服务端读取一帧音频，返回 seq、是否最后一包（负 seq 标志）与音频字节。
func readAudioFrameAsync(t *testing.T, conn *websocket.Conn) (seq int32, last bool, payload []byte) {
	t.Helper()
	_, frame, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读音频帧失败: %v", err)
	}
	if frame[1]>>4 != 0x02 {
		t.Fatalf("期望 CLIENT_AUDIO_ONLY_REQUEST, got %#x", frame[1]>>4)
	}
	seq, flags, payload := parseClientFrame(t, frame)
	return seq, flags&0x02 != 0, payload
}

// collectASRAsyncUpdates 后台收集增量更新（模拟 Task 2 relay 的消费方式）。
func collectASRAsyncUpdates(c *ASRAsyncClient, out *[]ASRAsyncUpdate, mu *sync.Mutex) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for u := range c.Updates() {
			mu.Lock()
			*out = append(*out, u)
			mu.Unlock()
		}
	}()
	return done
}

// TestASRAsyncStreamingUpdates 全链路回放：full ack → 增量帧（definite 真/假混合）→
// 增量帧（additions.speaker_id 形态）→ 音频分片（首包 seq=2、末包负 seq、重组一致）→ 终帧。
// 断言回调序列、committed 累积 / unstable 进行中语义、终帧全量 segments 说话人映射。
func TestASRAsyncStreamingUpdates(t *testing.T) {
	chunk0 := bytes.Repeat([]byte{0x01}, 100)
	chunk1 := bytes.Repeat([]byte{0x02}, 100)
	chunk2 := bytes.Repeat([]byte{0x03}, 100)
	pcm := append(append(append([]byte{}, chunk0...), chunk1...), chunk2...)

	const (
		partialA = `{"result":{"text":"今天天气不错，我们下午","utterances":[` +
			`{"text":"今天天气不错，","start_time":0,"end_time":1800,"definite":true},` +
			`{"text":"我们下午","start_time":1900,"end_time":2600,"definite":false}]}}`
		partialB = `{"result":{"text":"今天天气不错，我们下午三点开会。请大家准时参加","utterances":[` +
			`{"text":"今天天气不错，","start_time":0,"end_time":1800,"definite":true,"additions":{"speaker_id":"0"}},` +
			`{"text":"我们下午三点开会。","start_time":1900,"end_time":4200,"definite":true,"additions":{"speaker_id":"1"}},` +
			`{"text":"请大家准时参加","start_time":4300,"end_time":5100,"definite":false}]}}`
		final = `{"audio_info":{"duration":6200},"result":{"text":"今天天气不错，我们下午三点开会。请大家准时参加。","utterances":[` +
			`{"text":"今天天气不错，","start_time":0,"end_time":1800,"definite":true,"additions":{"speaker_id":"0"}},` +
			`{"text":"我们下午三点开会。","start_time":1900,"end_time":4200,"definite":true,"additions":{"speaker_id":"1"}},` +
			`{"text":"请大家准时参加。","start_time":4300,"end_time":6200,"definite":true,"additions":{"speaker_id":"1"}}]}}`
	)

	wsURL := newMockASRServer(t, func(r *http.Request) error {
		if got := r.Header.Get("X-Api-Key"); got != "key-1" {
			return fmt.Errorf("X-Api-Key = %q, 期望 key-1", got)
		}
		if r.Header.Get("X-Api-Connect-Id") == "" {
			return errors.New("缺少 X-Api-Connect-Id")
		}
		return nil
	}, func(t *testing.T, conn *websocket.Conn) {
		// 1. full request：audio 段 pcm/raw/16k/16bit/单声道，request 段 async 选项恒开。
		audio, request := readFullRequestAsync(t, conn)
		for k, want := range map[string]any{"format": "pcm", "codec": "raw", "rate": float64(16000), "bits": float64(16), "channel": float64(1)} {
			if got := audio[k]; got != want {
				t.Errorf("audio.%s = %v, 期望 %v", k, got, want)
			}
		}
		for k, want := range map[string]any{
			"model_name": "bigmodel", "enable_nonstream": true, "show_utterances": true,
			"enable_punc": true, "enable_itn": true, "enable_lid": true,
			"enable_speaker_info": true,
			"corpus":              map[string]any{"context": `{"hotwords":[{"word":"下周三"}]}`},
		} {
			got := request[k]
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("request.%s = %s, 期望 %s", k, gotJSON, wantJSON)
			}
		}

		// 2. ack + 第一帧增量（definite 真/假混合）。
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			t.Errorf("写 ack 失败: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverPartialResponseFrame(partialA)); err != nil {
			t.Errorf("写增量帧 A 失败: %v", err)
			return
		}

		// 3. 音频分片：收到首包（持有帧）后发第二帧增量（additions.speaker_id 形态）。
		seq0, last, payload := readAudioFrameAsync(t, conn)
		if seq0 != 2 || last {
			t.Errorf("首包 seq = %d last = %v, 期望 2 / false", seq0, last)
		}
		if !bytes.Equal(payload, chunk0) {
			t.Errorf("首包 payload != chunk0（持有帧应先上 wire）")
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverPartialResponseFrame(partialB)); err != nil {
			t.Errorf("写增量帧 B 失败: %v", err)
			return
		}

		// 4. 剩余分片直到负 seq；重组（含已消费首包）与原始 PCM 一致，序号连续递增。
		got := append([]byte{}, chunk0...)
		wantSeq := int32(3)
		for {
			seq, last, payload := readAudioFrameAsync(t, conn)
			if last {
				if wantSeq != -seq {
					t.Errorf("末包 seq = %d, 期望 -%d（持有包负 seq 终止）", seq, wantSeq)
				}
				got = append(got, payload...)
				break
			}
			if seq != wantSeq {
				t.Errorf("seq = %d, 期望 %d", seq, wantSeq)
			}
			wantSeq++
			got = append(got, payload...)
		}
		if !bytes.Equal(got, pcm) {
			t.Errorf("重组音频不匹配: got %d bytes, want %d bytes", len(got), len(pcm))
		}

		// 5. 终帧（二遍识别修正全量），随后等待客户端关闭。
		if err := conn.WriteMessage(websocket.BinaryMessage, serverLastResponseFrame(final)); err != nil {
			t.Errorf("写终帧失败: %v", err)
			return
		}
		_, _, _ = conn.ReadMessage()
	})

	client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{Hotwords: "下周三", Speaker: true})
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer client.Close()

	var (
		mu  sync.Mutex
		ups []ASRAsyncUpdate
	)
	collDone := collectASRAsyncUpdates(client, &ups, &mu)
	for _, ch := range [][]byte{chunk0, chunk1, chunk2} {
		if err := client.Send(ch); err != nil {
			t.Fatalf("Send() err = %v", err)
		}
	}
	if err := client.Finish(); err != nil {
		t.Fatalf("Finish() err = %v", err)
	}
	res, err := client.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() err = %v", err)
	}
	<-collDone

	mu.Lock()
	defer mu.Unlock()
	if len(ups) != 2 {
		t.Fatalf("增量回调共 %d 次, 期望 2（A/B 各一）", len(ups))
	}
	if ups[0].CommittedText != "今天天气不错，" || ups[0].UnstableText != "我们下午" {
		t.Errorf("ups[0] committed = %q unstable = %q", ups[0].CommittedText, ups[0].UnstableText)
	}
	if len(ups[0].Segments) != 1 || ups[0].Segments[0].Text != "今天天气不错，" {
		t.Errorf("ups[0].Segments = %+v", ups[0].Segments)
	}
	if ups[1].CommittedText != "今天天气不错，我们下午三点开会。" || ups[1].UnstableText != "请大家准时参加" {
		t.Errorf("ups[1] committed = %q unstable = %q", ups[1].CommittedText, ups[1].UnstableText)
	}
	if len(ups[1].Segments) != 2 || ups[1].Segments[0].Speaker != "0" || ups[1].Segments[1].Speaker != "1" {
		t.Errorf("ups[1].Segments（additions.speaker_id 兜底）= %+v", ups[1].Segments)
	}
	if res.Text != "今天天气不错，我们下午三点开会。请大家准时参加。" {
		t.Errorf("res.Text = %q", res.Text)
	}
	if res.DurationMS != 6200 {
		t.Errorf("res.DurationMS = %d, 期望 6200", res.DurationMS)
	}
	if len(res.Segments) != 3 {
		t.Fatalf("res.Segments 共 %d 句, 期望 3", len(res.Segments))
	}
	if res.Segments[0].StartMS != 0 || res.Segments[0].EndMS != 1800 || res.Segments[2].EndMS != 6200 {
		t.Errorf("res.Segments 时间戳 = %+v", res.Segments)
	}
	if res.Segments[0].Speaker != "0" || res.Segments[1].Speaker != "1" || res.Segments[2].Speaker != "1" {
		t.Errorf("res.Segments 说话人 = %q/%q/%q", res.Segments[0].Speaker, res.Segments[1].Speaker, res.Segments[2].Speaker)
	}
}

// TestASRAsyncDefaultParams 默认选项（无热词/无说话人）full request 断言：
// enable_speaker_info 缺席（omitempty）、corpus.context 缺席、async 恒开项在场。
func TestASRAsyncDefaultParams(t *testing.T) {
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		_, request := readFullRequestAsync(t, conn)
		if _, ok := request["enable_speaker_info"]; ok {
			t.Errorf("enable_speaker_info 应缺席(omitempty), got %v", request["enable_speaker_info"])
		}
		corpus, _ := request["corpus"].(map[string]any)
		if ctx, ok := corpus["context"]; ok {
			t.Errorf("corpus.context 应缺席, got %v", ctx)
		}
		if request["enable_nonstream"] != true || request["show_utterances"] != true {
			t.Errorf("enable_nonstream/show_utterances 应恒为 true: %v / %v", request["enable_nonstream"], request["show_utterances"])
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			return
		}
		var got []byte
		for {
			_, last, payload := readAudioFrameAsync(t, conn)
			got = append(got, payload...)
			if last {
				break
			}
		}
		if !bytes.Equal(got, []byte("pcm-audio")) {
			t.Errorf("重组音频 = %q", got)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverLastResponseFrame(mockFinalPayloadJSON)); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
	})

	client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{})
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer client.Close()
	if err := client.Send([]byte("pcm-audio")); err != nil {
		t.Fatalf("Send() err = %v", err)
	}
	if err := client.Finish(); err != nil {
		t.Fatalf("Finish() err = %v", err)
	}
	res, err := client.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() err = %v", err)
	}
	if res.Text != "这是字节跳动，今日头条母公司。" || len(res.Segments) != 2 {
		t.Errorf("res = %+v", res)
	}
}

// TestASRAsyncErrorFrame 服务端错误帧（ack 后）：读循环置终态错误，Wait 透传中文错误（含 code），
// updates 通道随之关闭。
func TestASRAsyncErrorFrame(t *testing.T) {
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		_, _ = readFullRequestAsync(t, conn)
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverErrorFrame(45000001, `{"error":"音频解码失败"}`)); err != nil {
			return
		}
		for { // 等待客户端关闭
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{})
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer client.Close()
	res, err := client.Wait(context.Background())
	if err == nil {
		t.Fatalf("Wait() 期望错误, got res=%+v", res)
	}
	if !strings.Contains(err.Error(), "45000001") || !strings.Contains(err.Error(), "音频解码失败") {
		t.Errorf("错误信息应含 code 与详情: %v", err)
	}
	if _, ok := <-client.Updates(); ok {
		t.Error("错误终态后 updates 通道应关闭")
	}
	select {
	case <-client.Done():
	default:
		t.Error("错误终态后 Done 应已关闭")
	}
	// 终态后 Finish/Send 报错而非挂死。
	if err := client.Finish(); err == nil {
		t.Error("会话终止后 Finish 应报错")
	}
	if err := client.Send([]byte("x")); err == nil {
		t.Error("会话终止后 Send 应报错")
	}
}

// TestASRAsyncCtxCancel ctx 取消联动关连接（nostream 先例）：Wait 返回可 errors.Is 到
// context.Canceled 的错误，updates/done 收尾，无 goroutine 挂死。
func TestASRAsyncCtxCancel(t *testing.T) {
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		_, _ = readFullRequestAsync(t, conn)
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			return
		}
		for { // 静默保持连接直到客户端断开
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{})
	if err := client.Open(ctx); err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	defer client.Close()
	time.Sleep(50 * time.Millisecond)
	cancel()
	_, err := client.Wait(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() err = %v, 期望 errors.Is(err, context.Canceled)", err)
	}
	select {
	case <-client.Done():
	default:
		t.Error("ctx 取消后 Done 应已关闭")
	}
	if _, ok := <-client.Updates(); ok {
		t.Error("ctx 取消后 updates 通道应关闭")
	}
	// Close 幂等。
	if err := client.Close(); err != nil {
		// 连接已被 ctx 联动关闭，Close 返回错误可接受，但不得 panic/挂死。
		t.Logf("Close() err = %v（连接已关，可接受）", err)
	}
}

// TestASRAsyncNotOpened 未 Open 直接调用应立即报错（不 panic、不挂死）。
func TestASRAsyncNotOpened(t *testing.T) {
	client := NewASRAsyncClient(SpeechCred{APIKey: "k"}, ASRAsyncOptions{})
	if err := client.Send([]byte("x")); err == nil {
		t.Error("未 Open 的 Send 应报错")
	}
	if err := client.Finish(); err == nil {
		t.Error("未 Open 的 Finish 应报错")
	}
	// 审查 Minor-⑤ 回归：Wait 必须立即返回错误而非永挂。
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Wait(waitCtx); err == nil {
		t.Error("未 Open 的 Wait 应立即报错")
	}
}

// TestASRAsyncOpenFailedLifecycle 审查 Critical-1/Minor-⑤ 回归：Open 中段失败（含拨号失败）
// 后 defer Close 不得 panic（ctxClosed 恰好关闭一次），Wait 立即返回错误，Updates/Done 已关闭。
func TestASRAsyncOpenFailedLifecycle(t *testing.T) {
	t.Run("ack阶段错误帧", func(t *testing.T) {
		wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
			_, _ = readFullRequestAsync(t, conn)
			if err := conn.WriteMessage(websocket.BinaryMessage, serverErrorFrame(45000001, `{"error":"音频解码失败"}`)); err != nil {
				return
			}
			for { // 保持连接直到客户端关闭
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		})
		client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{})
		err := client.Open(context.Background())
		if err == nil {
			t.Fatal("Open 期望失败（ack 错误帧）")
		}
		if !strings.Contains(err.Error(), "45000001") {
			t.Errorf("Open 错误应含 code: %v", err)
		}
		_ = client.Close() // Critical-1：此前会二次 close(ctxClosed) panic
		_ = client.Close() // 幂等
		waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := client.Wait(waitCtx); err == nil {
			t.Error("失败会话的 Wait 应返回错误")
		}
		select {
		case <-client.Done():
		default:
			t.Error("Open 失败后 Done 应关闭")
		}
		if _, ok := <-client.Updates(); ok {
			t.Error("Open 失败后 Updates 应关闭")
		}
		// 重复 Open 报错且不 panic。
		if err := client.Open(context.Background()); err == nil {
			t.Error("重复 Open 应报错")
		}
	})
	t.Run("拨号不可达", func(t *testing.T) {
		client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "k"}, "ws://127.0.0.1:1", ASRAsyncOptions{})
		if err := client.Open(context.Background()); err == nil {
			t.Fatal("Open 期望失败（不可达地址）")
		}
		_ = client.Close()
		_ = client.Close()
		waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := client.Wait(waitCtx); err == nil {
			t.Error("失败会话的 Wait 应返回错误")
		}
		select {
		case <-client.Done():
		default:
			t.Error("Open 失败后 Done 应关闭")
		}
	})
}

// TestASRAsyncNormalCompletionClosesConn 审查 Important-2 回归：正常完成（终帧）后客户端必须
// 关闭连接——mock 服务端写完终帧后限时读，观察到客户端关闭错误才算过（否则 conn/ctx-watcher
// 泄漏，审查前 readLoop 终态路径两件都不做）。
func TestASRAsyncNormalCompletionClosesConn(t *testing.T) {
	serverSawClose := make(chan error, 1)
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		_, _ = readFullRequestAsync(t, conn)
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			return
		}
		for {
			_, last, payload := readAudioFrameAsync(t, conn)
			_ = payload
			if last {
				break
			}
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverLastResponseFrame(mockFinalPayloadJSON)); err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, err := conn.ReadMessage() // 期待客户端正常完成后的连接关闭
		serverSawClose <- err
	})

	client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{})
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	if err := client.Send([]byte("pcm-audio")); err != nil {
		t.Fatalf("Send() err = %v", err)
	}
	if err := client.Finish(); err != nil {
		t.Fatalf("Finish() err = %v", err)
	}
	if _, err := client.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() err = %v", err)
	}
	select {
	case err := <-serverSawClose:
		if err == nil {
			t.Error("正常完成后服务端未观察到客户端关闭连接（conn/ctx-watcher 泄漏）")
		}
	case <-time.After(5 * time.Second):
		t.Error("5s 内服务端读未返回（客户端未关闭连接）")
	}
	// 正常完成后 Close 仍幂等可用（上层兜底），不得 panic。
	_ = client.Close()
}

// TestASRAsyncCloseUnblocksBackpressure 审查 Important-3 回归：消费方停读 Updates 且缓冲打满
// （>64 帧）时，Close 必须经 stopped 逃逸解除读循环阻塞，Wait 以会话错误返回而非永挂
// （此前 stopped 只由读循环自身 setEnd 关闭，逃逸分支永远等不到）。
func TestASRAsyncCloseUnblocksBackpressure(t *testing.T) {
	const frames = asrAsyncUpdateBuffer + 6
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		_, _ = readFullRequestAsync(t, conn)
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			return
		}
		payload := `{"result":{"text":"你好","utterances":[{"text":"你好","start_time":0,"end_time":10,"definite":true}]}}`
		for i := 0; i < frames; i++ {
			if err := conn.WriteMessage(websocket.BinaryMessage, serverPartialResponseFrame(payload)); err != nil {
				return
			}
		}
		for { // 保持连接直到客户端关闭
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	client := NewASRAsyncClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL, ASRAsyncOptions{})
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() err = %v", err)
	}
	time.Sleep(300 * time.Millisecond) // 读循环消费满缓冲并阻塞在缓冲外某次通道发送上
	if err := client.Close(); err != nil {
		t.Logf("Close() err = %v（连接关闭错误可接受）", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.Wait(waitCtx)
	if err == nil {
		t.Fatal("Close 后 Wait 应返回错误而非永挂")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait 3s 超时（stopped 逃逸未生效，读循环仍阻塞）: %v", err)
	}
	select {
	case <-client.Done():
	default:
		t.Error("Close 后 Done 应关闭")
	}
	// 关闭的缓冲通道仍会先吐出残余元素，排空至真正关闭为止。
	for range frames {
		if _, ok := <-client.Updates(); !ok {
			break
		}
	}
	if _, ok := <-client.Updates(); ok {
		t.Error("Close 后排空缓冲后 Updates 仍未关闭")
	}
}

// TestSmokeASRAsyncLive 真机冒烟（实时字幕硬验收）：VOXBOX_LIVE_SMOKE=volc 显式启用
// （缺省 t.Skip——会话按时长计费，不门控则本机有凭据时每次 go test 都会真打 2 次计费
// ASR 会话，与 internal/server 侧冒烟门控统一），无凭据/样本亦自动跳过，不进 CI。
// 按实时节奏（200ms/包）喂入双人样本：
//   - 会话 1：说话人分离开——断言增量回调多次、definite 分句出现、speaker 字段（additions 兜底）、
//     Finish 后全量文本合理；
//   - 会话 2（对照）：热词（样本中真实出现的「开会」）+ enable_nonstream 默认开——断言会话成功、
//     全量文本含热词语义词。
//
// 实时节奏耗时 ≈ 音频时长（样本 ≤30s），两会话约 2×样本时长 + 尾部处理。
func TestSmokeASRAsyncLive(t *testing.T) {
	if os.Getenv("VOXBOX_LIVE_SMOKE") != "volc" {
		t.Skip("设 VOXBOX_LIVE_SMOKE=volc 启用真机冒烟（产生计费 ASR 会话，缺省跳过）")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("读取本机配置失败，跳过真机冒烟: %v", err)
	}
	cred := SpeechCred{AppID: cfg.Volc.Speech.AppID, AccessToken: cfg.Volc.Speech.AccessToken, APIKey: cfg.Volc.Speech.APIKey}
	if cred.APIKey == "" && (cred.AppID == "" || cred.AccessToken == "") {
		t.Skip("未配置火山语音凭据，跳过真机冒烟")
	}
	wav, err := smokeDuoSample(t, cred)
	if err != nil {
		t.Skipf("生成双人样本失败，跳过真机冒烟: %v", err)
	}
	pcm, err := smokeMono16kPCM(t, wav)
	if err != nil {
		t.Skipf("转换 16k 单声道 PCM 失败，跳过真机冒烟: %v", err)
	}
	durSec := float64(len(pcm)) / (16000 * 2)
	if durSec > 30 {
		t.Skipf("样本 %.1fs 超过 30s 上限，跳过（实时节奏冒烟耗时可控）", durSec)
	}
	t.Logf("冒烟样本: %d 字节 PCM ≈ %.1fs，200ms/包实时喂入", len(pcm), durSec)

	// 会话 1：说话人分离。
	ups1, res1, err := runASRAsyncLiveSession(t, cred, ASRAsyncOptions{Speaker: true}, pcm)
	if err != nil {
		t.Fatalf("会话1(说话人分离)失败: %v", err)
	}
	t.Logf("会话1: 增量回调 %d 次", len(ups1))
	for i, u := range ups1 {
		t.Logf("  update[%d] committed=%q unstable=%q segments=%d", i, u.CommittedText, u.UnstableText, len(u.Segments))
	}
	t.Logf("会话1 最终: text=%q duration_ms=%d segments=%d", res1.Text, res1.DurationMS, len(res1.Segments))
	if len(ups1) < 2 {
		t.Errorf("增量回调应 ≥2 次, got %d", len(ups1))
	}
	committedSeen := false
	for _, u := range ups1 {
		if u.CommittedText != "" {
			committedSeen = true
		}
	}
	if !committedSeen {
		t.Error("流式过程未出现 definite=true 已提交分句")
	}
	if res1.Text == "" {
		t.Fatal("会话1 最终文本为空")
	}
	if len(res1.Segments) == 0 {
		t.Fatal("会话1 最终 segments 为空")
	}
	speakers := map[string]int{}
	for _, s := range res1.Segments {
		if s.Speaker != "" {
			speakers[s.Speaker]++
		}
	}
	t.Logf("会话1 说话人分布: %v", speakers)
	if len(speakers) == 0 {
		t.Errorf("会话1 未返回任何 speaker_id（顶层与 additions 均无），实时字幕说话人标签不可用")
	}

	// 会话 2（对照）：热词 + enable_nonstream 默认开。
	ups2, res2, err := runASRAsyncLiveSession(t, cred, ASRAsyncOptions{Hotwords: "开会"}, pcm)
	if err != nil {
		t.Fatalf("会话2(热词对照)失败: %v", err)
	}
	t.Logf("会话2(热词=开会): 增量回调 %d 次, 最终 text=%q, segments=%d", len(ups2), res2.Text, len(res2.Segments))
	if len(ups2) < 2 {
		t.Errorf("会话2 增量回调应 ≥2 次, got %d", len(ups2))
	}
	if res2.Text == "" {
		t.Fatal("会话2 最终文本为空")
	}
	if !strings.Contains(res2.Text, "开会") {
		t.Errorf("会话2 全量文本应含热词「开会」: %q", res2.Text)
	}
}

// runASRAsyncLiveSession 真机会话驱动：实时节奏（200ms/包）发送 + 后台收集增量，返回回调与终态。
func runASRAsyncLiveSession(t *testing.T, cred SpeechCred, opts ASRAsyncOptions, pcm []byte) ([]ASRAsyncUpdate, ASRAsyncResult, error) {
	t.Helper()
	const (
		chunkSize = 16000 * 2 * 200 / 1000 // 200ms 16k 单声道 16bit = 6400B
		pace      = 200 * time.Millisecond
	)
	client := NewASRAsyncClient(cred, opts)
	ctx := context.Background()
	if err := client.Open(ctx); err != nil {
		return nil, ASRAsyncResult{}, err
	}
	var (
		mu  sync.Mutex
		ups []ASRAsyncUpdate
	)
	collDone := collectASRAsyncUpdates(client, &ups, &mu)

	sendErr := make(chan error, 1)
	go func() {
		var err error
		for off := 0; off < len(pcm); off += chunkSize {
			end := off + chunkSize
			if end > len(pcm) {
				end = len(pcm)
			}
			if err = client.Send(pcm[off:end]); err != nil {
				break
			}
			time.Sleep(pace)
		}
		if err == nil {
			err = client.Finish()
		}
		sendErr <- err
	}()

	res, err := client.Wait(ctx)
	if serr := <-sendErr; serr != nil && err == nil {
		err = serr
	}
	<-collDone
	_ = client.Close()
	mu.Lock()
	defer mu.Unlock()
	return ups, res, err
}

// smokeMono16kPCM 冒烟样本转 16k 单声道 s16le PCM：已是目标格式直接剥 WAV 头；
// 否则 ffmpeg 转码，缓存 /tmp/voxbox-duo-smoke-16k.pcm 复用。
func smokeMono16kPCM(t *testing.T, wav []byte) ([]byte, error) {
	t.Helper()
	const cache = "/tmp/voxbox-duo-smoke-16k.pcm"
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		return b, nil
	}
	nch, sampwidth, framerate, _, wave, err := sauc.ReadWavInfo(wav)
	if err == nil && nch == 1 && sampwidth == 2 && framerate == 16000 {
		_ = os.WriteFile(cache, wave, 0o600)
		return wave, nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("样本为 %dch/%dbit/%dHz 且本机无 ffmpeg，无法转 16k 单声道 PCM", nch, sampwidth*8, framerate)
	}
	src := filepath.Join(t.TempDir(), "src.wav")
	if err := os.WriteFile(src, wav, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command("ffmpeg", "-y", "-i", src, "-f", "s16le", "-ac", "1", "-ar", "16000", cache)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg 转码失败: %v: %s", err, out)
	}
	return os.ReadFile(cache)
}
