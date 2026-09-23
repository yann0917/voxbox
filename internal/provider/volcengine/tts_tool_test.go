package volcengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

func newTTSToolWithMock(t *testing.T, text string, segment int) (*TTSTool, *httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 3000, "message": "success",
			"data":     base64.StdEncoding.EncodeToString([]byte("MP3DATA")),
			"addition": map[string]any{"duration": "1000"},
		})
	}))
	t.Cleanup(srv.Close)
	cred := SpeechCred{AppID: "a", AccessToken: "t"}
	client := NewTTSClientWithBaseURL(cred, srv.URL)
	return &TTSTool{client: client, cred: cred, outDir: t.TempDir()}, srv, text
}

func TestTTSToolRun(t *testing.T) {
	tool, _, _ := newTTSToolWithMock(t, "", 0)
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": "你好世界", "voice": "v1", "format": "mp3",
			"speed_ratio": 1.0, "volume_ratio": 1.0},
	}, func(progress int, note string, detail map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "audio" {
		t.Fatalf("out = %+v", out)
	}
	if !strings.HasPrefix(out.Artifacts[0].Path, "tts/") {
		t.Errorf("artifact path = %s", out.Artifacts[0].Path)
	}
}

func TestTTSToolRunLongTextWrongFormat(t *testing.T) {
	tool, _, _ := newTTSToolWithMock(t, "", 0)
	long := strings.Repeat("测试句子。", 300) // 1800 字
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"text": long, "format": "wav"},
	}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "mp3") {
		t.Fatalf("err = %v", err)
	}
}

func TestTTSToolMissingText(t *testing.T) {
	tool, _, _ := newTTSToolWithMock(t, "", 0)
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{}}, nopReport)
	if err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("err = %v", err)
	}
}

func nopReport(progress int, note string, detail map[string]any) {}
