package volcengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

func TestAggregateSubtitleSegments(t *testing.T) {
	words := []TTSStreamWord{
		{Word: "你", StartMS: 0, EndMS: 200},
		{Word: "好，", StartMS: 200, EndMS: 500}, // 逗号不是句末标点，不切句
		{Word: "世", StartMS: 500, EndMS: 700},
		{Word: "界。", StartMS: 700, EndMS: 1000},
		{Word: "好", StartMS: 1000, EndMS: 1200},
		{Word: "！", StartMS: 1200, EndMS: 1400},
	}
	segs := aggregateSubtitleSegments(words)
	if len(segs) != 2 {
		t.Fatalf("segs = %+v, want 2 句", segs)
	}
	if segs[0].Text != "你好，世界。" || segs[0].StartMS != 0 || segs[0].EndMS != 1000 {
		t.Errorf("segs[0] = %+v", segs[0])
	}
	if segs[1].Text != "好！" || segs[1].StartMS != 1000 || segs[1].EndMS != 1400 {
		t.Errorf("segs[1] = %+v", segs[1])
	}

	// 无句末标点：全部聚合为一句
	tail := aggregateSubtitleSegments([]TTSStreamWord{
		{Word: "没", StartMS: 0, EndMS: 100}, {Word: "标点", StartMS: 100, EndMS: 300},
	})
	if len(tail) != 1 || tail[0].Text != "没标点" || tail[0].EndMS != 300 {
		t.Errorf("tail = %+v", tail)
	}

	// 空输入
	if segs = aggregateSubtitleSegments(nil); len(segs) != 0 {
		t.Errorf("空输入应返回空: %+v", segs)
	}
}

func TestTTSStreamToolRunParamErrors(t *testing.T) {
	tool := NewTTSStreamTool(SpeechCred{APIKey: "k"}, t.TempDir())
	cases := []struct {
		name    string
		params  map[string]any
		wantErr string
	}{
		{"缺文本", map[string]any{}, "缺少必填参数"},
		{"格式不支持", map[string]any{"text": "你好", "format": "flac"}, "仅支持"},
		{"采样率非法", map[string]any{"text": "你好", "sample_rate": "12345"}, "不支持的采样率"},
		{"wav指定比特率", map[string]any{"text": "你好", "format": "wav", "bit_rate": "160000"}, "不支持指定比特率"},
		{"pcm指定比特率", map[string]any{"text": "你好", "format": "pcm", "bit_rate": "64000"}, "不支持指定比特率"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tool.Run(context.Background(), provider.TaskInput{Params: c.params}, func(int, string, map[string]any) {})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want 含 %q", err, c.wantErr)
			}
		})
	}
}

func TestTTSStreamToolRunNoCred(t *testing.T) {
	tool := NewTTSStreamTool(SpeechCred{}, t.TempDir())
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "你好"}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "凭证") {
		t.Fatalf("err = %v, want 凭证错误", err)
	}
}

// TestTTSStreamToolRunSuccess 全流程：三分片流 + 字级时间戳 → 音频 + SRT。
func TestTTSStreamToolRunSuccess(t *testing.T) {
	words1 := `[{"word":"你","startTime":0,"endTime":0.2},{"word":"好，","startTime":0.2,"endTime":0.5}]`
	words2 := `[{"word":"世界。","startTime":0.5,"endTime":1.0}]`
	m := newTTSStreamMockServer(t, func() []string {
		return []string{
			`{"code":0,"data":"` + base64.StdEncoding.EncodeToString([]byte("PART1")) + `"}`,
			`{"code":0,"data":"` + base64.StdEncoding.EncodeToString([]byte("PART2")) + `","sentence":{"words":` + words1 + `}}`,
			`{"code":20000000,"message":"success","sentence":{"text":"你好，世界。","words":` + words2 + `},"usage":{"text_words":5}}`,
		}
	})

	outDir := t.TempDir()
	tool := NewTTSStreamToolWithBaseURL(SpeechCred{APIKey: "key-1"}, outDir, m.srv.URL)
	var lastProgress int
	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "你好，世界。", "voice": "zh_female_vv_uranus_bigtts",
		"format": "mp3", "subtitle": true,
	}}, func(p int, _ string, _ map[string]any) { lastProgress = p })
	if err != nil {
		t.Fatal(err)
	}

	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2（audio+subtitle）", len(out.Artifacts))
	}
	a, srt := out.Artifacts[0], out.Artifacts[1]
	if a.Kind != "audio" || a.Size != int64(len("PART1PART2")) || a.DurationMS != 1000 {
		t.Errorf("audio artifact = %+v", a)
	}
	if srt.Kind != "subtitle" || !strings.HasSuffix(srt.Path, ".srt") {
		t.Errorf("srt artifact = %+v", srt)
	}

	audioBytes, err := os.ReadFile(filepath.Join(outDir, a.Path))
	if err != nil || string(audioBytes) != "PART1PART2" {
		t.Fatalf("音频落盘失败: %v %q", err, audioBytes)
	}
	srtBytes, err := os.ReadFile(filepath.Join(outDir, srt.Path))
	if err != nil {
		t.Fatal(err)
	}
	srtText := string(srtBytes)
	if !strings.Contains(srtText, "00:00:00,000 --> 00:00:01,000") ||
		!strings.Contains(srtText, "你好，世界。") {
		t.Errorf("SRT 内容不符: %s", srtText)
	}

	raw, _ := json.Marshal(out.Summary)
	for _, want := range []string{`"char_count":6`, `"billed_chars":5`, `"chunks":2`, `"duration_ms":1000`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("summary 缺少 %s: %s", want, raw)
		}
	}
	// 分片进度已上报且最终到 95 保存
	if lastProgress != 95 {
		t.Errorf("最后进度 = %d, want 95", lastProgress)
	}
}

// TestTTSStreamToolRunOutRedirect _out 重定向：音频与 SRT 跟随。
func TestTTSStreamToolRunOutRedirect(t *testing.T) {
	m := newTTSStreamMockServer(t, func() []string {
		return []string{
			`{"code":0,"data":"` + base64.StdEncoding.EncodeToString([]byte("A")) + `"}`,
			`{"code":20000000,"message":"ok","sentence":{"words":[{"word":"好。","startTime":0,"endTime":0.4}]}}`,
		}
	})
	tool := NewTTSStreamToolWithBaseURL(SpeechCred{APIKey: "k"}, t.TempDir(), m.srv.URL)
	outPath := filepath.Join(t.TempDir(), "stream.mp3")
	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "好。", "subtitle": true, "_out": outPath,
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if out.Artifacts[0].Path != outPath {
		t.Errorf("audio path = %q, want %q", out.Artifacts[0].Path, outPath)
	}
	srtPath := strings.TrimSuffix(outPath, ".mp3") + ".srt"
	if out.Artifacts[1].Path != srtPath {
		t.Errorf("srt path = %q, want %q", out.Artifacts[1].Path, srtPath)
	}
	if _, err := os.Stat(srtPath); err != nil {
		t.Fatal(err)
	}
}

// TestTTSStreamToolRunNoAudio 流正常结束但零音频 → 报错不落盘。
func TestTTSStreamToolRunNoAudio(t *testing.T) {
	m := newTTSStreamMockServer(t, func() []string {
		return []string{`{"code":20000000,"message":"ok"}`}
	})
	tool := NewTTSStreamToolWithBaseURL(SpeechCred{APIKey: "k"}, t.TempDir(), m.srv.URL)
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "你好"}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "未返回音频") {
		t.Fatalf("err = %v", err)
	}
}
