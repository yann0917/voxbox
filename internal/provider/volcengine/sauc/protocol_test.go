package sauc

import (
	"strings"
	"testing"
)

func TestNewAuthHeaderFrom(t *testing.T) {
	h := NewAuthHeaderFrom("key-1", "", "", "volc.seedasr.sauc.duration")
	if h.Get("X-Api-Key") != "key-1" || h.Get("X-Api-Resource-Id") != "volc.seedasr.sauc.duration" {
		t.Errorf("h = %v", h)
	}
	if h.Get("X-Api-Request-Id") == "" {
		t.Error("missing request id")
	}
	h2 := NewAuthHeaderFrom("", "app-1", "tok-1", "r")
	if h2.Get("X-Api-App-Key") != "app-1" || h2.Get("X-Api-Access-Key") != "tok-1" || h2.Get("X-Api-Key") != "" {
		t.Errorf("legacy auth h = %v", h2)
	}
}

func TestRoundTripFrames(t *testing.T) {
	// NewFullClientRequest 产出的帧首字节应为 0x11（version 1 | header size 1）
	frame := NewFullClientRequest(AsrRequestPayload{})
	if frame[0] != 0x11 {
		t.Errorf("byte0 = %x", frame[0])
	}
	// NewAudioOnlyRequest(-1, ...) 最后一包：byte1 高 4 位=0010
	audio := NewAudioOnlyRequest(-1, []byte("x"))
	if audio[1]>>4 != 0x2 {
		t.Errorf("audio type nibble = %x", audio[1]>>4)
	}
	if !strings.Contains(string(GzipDecompress(GzipCompress([]byte("ok")))), "ok") {
		t.Error("gzip roundtrip")
	}
}
