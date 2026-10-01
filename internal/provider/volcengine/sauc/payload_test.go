package sauc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestMetaSpeakerFields(t *testing.T) {
	b, err := json.Marshal(RequestMeta{ModelName: "bigmodel", EnableSpeakerInfo: true, EnableLID: true, SSDVersion: "20240904"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"enable_speaker_info":true`, `"enable_lid":true`, `"model_name":"bigmodel"`, `"ssd_version":"20240904"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
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
