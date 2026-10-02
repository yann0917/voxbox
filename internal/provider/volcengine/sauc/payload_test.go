package sauc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestMetaSpeakerFields(t *testing.T) {
	b, err := json.Marshal(RequestMeta{ModelName: "bigmodel", EnableSpeakerInfo: true, EnableLID: true, EnableEmotion: true, SSDVersion: "20240904"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"enable_speaker_info":true`, `"enable_lid":true`, `"enable_emotion_detection":true`, `"model_name":"bigmodel"`, `"ssd_version":"20240904"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}

// TestRequestMetaAsyncFields 实时字幕（bigmodel_async 双向流式）请求字段核对：
// enable_nonstream（二遍识别）、enable_punc、enable_itn、enable_lid、show_utterances 与
// corpus.context（热词）序列化形状。字段在 payload.go 既有定义，本测试锁定不回退。
func TestRequestMetaAsyncFields(t *testing.T) {
	b, err := json.Marshal(RequestMeta{
		ModelName:       "bigmodel",
		EnableITN:       true,
		EnablePUNC:      true,
		EnableLID:       true,
		EnableNonstream: true,
		ShowUtterances:  true,
		Corpus:          CorpusMeta{Context: "热词"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"model_name":"bigmodel"`, `"enable_itn":true`, `"enable_punc":true`, `"enable_lid":true`,
		`"enable_nonstream":true`, `"show_utterances":true`, `"context":"热词"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	// 布尔默认 false 时 enable_nonstream/show_utterances 仍序列化（无 omitempty，协议要求恒携带）。
	b0, err := json.Marshal(RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	s0 := string(b0)
	if !strings.Contains(s0, `"enable_nonstream":false`) || !strings.Contains(s0, `"show_utterances":false`) {
		t.Fatalf("默认 RequestMeta 应恒携带 enable_nonstream/show_utterances: %s", s0)
	}
}

func TestResponseSpeakerAndAdditions(t *testing.T) {
	const raw = `{"audio_info":{"duration":1234},"result":{"text":"你好","additions":{"language":"speech_mand"},"utterances":[{"definite":true,"start_time":0,"end_time":900,"text":"你好","speaker_id":"1","words":[]}]}}`
	var p AsrResponsePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Result.Utterances) != 1 || p.Result.Utterances[0].SpeakerID != "1" {
		t.Fatalf("speaker_id not parsed: %+v", p.Result.Utterances)
	}
	if p.Result.Additions["language"] != "speech_mand" {
		t.Fatalf("additions not parsed: %v", p.Result.Additions)
	}
}
