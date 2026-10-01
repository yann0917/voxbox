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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/config"
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

// TestASRRecognizeNostreamSpeakerParams table-driven 断言：选项置 true 时 full request 的
// request 段 JSON 含对应 key；未置的 key 因 omitempty 缺席（默认请求不透传新参数）。
// enable_speaker_info 官方生效条件为 language 为空或 zh-CN，一并断言 language 透传行为。
func TestASRRecognizeNostreamSpeakerParams(t *testing.T) {
	// nostream 新参数全集：wantRequest 未收录的 key 必须在 payload 中缺席。
	newParamKeys := []string{"enable_speaker_info", "enable_lid", "enable_emotion_detection", "ssd_version"}
	tests := []struct {
		name        string
		req         ASRNostreamReq
		wantRequest map[string]any // request 段期望出现的 key→值
	}{
		{
			name: "默认不透传新参数",
			req:  ASRNostreamReq{Audio: []byte("audio"), Format: "mp3"},
		},
		{
			name:        "仅说话人分离",
			req:         ASRNostreamReq{Audio: []byte("audio"), Format: "mp3", SpeakerInfo: true},
			wantRequest: map[string]any{"enable_speaker_info": true},
		},
		{
			name:        "全量新参数含 ssd_version",
			req:         ASRNostreamReq{Audio: []byte("audio"), Format: "mp3", SpeakerInfo: true, LID: true, Emotion: true, SSDVersion: "200"},
			wantRequest: map[string]any{"enable_speaker_info": true, "enable_lid": true, "enable_emotion_detection": true, "ssd_version": "200"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				gotRequest map[string]any
				gotAudio   map[string]any
			)
			wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
				_, frame, err := conn.ReadMessage() // full client request
				if err != nil {
					t.Errorf("读 full request 失败: %v", err)
					return
				}
				_, _, payload := parseClientFrame(t, frame)
				var full struct {
					Audio   map[string]any `json:"audio"`
					Request map[string]any `json:"request"`
				}
				if err := json.Unmarshal(payload, &full); err != nil {
					t.Errorf("解析 full request JSON 失败: %v", err)
					return
				}
				gotRequest, gotAudio = full.Request, full.Audio
				if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
					t.Errorf("写 ack 失败: %v", err)
					return
				}
				// 排空音频分片直到最后一包（负 seq），再回最终识别结果帧。
				for {
					if _, frame, err := conn.ReadMessage(); err != nil || frame[1]&0x0f&0x02 != 0 {
						break
					}
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, serverLastResponseFrame(mockFinalPayloadJSON)); err != nil {
					t.Errorf("写最终响应失败: %v", err)
				}
				_, _, _ = conn.ReadMessage() // 等待客户端关闭
			})

			client := NewASRClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL)
			_, err := client.Recognize(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Recognize() err = %v", err)
			}
			for _, k := range newParamKeys {
				want, inWant := tt.wantRequest[k]
				got, ok := gotRequest[k]
				if inWant && (!ok || fmt.Sprint(got) != fmt.Sprint(want)) {
					t.Errorf("request.%s = %v (present=%v), 期望 %v", k, got, ok, want)
				}
				if !inWant && ok {
					t.Errorf("request.%s = %v, 期望缺省(omitempty)", k, got)
				}
			}
			// 说话人分离生效条件：language 留空则 audio.language 必须缺席。
			if tt.req.Language == "" {
				if l, ok := gotAudio["language"]; ok {
					t.Errorf("audio.language = %v, 期望缺省（enable_speaker_info 生效条件）", l)
				}
			}
		})
	}
}

// TestUtteranceToSegments 映射函数：Text/StartMS/EndMS 恒拷贝；speaker_id 非空才携带 Speaker，
// 序列化契约 speaker 仅非空出现（空=未启用说话人分离，前端/Task 3 依赖此形状）。
// 说话人有两个来源：顶层 speaker_id（兜底）与 utterances[].additions.speaker_id（真机实测主来源）。
func TestUtteranceToSegments(t *testing.T) {
	us := []sauc.UtteranceLike{
		{Definite: true, StartTime: 0, EndTime: 2100, Text: "我们下周三下午三点开会。",
			Additions: map[string]any{"source": "stream", "speaker_id": "0"}},
		{Definite: true, StartTime: 2200, EndTime: 3600, Text: "好的我记一下。",
			Additions: map[string]any{"source": "stream", "speaker_id": "1"}},
		{Definite: true, StartTime: 3700, EndTime: 4200, Text: "无说话人信息。"},
		{Definite: true, StartTime: 4300, EndTime: 4800, Text: "顶层兜底。", SpeakerID: "2"},
		{Definite: true, StartTime: 4900, EndTime: 5400, Text: "顶层优先于additions。", SpeakerID: "3",
			Additions: map[string]any{"speaker_id": "9"}},
	}
	segs := utteranceToSegments(us)
	if len(segs) != len(us) {
		t.Fatalf("segments 共 %d 句, 期望 %d", len(segs), len(us))
	}
	if segs[0].Text != "我们下周三下午三点开会。" || segs[0].StartMS != 0 || segs[0].EndMS != 2100 {
		t.Errorf("Segments[0] = %+v", segs[0])
	}
	if segs[0].Speaker != "0" || segs[1].Speaker != "1" {
		t.Errorf("additions.speaker_id 未映射: %q / %q", segs[0].Speaker, segs[1].Speaker)
	}
	if segs[2].Speaker != "" {
		t.Errorf("无 speaker_id 的分句 Speaker 应为空, got %q", segs[2].Speaker)
	}
	if segs[3].Speaker != "2" {
		t.Errorf("顶层 speaker_id 兜底未生效: %q", segs[3].Speaker)
	}
	if segs[4].Speaker != "3" {
		t.Errorf("顶层 speaker_id 应优先于 additions: %q", segs[4].Speaker)
	}
	raw, err := json.Marshal(segs)
	if err != nil {
		t.Fatalf("序列化 segments 失败: %v", err)
	}
	if strings.Contains(string(raw), `"speaker":""`) {
		t.Errorf("空 speaker 不应序列化: %s", raw)
	}
	if !strings.Contains(string(raw), `"speaker":"0"`) {
		t.Errorf("非空 speaker 应序列化为 speaker 键: %s", raw)
	}
}

// TestASRRecognizeNostreamSpeakerFromAdditions 端到端回归：真机实测响应形状
// （说话人在 utterances[].additions.speaker_id，非顶层字段）经 mock 全链路后 segments 携带 Speaker。
func TestASRRecognizeNostreamSpeakerFromAdditions(t *testing.T) {
	finalPayloadJSON := `{"audio_info":{"duration":5476},"result":{"additions":{"log_id":"smoke"},` +
		`"text":"我们下周三下午3点开会。好的，我记下，到时候提醒你。",` +
		`"utterances":[` +
		`{"additions":{"fixed_prefix_result":"","source":"stream","speaker_id":"0"},"definite":true,` +
		`"end_time":2360,"start_time":200,"text":"我们下周三下午3点开会。"},` +
		`{"additions":{"fixed_prefix_result":"","source":"stream","speaker_id":"1"},"definite":true,` +
		`"end_time":5360,"start_time":3080,"text":"好的，我记下，到时候提醒你。"}]}}`
	wsURL := newMockASRServer(t, nil, func(t *testing.T, conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil { // 消费 full request
			t.Errorf("读 full request 失败: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverAckFrame()); err != nil {
			t.Errorf("写 ack 失败: %v", err)
			return
		}
		for { // 排空音频分片直到最后一包（负 seq）
			if _, frame, err := conn.ReadMessage(); err != nil || frame[1]&0x0f&0x02 != 0 {
				break
			}
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, serverLastResponseFrame(finalPayloadJSON)); err != nil {
			t.Errorf("写最终响应失败: %v", err)
		}
		_, _, _ = conn.ReadMessage() // 等待客户端关闭
	})

	client := NewASRClientWithURL(SpeechCred{APIKey: "key-1"}, wsURL)
	resp, err := client.Recognize(context.Background(), ASRNostreamReq{Audio: []byte("audio"), Format: "mp3", SpeakerInfo: true})
	if err != nil {
		t.Fatalf("Recognize() err = %v", err)
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("Segments 共 %d 句, 期望 2", len(resp.Segments))
	}
	if resp.Segments[0].Speaker != "0" || resp.Segments[1].Speaker != "1" {
		t.Errorf("Speaker = %q / %q, 期望 %q / %q", resp.Segments[0].Speaker, resp.Segments[1].Speaker, "0", "1")
	}
}

// TestSmokeNostreamSpeaker 真机冒烟（录音笔记决策门）：需本机 ~/.voxbox/config.yaml 配置
// 火山语音凭据，无凭据（或无 ffmpeg 且无缓存样本）自动跳过，不进 CI。
// 双人样本用火山 TTS 两个音色各合成一句含时间语义的中文短句后 ffmpeg concat 拼接（缓存 /tmp 复用）。
// 说话人分离分两次尝试：先只发 enable_speaker_info+show_utterances（language 留空，官方生效条件）；
// 返回无 speaker_id 再补 ssd_version=200；两次均无 → 决策门判改道 AUC（由控制者裁定，此处显式跳过并留证据日志）。
func TestSmokeNostreamSpeaker(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("读取本机配置失败，跳过真机冒烟: %v", err)
	}
	cred := SpeechCred{AppID: cfg.Volc.Speech.AppID, AccessToken: cfg.Volc.Speech.AccessToken, APIKey: cfg.Volc.Speech.APIKey}
	if cred.APIKey == "" && (cred.AppID == "" || cred.AccessToken == "") {
		t.Skip("未配置火山语音凭据，跳过真机冒烟")
	}
	audio, err := smokeDuoSample(t, cred)
	if err != nil {
		t.Skipf("生成双人样本失败，跳过真机冒烟: %v", err)
	}

	attempts := []struct {
		name string
		req  ASRNostreamReq
	}{
		{"第一次: enable_speaker_info+show_utterances(language 留空)", ASRNostreamReq{Audio: audio, Format: "wav", SpeakerInfo: true}},
		{"第二次: 补 ssd_version=200", ASRNostreamReq{Audio: audio, Format: "wav", SpeakerInfo: true, SSDVersion: "200"}},
	}
	for i, a := range attempts {
		resp, err := NewASRClient(cred).Recognize(context.Background(), a.req)
		if err != nil {
			t.Fatalf("%s 失败: %v", a.name, err)
		}
		speakers := map[string]int{}
		for _, s := range resp.Segments {
			if s.Speaker != "" {
				speakers[s.Speaker]++
			}
		}
		t.Logf("%s: code=0 text=%q duration_ms=%d segments=%d speakers=%v", a.name, resp.Text, resp.DurationMS, len(resp.Segments), speakers)
		if len(resp.Segments) == 0 {
			t.Fatalf("%s: segments 为空", a.name)
		}
		if len(speakers) > 0 {
			if i > 0 {
				t.Logf("决策门：第二次尝试生效（补 ssd_version=200），分离出 %d 个说话人", len(speakers))
			} else {
				t.Logf("决策门：第一次尝试生效（仅 enable_speaker_info），分离出 %d 个说话人", len(speakers))
			}
			return // 验收线 ≥1 个非空 Speaker；实际分离人数记录在案
		}
		t.Logf("%s: 未返回任何 speaker_id", a.name)
	}
	t.Skipf("决策门不通过：两次尝试均未返回 speaker_id（请求参数与响应要点见上方日志），按简报判改道 AUC，待控制者裁定")
}

// smokeDuoSample 双人冒烟样本：火山 TTS 两音色各合成一句 wav，ffmpeg concat 拼接；
// 结果缓存 /tmp/voxbox-duo-smoke.wav 复用（省 TTS 配额，样本不入库）。
func smokeDuoSample(t *testing.T, cred SpeechCred) ([]byte, error) {
	t.Helper()
	const cache = "/tmp/voxbox-duo-smoke.wav"
	if b, err := os.ReadFile(cache); err == nil && len(b) > 44 {
		return b, nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("本机无 ffmpeg，无法拼接双人样本")
	}
	clips := [][2]string{
		{"zh_female_cancan_mars_bigtts", "我们下周三下午三点开会。"},
		{"zh_male_lengkugege_emo_v2_mars_bigtts", "好的我记一下，到时候提醒你。"},
	}
	var paths []string
	for i, c := range clips {
		resp, err := NewTTSClient(cred).Synthesize(context.Background(),
			TTSSynthesizeReq{Text: c[1], VoiceType: c[0], Format: "wav"})
		if err != nil {
			return nil, fmt.Errorf("TTS 合成样本 %d(%s) 失败: %w", i, c[0], err)
		}
		p := filepath.Join(t.TempDir(), fmt.Sprintf("clip%d.wav", i))
		if err := os.WriteFile(p, resp.Audio, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	cmd := exec.Command("ffmpeg", "-y", "-i", paths[0], "-i", paths[1], "-filter_complex", "concat=n=2:v=0:a=1", cache)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg 拼接失败: %v: %s", err, out)
	}
	return os.ReadFile(cache)
}
