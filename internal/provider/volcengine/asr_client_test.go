package volcengine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

const mockFinalPayloadJSON = `{"audio_info":{"duration":3696},"result":{"text":"这是字节跳动，今日头条母公司。","utterances":[{"text":"这是字节跳动，","start_time":0,"end_time":1705,"definite":true},{"text":"今日头条母公司。","start_time":2110,"end_time":3696,"definite":true}]}}`

func beUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// parseClientFrame 解析客户端帧（full request / audio only），返回 seq、flags 与解压后的 payload。
func parseClientFrame(t *testing.T, frame []byte) (seq int32, flags byte, payload []byte) {
	t.Helper()
	if len(frame) < 8 {
		t.Fatalf("帧过短: %d bytes", len(frame))
	}
	if frame[0] != 0x11 {
		t.Errorf("帧首字节 = %#x, 期望 0x11", frame[0])
	}
	flags = frame[1] & 0x0f
	p := frame[int(frame[0]&0x0f)*4:]
	if flags&0x01 != 0 {
		seq = int32(binary.BigEndian.Uint32(p[:4]))
		p = p[4:]
	}
	size := binary.BigEndian.Uint32(p[:4])
	p = p[4:]
	if uint32(len(p)) < size {
		t.Fatalf("payload 不足: got %d bytes, want %d", len(p), size)
	}
	return seq, flags, sauc.GzipDecompress(p[:size])
}

// serverAckFrame ack 帧：SERVER_FULL_RESPONSE + POS_SEQUENCE + JSON + GZIP，seq=1，空 payload。
func serverAckFrame() []byte {
	frame := append([]byte{0x11, 0x91, 0x11, 0x00}, beUint32(1)...)
	return append(frame, beUint32(0)...) // payload size = 0
}

// serverLastResponseFrame 最终帧：SERVER_FULL_RESPONSE + NEG_SEQUENCE + JSON + GZIP（官方最后一包不带 seq）。
func serverLastResponseFrame(payloadJSON string) []byte {
	gz := sauc.GzipCompress([]byte(payloadJSON))
	frame := append([]byte{0x11, 0x92, 0x11, 0x00}, beUint32(uint32(len(gz)))...)
	return append(frame, gz...)
}

// serverErrorFrame 错误帧：SERVER_ERROR_RESPONSE + POS_SEQUENCE + JSON + GZIP。
func serverErrorFrame(code uint32, payloadJSON string) []byte {
	gz := sauc.GzipCompress([]byte(payloadJSON))
	frame := append([]byte{0x11, 0xF1, 0x11, 0x00}, beUint32(1)...) // seq
	frame = append(frame, beUint32(code)...)                        // code
	frame = append(frame, beUint32(uint32(len(gz)))...)             // payload size
	return append(frame, gz...)
}

type mockASRHandler func(t *testing.T, conn *websocket.Conn)

// newMockASRServer mock ASR WebSocket 服务。beforeUpgrade 在 Upgrade 前执行（返回 err → 401）。
func newMockASRServer(t *testing.T, beforeUpgrade func(r *http.Request) error, serve mockASRHandler) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if beforeUpgrade != nil {
			if err := beforeUpgrade(r); err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade 失败: %v", err)
			return
		}
		defer conn.Close()
		serve(t, conn)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// serveAckThenCollect 读取并校验 full request → 回 ack → 收集音频分片直到最后一包（负 seq）→ 回最终识别结果帧。
func serveAckThenCollect(t *testing.T, conn *websocket.Conn, wantAudio []byte, wantFormat string, wantChunk int) {
	t.Helper()

	// 1. full client request：byte0==0x11、帧类型 CLIENT_FULL_REQUEST、audio.format、request.show_utterances。
	_, frame, err := conn.ReadMessage()
	if err != nil {
		t.Errorf("读 full request 失败: %v", err)
		return
	}
	if frame[0] != 0x11 {
		t.Errorf("full request byte0 = %#x, 期望 0x11", frame[0])
	}
	if frame[1]>>4 != 0x01 {
		t.Errorf("full request 帧类型 = %#x, 期望 0x1 (CLIENT_FULL_REQUEST)", frame[1]>>4)
	}
	_, _, payload := parseClientFrame(t, frame)
	var fullReq struct {
		Audio struct {
			Format string `json:"format"`
		} `json:"audio"`
		Request struct {
			ShowUtterances bool `json:"show_utterances"`
		} `json:"request"`
	}
	if err := json.Unmarshal(payload, &fullReq); err != nil {
		t.Errorf("解析 full request JSON 失败: %v", err)
		return
	}
	if fullReq.Audio.Format != wantFormat {
		t.Errorf("audio.format = %q, 期望 %q", fullReq.Audio.Format, wantFormat)
	}
	if !fullReq.Request.ShowUtterances {
		t.Error("request.show_utterances 应为 true")
	}

	// 2. ack。
	if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
		t.Errorf("写 ack 失败: %v", err)
		return
	}

	// 3. 音频分片：首包 seq=2，末包负 seq；重组后与原始音频一致；每片大小符合分片策略。
	var (
		got      []byte
		segLens  []int
		lastSeg  []byte
		firstSeq = int32(-1)
	)
	for {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("读音频帧失败: %v", err)
			return
		}
		if frame[1]>>4 != 0x02 {
			t.Errorf("期望 CLIENT_AUDIO_ONLY_REQUEST, got %#x", frame[1]>>4)
			return
		}
		seq, flags, seg := parseClientFrame(t, frame)
		if firstSeq == -1 {
			firstSeq = seq
		}
		got = append(got, seg...)
		segLens = append(segLens, len(seg))
		lastSeg = seg
		if flags&0x02 != 0 { // 最后一包：负 seq
			if seq >= 0 {
				t.Errorf("最后一包 seq 应为负, got %d", seq)
			}
			break
		}
	}
	if firstSeq != 2 {
		t.Errorf("首个音频包 seq = %d, 期望 2", firstSeq)
	}
	if !bytes.Equal(got, wantAudio) {
		t.Errorf("重组音频不匹配: got %d bytes, want %d bytes", len(got), len(wantAudio))
	}
	if !bytes.Equal(lastSeg, wantAudio[len(wantAudio)-len(lastSeg):]) {
		t.Errorf("最后一包 != 测试音频尾片: last %d bytes", len(lastSeg))
	}
	for i, n := range segLens {
		if i == len(segLens)-1 {
			break
		}
		if n != wantChunk {
			t.Errorf("第 %d 片大小 = %d, 期望 %d", i, n, wantChunk)
		}
	}

	// 4. 最终识别结果帧。
	if err := conn.WriteMessage(websocket.BinaryMessage, serverLastResponseFrame(mockFinalPayloadJSON)); err != nil {
		t.Errorf("写最终响应失败: %v", err)
		return
	}
	// 等待客户端关闭，保证 httptest.Server.Close 前客户端已读完最终帧。
	_, _, _ = conn.ReadMessage()
}

func TestASRRecognizeNostream(t *testing.T) {
	testAudio := make([]byte, 100*1024) // 4 片：32KB×3 + 4KB 尾片
	for i := range testAudio {
		testAudio[i] = byte(i % 251)
	}
	wsURL := newMockASRServer(t,
		func(r *http.Request) error {
			if got := r.Header.Get("X-Api-Key"); got != "key-1" {
				return fmt.Errorf("X-Api-Key = %q, 期望 key-1", got)
			}
			if got := r.Header.Get("X-Api-Resource-Id"); got != "volc.seedasr.sauc.duration" {
				return fmt.Errorf("X-Api-Resource-Id = %q, 期望 volc.seedasr.sauc.duration", got)
			}
			if r.Header.Get("X-Api-Connect-Id") == "" {
				return errors.New("缺少 X-Api-Connect-Id")
			}
			return nil
		},
		func(t *testing.T, conn *websocket.Conn) {
			serveAckThenCollect(t, conn, testAudio, "mp3", 32*1024)
		})

	client := NewASRClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL)
	resp, err := client.Recognize(context.Background(), ASRNostreamReq{Audio: testAudio, Format: "mp3"})
	if err != nil {
		t.Fatalf("Recognize() err = %v", err)
	}
	if resp.Text != "这是字节跳动，今日头条母公司。" {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.DurationMS != 3696 {
		t.Errorf("DurationMS = %d, 期望 3696", resp.DurationMS)
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("Segments 共 %d 句, 期望 2", len(resp.Segments))
	}
	if resp.Segments[0].Text != "这是字节跳动，" || resp.Segments[0].StartMS != 0 || resp.Segments[0].EndMS != 1705 {
		t.Errorf("Segments[0] = %+v", resp.Segments[0])
	}
	if resp.Segments[1].Text != "今日头条母公司。" || resp.Segments[1].StartMS != 2110 || resp.Segments[1].EndMS != 3696 {
		t.Errorf("Segments[1] = %+v", resp.Segments[1])
	}
}

func TestASRServerErrorFrame(t *testing.T) {
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil { // 消费 full request
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverErrorFrame(45000001, `{"error":"音频解码失败"}`)); err != nil {
			t.Errorf("写 error 帧失败: %v", err)
			return
		}
		_, _, _ = conn.ReadMessage() // 等待客户端关闭
	})

	client := NewASRClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL)
	_, err := client.Recognize(context.Background(), ASRNostreamReq{Audio: []byte("fake-mp3-audio"), Format: "mp3"})
	if err == nil {
		t.Fatal("期望返回错误, 实际 nil")
	}
	if !strings.Contains(err.Error(), "45000001") {
		t.Errorf("错误信息应包含 code: %v", err)
	}
	if !strings.Contains(err.Error(), "音频解码失败") {
		t.Errorf("错误信息应包含服务端错误详情: %v", err)
	}
}

func TestASRAuthReject(t *testing.T) {
	wsURL := newMockASRServer(t,
		func(r *http.Request) error {
			if got := r.Header.Get("X-Api-Key"); got != "key-1" {
				return fmt.Errorf("无效的 API Key: %q", got)
			}
			return nil
		},
		func(t *testing.T, conn *websocket.Conn) {
			t.Error("鉴权失败时不应 Upgrade 成功")
		})

	client := NewASRClientWithURL(SpeechCred{APIKey: "wrong-key"}, wsURL)
	_, err := client.Recognize(context.Background(), ASRNostreamReq{Audio: []byte("audio"), Format: "mp3"})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, 期望 errors.Is(err, ErrAuth)", err)
	}
}

func TestASRWavChunking(t *testing.T) {
	// 1.5s 16kHz 单声道 16bit wav：分片 = 1ch × 2B × 16000 × 200ms = 6400B；音频原样直发（含 44B 头）。
	pcm := make([]byte, 48000)
	for i := range pcm {
		pcm[i] = byte(i % 253)
	}
	hdr := sauc.WavHeader{
		ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
		ChunkSize:     uint32(36 + len(pcm)),
		Format:        [4]byte{'W', 'A', 'V', 'E'},
		Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
		Subchunk1Size: 16,
		AudioFormat:   1,
		NumChannels:   1,
		SampleRate:    16000,
		ByteRate:      16000 * 2,
		BlockAlign:    2,
		BitsPerSample: 16,
		Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
		Subchunk2Size: uint32(len(pcm)),
	}
	var wav bytes.Buffer
	if err := binary.Write(&wav, binary.LittleEndian, hdr); err != nil {
		t.Fatalf("构造 WAV 头失败: %v", err)
	}
	wav.Write(pcm)

	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		serveAckThenCollect(t, conn, wav.Bytes(), "wav", 6400)
	})

	client := NewASRClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL)
	resp, err := client.Recognize(context.Background(), ASRNostreamReq{Audio: wav.Bytes(), Format: "wav"})
	if err != nil {
		t.Fatalf("Recognize() err = %v", err)
	}
	if resp.DurationMS != 3696 || resp.Text != "这是字节跳动，今日头条母公司。" {
		t.Errorf("resp = %+v", resp)
	}
}
