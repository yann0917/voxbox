package local

import (
	"context"
	"testing"

	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
)

// TestTTSToolAppliesPronunciation 发音词典接线：工具在合成前把命中词条替换为读音写法
// （qwen3 语言参数为全称 Chinese，别名折叠后仍应命中全语言词条）。
func TestTTSToolAppliesPronunciation(t *testing.T) {
	pronunciation.Init(t.TempDir())
	if _, err := pronunciation.Default().Add("重庆", "chóngqìng", "*", true); err != nil {
		t.Fatal(err)
	}

	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "qwen3-tts-customvoice-q8", "customvoice-q8_0.gguf")

	var got localruntime.SynthRequest
	tool := newTTSTool(dataDir, m, nil, nil)
	tool.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return fakeOutWav(t, dataDir), nil
	}

	if _, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"text": "重庆火锅", "model": "qwen3-tts-customvoice-q8",
			"mode": "preset", "speaker": "Vivian", "language": "Chinese",
		},
	}, func(int, string, map[string]any) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Text != "chóngqìng火锅" {
		t.Fatalf("合成文本未应用发音词典: %q, want %q", got.Text, "chóngqìng火锅")
	}
}
