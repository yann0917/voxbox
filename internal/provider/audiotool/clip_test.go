package audiotool

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

func TestBuildClipArgs(t *testing.T) {
	// 基础：start=61.5 end=91.5 fi=1 fo=0.5 loud=0 mp3
	p := clipParams{Start: 61.5, End: 91.5, FadeIn: 1, FadeOut: 0.5, Loudness: 0, Format: "mp3"}
	args, err := buildClipArgs(p, 30)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	// 滤镜串精确锚定：atrim 截取 + asetpts 归零 + 淡入（st=0）+ 淡出
	// （st=span−fo'=30−0.5=29.5，裁定 3 公式；计划示例串「st=29」为算术笔误——
	// 其自身钳制用例 选区4s/fo=3 → st=4−2=2 用的正是同一公式）
	if !strings.Contains(joined, "[0:a]atrim=start=61.5:end=91.5,asetpts=PTS-STARTPTS,"+
		"afade=t=in:st=0:d=1,afade=t=out:st=29.5:d=0.5[a]") {
		t.Errorf("滤镜串错误: %s", joined)
	}
	if !strings.Contains(joined, "-map [a]") {
		t.Errorf("loud=0 应 -map [a]: %s", joined)
	}
	if strings.Contains(joined, "loudnorm") {
		t.Errorf("loud=0 不应出现 loudnorm: %s", joined)
	}
	if !strings.Contains(joined, "-ar 44100 -c:a libmp3lame -b:a 128k") {
		t.Errorf("mp3 编码错误: %s", joined)
	}

	// loud=-14 → 尾接 loudnorm 段且 -map [out]
	p2 := p
	p2.Loudness = -14
	args2, err := buildClipArgs(p2, 30)
	if err != nil {
		t.Fatal(err)
	}
	j2 := strings.Join(args2, " ")
	if !strings.Contains(j2, ";[a]loudnorm=I=-14:TP=-1.5:LRA=11[out]") || !strings.Contains(j2, "-map [out]") {
		t.Errorf("loud=-14 应尾接 loudnorm 并 -map [out]: %s", j2)
	}

	// m4r → -ar 44100 + -f ipod + aac 128k；m4a → 无 -f ipod 但同样归一 44.1k + aac 128k
	p3 := p
	p3.Format = "m4r"
	j3 := strings.Join(mustClipArgs(t, p3, 30), " ")
	if !strings.Contains(j3, "-ar 44100 -f ipod -c:a aac -b:a 128k") {
		t.Errorf("m4r 编码错误: %s", j3)
	}
	p4 := p
	p4.Format = "m4a"
	j4 := strings.Join(mustClipArgs(t, p4, 30), " ")
	if strings.Contains(j4, "ipod") || !strings.Contains(j4, "-ar 44100 -c:a aac -b:a 128k") {
		t.Errorf("m4a 编码错误: %s", j4)
	}

	// fade 超半钳制：选区 4s、fi=3/fo=3 → 各钳 2（淡出 st=span−2=2:d=2）
	p5 := clipParams{Start: 0, End: 4, FadeIn: 3, FadeOut: 3, Loudness: 0, Format: "mp3"}
	j5 := strings.Join(mustClipArgs(t, p5, 4), " ")
	if !strings.Contains(j5, "afade=t=in:st=0:d=2,afade=t=out:st=2:d=2") {
		t.Errorf("fade 超半应钳到选区一半: %s", j5)
	}

	// fade 0（与负值）= 省略对应 afade：链上不出现任何 afade
	p6 := clipParams{Start: 10, End: 30, FadeIn: 0, FadeOut: -1, Loudness: 0, Format: "mp3"}
	j6 := strings.Join(mustClipArgs(t, p6, 20), " ")
	if !strings.Contains(j6, "[0:a]atrim=start=10:end=30,asetpts=PTS-STARTPTS[a]") ||
		strings.Contains(j6, "afade") {
		t.Errorf("fade 0/负 应整体省略 afade: %s", j6)
	}

	// 非法：end<=start；跨度 >600；起点为负
	if _, err := buildClipArgs(clipParams{Start: 30, End: 30, Format: "mp3"}, 0); err == nil {
		t.Error("end==start 应报错")
	}
	if _, err := buildClipArgs(clipParams{Start: 10, End: 5, Format: "mp3"}, -5); err == nil {
		t.Error("end<start 应报错")
	}
	if _, err := buildClipArgs(clipParams{Start: 0, End: 601, Format: "mp3"}, 601); err == nil {
		t.Error("跨度 >600 应报错")
	}
	if _, err := buildClipArgs(clipParams{Start: -1, End: 10, Format: "mp3"}, 11); err == nil {
		t.Error("起点为负应报错")
	}
}

func mustClipArgs(t *testing.T, p clipParams, total float64) []string {
	t.Helper()
	args, err := buildClipArgs(p, total)
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestClipName(t *testing.T) {
	// int 截断：61.5/91.5 → 61s/91s
	if got := clipArtName("/x/稻香.mp3", 61.5, 91.5, "mp3"); got != "稻香_clip_61s-91s.mp3" {
		t.Errorf("clipArtName = %q, want 稻香_clip_61s-91s.mp3", got)
	}
	if got := clipArtName("/x/稻香.mp3", 61.9, 91.9, "m4r"); got != "稻香_clip_61s-91s.m4r" {
		t.Errorf("clipArtName int 截断 = %q, want 稻香_clip_61s-91s.m4r", got)
	}
}

// TestParseClipParams 参数域钳制：loudness ∈ loudnorm 合法域 [-70,-5]（0=关不钳），
// format 仅认 m4a/m4r（其余一律 mp3），字符串数值走 paramFloat 通道。
func TestParseClipParams(t *testing.T) {
	// 缺省 = 默认档：fade_in 1 / fade_out 0.5 / loudness -14 / mp3
	p := parseClipParams(map[string]any{"start": float64(10), "end": float64(40)})
	if p.Start != 10 || p.End != 40 || p.FadeIn != 1 || p.FadeOut != 0.5 ||
		p.Loudness != -14 || p.Format != "mp3" {
		t.Errorf("缺省参数错误: %+v", p)
	}
	// loudness 0=关；域外钳 [-70,-5]
	if p := parseClipParams(map[string]any{"loudness": float64(0)}); p.Loudness != 0 {
		t.Errorf("loudness 0=关应保持 0: %+v", p)
	}
	if p := parseClipParams(map[string]any{"loudness": float64(-99)}); p.Loudness != -70 {
		t.Errorf("loudness 下界钳制错误: %+v", p)
	}
	if p := parseClipParams(map[string]any{"loudness": float64(-1)}); p.Loudness != -5 {
		t.Errorf("loudness 上界钳制错误: %+v", p)
	}
	// format 仅认 m4a/m4r
	if p := parseClipParams(map[string]any{"format": "m4r"}); p.Format != "m4r" {
		t.Errorf("format m4r 应保留: %+v", p)
	}
	if p := parseClipParams(map[string]any{"format": "flac"}); p.Format != "mp3" {
		t.Errorf("format 非 m4a/m4r 应归 mp3: %+v", p)
	}
	// 字符串数值（CLI/MCP 通道）
	p = parseClipParams(map[string]any{"start": "10.5", "end": "30.5", "fade_in": "2", "fade_out": "0"})
	if p.Start != 10.5 || p.End != 30.5 || p.FadeIn != 2 || p.FadeOut != 0 {
		t.Errorf("字符串数值解析错误: %+v", p)
	}
}

// TestClipToolRun Run 层 e2e（无 ffmpeg 自动跳过）：200s 样本切 10-30s，
// 进度骨架 5→8→100，产物落盘且时长≈选区，Summary 承载选区与淡化参数。
func TestClipToolRun(t *testing.T) {
	wav := genHookWav(t) // 200s 恒定正弦
	var probs []int
	out, err := (&clipTool{baseTool{outDir: t.TempDir()}}).Run(context.Background(),
		provider.TaskInput{Params: map[string]any{"start": float64(10), "end": float64(30)},
			Files: map[string]string{"audio": wav}},
		func(p int, _ string, _ map[string]any) { probs = append(probs, p) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 进度骨架：5 探测 → 8 切片（8-95 桥接段条数随 ffmpeg 实际进度不定）→ 100
	if len(probs) < 3 || probs[0] != 5 || probs[1] != 8 || probs[len(probs)-1] != 100 {
		t.Fatalf("进度序列异常: %v", probs)
	}

	if len(out.Artifacts) != 1 {
		t.Fatalf("产物数 = %d, want 1", len(out.Artifacts))
	}
	art := out.Artifacts[0]
	if art.Kind != "audio" || art.Format != "mp3" || art.Size <= 0 {
		t.Errorf("产物头错误: %+v", art)
	}
	if got := filepath.Base(art.Path); got != "sample_song_clip_10s-30s.mp3" {
		t.Errorf("产物名 = %q, want sample_song_clip_10s-30s.mp3", got)
	}
	if _, err := os.Stat(art.Path); err != nil {
		t.Errorf("产物未落盘: %v", err)
	}
	if d := float64(art.DurationMS); math.Abs(d-20000) > 1000 {
		t.Errorf("产物时长 = %gms, want ≈20000（选区 20s）", d)
	}

	if out.Summary["start"].(float64) != 10 || out.Summary["end"].(float64) != 30 {
		t.Errorf("Summary 选区错误: %v", out.Summary)
	}
	if out.Summary["fade_in"].(float64) != 1 || out.Summary["fade_out"].(float64) != 0.5 {
		t.Errorf("Summary 淡化错误: %v", out.Summary)
	}
	if out.Summary["loudness"].(float64) != -14 || out.Summary["format"].(string) != "mp3" {
		t.Errorf("Summary 响度/格式错误: %v", out.Summary)
	}
	if d := out.Summary["duration_sec"].(float64); d != 20 {
		t.Errorf("Summary duration_sec = %g, want 20（选区跨度）", d)
	}

	// m4r 冒烟：ipod muxer 落 .m4r；双 fade 0 → 真实 ffmpeg 下整链省略 afade
	out2, err := (&clipTool{baseTool{outDir: t.TempDir()}}).Run(context.Background(),
		provider.TaskInput{Params: map[string]any{"start": float64(0), "end": float64(5),
			"fade_in": float64(0), "fade_out": float64(0), "format": "m4r"},
			Files: map[string]string{"audio": wav}},
		func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("m4r Run: %v", err)
	}
	art2 := out2.Artifacts[0]
	if art2.Format != "m4r" || !strings.HasSuffix(art2.Path, ".m4r") || art2.Size <= 0 {
		t.Errorf("m4r 产物错误: %+v", art2)
	}
	// 采样率锁 44.1k（loudnorm 走默认 -14 档内部上采 192k，靠 -ar 44100 归一）
	if info, err := probeAudio(context.Background(), art2.Path); err != nil {
		t.Fatalf("probe m4r: %v", err)
	} else if info.SampleRate != 44100 {
		t.Errorf("m4r 采样率 = %d, want 44100", info.SampleRate)
	}
}

func TestClipToolRunErrors(t *testing.T) {
	if _, err := (&clipTool{}).Run(context.Background(),
		provider.TaskInput{}, func(int, string, map[string]any) {}); err == nil {
		t.Fatal("缺输入应报错")
	}
	// 终点超出音频时长（200s 样本）：容差 0.01s
	wav := genHookWav(t)
	_, err := (&clipTool{}).Run(context.Background(),
		provider.TaskInput{Params: map[string]any{"start": float64(0), "end": float64(500)},
			Files: map[string]string{"audio": wav}},
		func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "超出音频时长") {
		t.Fatalf("终点超时长应报错, got %v", err)
	}
}
