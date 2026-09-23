package volcengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMockServer(t *testing.T, status int, body map[string]any, gotHeaders *map[string]string, gotBody *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := map[string]string{"Authorization": r.Header.Get("Authorization")}
		*gotHeaders = h
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		*gotBody = m
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestSynthesizeSuccess(t *testing.T) {
	audio := []byte("FAKE_MP3_DATA")
	var headers map[string]string
	var body map[string]any
	srv := newMockServer(t, 200, map[string]any{
		"code": 3000, "message": "success",
		"data":     base64.StdEncoding.EncodeToString(audio),
		"addition": map[string]any{"duration": "8400"},
	}, &headers, &body)
	defer srv.Close()

	c := NewTTSClientWithBaseURL(SpeechCred{AppID: "app", AccessToken: "tok"}, srv.URL)
	resp, err := c.Synthesize(context.Background(), TTSSynthesizeReq{
		Text: "你好", VoiceType: "v1", Format: "mp3", SpeedRatio: 1.0, VolumeRatio: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Audio) != string(audio) || resp.DurationMS != 8400 {
		t.Fatalf("resp = %+v", resp)
	}
	if headers["Authorization"] != "Bearer;tok" {
		t.Errorf("auth header = %q", headers["Authorization"])
	}
	appMap := body["app"].(map[string]any)
	if appMap["appid"] != "app" || appMap["cluster"] != "volcano_tts" {
		t.Errorf("app = %v", appMap)
	}
}

func TestSynthesizeAuthError(t *testing.T) {
	var h map[string]string
	var b map[string]any
	srv := newMockServer(t, 200, map[string]any{
		"code": 3001, "message": "invalid token",
	}, &h, &b)
	defer srv.Close()
	c := NewTTSClientWithBaseURL(SpeechCred{AppID: "a", AccessToken: "bad"}, srv.URL)
	_, err := c.Synthesize(context.Background(), TTSSynthesizeReq{Text: "x", VoiceType: "v", Format: "mp3"})
	if err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("err = %v", err)
	}
}

func TestCredValidate(t *testing.T) {
	if err := (SpeechCred{}).Validate(); err == nil {
		t.Error("empty cred should fail")
	}
	if err := (SpeechCred{APIKey: "k"}).Validate(); err != nil {
		t.Errorf("api key only: %v", err)
	}
	if err := (SpeechCred{AppID: "a", AccessToken: "t"}).Validate(); err != nil {
		t.Errorf("appid+token: %v", err)
	}
}
