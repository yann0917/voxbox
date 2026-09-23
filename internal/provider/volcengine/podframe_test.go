package volcengine

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"testing"
)

func u32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

func TestBuildStartSession(t *testing.T) {
	frame := BuildStartSession("sess-1", []byte(`{"a":1}`))
	if frame[0] != 0x11 || frame[1] != 0x94 || frame[2] != 0x10 || frame[3] != 0x00 {
		t.Fatalf("header = % x", frame[:4])
	}
	if got := u32(frame[4:8]); got != 100 {
		t.Errorf("event = %d", got)
	}
	// session_id: len + bytes
	if got := u32(frame[8:12]); got != 6 || string(frame[12:18]) != "sess-1" {
		t.Errorf("session_id = %d %q", got, frame[12:18])
	}
	// 简报原文断言 got != 6，但 `{"a":1}` 实为 7 字节；按权威帧格式 payload_len == len(payload) 修正为 7。
	if got := u32(frame[18:22]); got != 7 || string(frame[22:]) != `{"a":1}` {
		t.Errorf("payload = %d %q", got, frame[22:])
	}
}

func TestParseAudioFrame(t *testing.T) {
	audio := []byte{0xFF, 0xFB, 0x90, 0x00} // mp3 片段样例
	var b []byte
	b = append(b, 0x11, 0xB4, 0x00, 0x00) // 音频帧 raw 无压缩
	b = binary.BigEndian.AppendUint32(b, 361)
	b = binary.BigEndian.AppendUint32(b, 6)
	b = append(b, "sess-1"...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(audio)))
	b = append(b, audio...)
	f, err := ParsePodFrame(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Event != 361 || !bytes.Equal(f.Audio, audio) || f.SessionID != "sess-1" {
		t.Fatalf("f = %+v", f)
	}
}

func TestParseTextAndErrorFrames(t *testing.T) {
	// 文本帧：event 360 + session_id + JSON payload
	var b []byte
	b = append(b, 0x11, 0x94, 0x10, 0x00)
	b = binary.BigEndian.AppendUint32(b, 360)
	b = binary.BigEndian.AppendUint32(b, 6)
	b = append(b, "sess-1"...)
	// 简报原文写长度 7，但 `{"r_id":1}` 实为 10 字节；按权威帧格式 payload_len == len(payload) 修正为 10。
	b = binary.BigEndian.AppendUint32(b, 10)
	b = append(b, `{"r_id":1}`...)
	f, err := ParsePodFrame(b)
	if err != nil || f.Event != 360 || string(f.Payload) != `{"r_id":1}` {
		t.Fatalf("f = %+v err = %v", f, err)
	}
	// 错误帧：0xF0 + code + len + msg
	var e []byte
	e = append(e, 0x11, 0xF0, 0x10, 0x00)
	e = binary.BigEndian.AppendUint32(e, 45000001)
	msg := []byte(`{"message":"invalid param"}`)
	e = binary.BigEndian.AppendUint32(e, uint32(len(msg)))
	e = append(e, msg...)
	ef, err := ParsePodFrame(e)
	if err != nil || ef.ErrCode != 45000001 || ef.ErrMsg != "invalid param" {
		t.Fatalf("ef = %+v err = %v", ef, err)
	}
	if _, err := ParsePodFrame([]byte{0x11, 0x94}); err == nil {
		t.Error("truncated should error")
	}
}

func TestBuildFinishConnection(t *testing.T) {
	frame := BuildFinishConnection("s")
	if frame[1] != 0x94 || u32(frame[4:8]) != 2 || u32(frame[8:12]) != 1 {
		t.Fatalf("frame = % x", frame)
	}
}

// 补充用例：文本帧 compression 位为 1 时 gzip 解压（示例帧均无压缩，防御性支持）。
func TestParseTextFrameGzipPayload(t *testing.T) {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write([]byte(`{"gz":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var b []byte
	b = append(b, 0x11, 0x94, 0x11, 0x00) // JSON + gzip
	b = binary.BigEndian.AppendUint32(b, 363)
	b = binary.BigEndian.AppendUint32(b, 3)
	b = append(b, "abc"...)
	b = binary.BigEndian.AppendUint32(b, uint32(gz.Len()))
	b = append(b, gz.Bytes()...)
	f, err := ParsePodFrame(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Event != 363 || f.SessionID != "abc" || string(f.Payload) != `{"gz":true}` {
		t.Fatalf("f = %+v", f)
	}
}

// 补充用例：payload 长度字段超过实际帧体时报错。
func TestParsePayloadLengthOverflow(t *testing.T) {
	var b []byte
	b = append(b, 0x11, 0x94, 0x10, 0x00)
	b = binary.BigEndian.AppendUint32(b, 360)
	b = binary.BigEndian.AppendUint32(b, 3)
	b = append(b, "abc"...)
	b = binary.BigEndian.AppendUint32(b, 100) // 谎称 100 字节 payload，实际为 0
	b = append(b, "trailing"...)
	if _, err := ParsePodFrame(b); err == nil {
		t.Fatal("payload length overflow should error")
	}
}
