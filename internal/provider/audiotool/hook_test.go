package audiotool

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

func genSR(t *testing.T, sec float64, f func(t float64) float64) []float64 {
	t.Helper()
	n := int(sec * 22050)
	out := make([]float64, n)
	for i := range out {
		out[i] = f(float64(i) / 22050)
	}
	return out
}

const amp = 0.25 // 避免削波

func TestHookCandidatesSingleLoud(t *testing.T) {
	// 180s：0-60 安静（0.01 本底），60-90 响（副歌），90-180 安静
	s := genSR(t, 180, func(t float64) float64 {
		if t >= 60 && t < 90 {
			return amp * math.Sin(2*math.Pi*220*t)
		}
		return 0.01 * math.Sin(2*math.Pi*110*t)
	})
	got := hookCandidates(s, 22050, 30, 3)
	if len(got) == 0 {
		t.Fatal("无候选")
	}
	if got[0].Start < 55 || got[0].Start > 65 {
		t.Errorf("top1 起点 = %.1f, want ∈ [55,65]（60-90 响段）", got[0].Start)
	}
	if got[0].End-got[0].Start != 30 {
		t.Errorf("选区长度 = %.1f, want 30", got[0].End-got[0].Start)
	}
	// score 契约（docs/mcp.md 与 MCP Description「评分 0-1」）：窗均值归一后
	// 域 [0,1]；真响段存在时 top1 能量分量近满 → 严格大于 0
	if got[0].Score <= 0 || got[0].Score > 1 {
		t.Errorf("top1 评分 = %.3f, want ∈ (0,1]", got[0].Score)
	}
}

func TestHookCandidatesTwoLoud(t *testing.T) {
	// 240s：两个响段 40-70 与 140-175 → top2 分别命中，间隔≥5s
	s := genSR(t, 240, func(t float64) float64 {
		loud := (t >= 40 && t < 70) || (t >= 140 && t < 175)
		if loud {
			return amp * math.Sin(2*math.Pi*220*t)
		}
		return 0.01 * math.Sin(2*math.Pi*110*t)
	})
	got := hookCandidates(s, 22050, 30, 3)
	if len(got) < 2 {
		t.Fatalf("候选不足 2: %+v", got)
	}
	mid := func(c hookCand) float64 { return (c.Start + c.End) / 2 }
	if !(mid(got[0]) >= 40 && mid(got[0]) <= 70) && !(mid(got[0]) >= 140 && mid(got[0]) <= 175) {
		t.Errorf("top1 中心 %.1f 不在任一响段", mid(got[0]))
	}
	if math.Abs(mid(got[0])-mid(got[1])) < 5+30 {
		t.Errorf("top2 与 top1 重叠或过近: %.1f vs %.1f", mid(got[0]), mid(got[1]))
	}
}

func TestHookCandidatesShortInput(t *testing.T) {
	s := genSR(t, 10, func(t float64) float64 { return 0.1 * math.Sin(2*math.Pi*220*t) })
	if got := hookCandidates(s, 22050, 30, 3); got != nil {
		t.Errorf("音频短于目标时长应返回 nil, got %+v", got)
	}
}

func TestHookCandidatesDegenerate(t *testing.T) {
	// 全静音：eMax=0，能量分量应取 0 而非 NaN，且输出确定（同分按时间先后）
	silent := genSR(t, 40, func(t float64) float64 { return 0 })
	got := hookCandidates(silent, 22050, 10, 2)
	if len(got) == 0 {
		t.Fatal("全静音输入（长度充足）应仍返回候选，分数为 0 而非 nil/NaN")
	}
	for _, c := range got {
		if math.IsNaN(c.Score) || math.IsNaN(c.Start) || math.IsNaN(c.End) {
			t.Fatalf("全静音输入不得产生 NaN: %+v", got)
		}
	}
	// 恒定幅度：rise_rate 全零（rMax=0），rise 分量应取 0 而非 NaN；结果按时间升序
	flat := genSR(t, 40, func(t float64) float64 { return 0.2 })
	got = hookCandidates(flat, 22050, 10, 2)
	if len(got) < 2 {
		t.Fatalf("恒定幅度输入应仍给出候选: %+v", got)
	}
	for _, c := range got {
		if math.IsNaN(c.Score) {
			t.Fatalf("恒定幅度输入不得产生 NaN: %+v", got)
		}
	}
	if got[0].Start >= got[1].Start {
		t.Errorf("候选应按时间升序: %.2f vs %.2f", got[0].Start, got[1].Start)
	}
	// count=0 直接 nil
	if got := hookCandidates(flat, 22050, 10, 0); got != nil {
		t.Errorf("count=0 应返回 nil, got %+v", got)
	}
}

// genHookWav 生成 200s 恒定正弦 WAV（Run 层 e2e 样本，ffmpeg 缺失则跳过）。
// 恒定幅度下各窗同分，候选确定（同分按时间先后），断言不依赖具体窗口位置。
func genHookWav(t *testing.T) string {
	t.Helper()
	if err := lookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过")
	}
	if err := lookPath("ffprobe"); err != nil {
		t.Skip("本机无 ffprobe，跳过")
	}
	wav := filepath.Join(t.TempDir(), "sample_song.wav")
	if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=220:duration=200", wav).CombinedOutput(); err != nil {
		t.Fatalf("生成样本 WAV 失败: %v: %s", err, out)
	}
	return wav
}

// runHook 驱动 hookTool.Run 并记录进度上报序列。
func runHook(t *testing.T, params map[string]any) (provider.TaskOutput, []int) {
	t.Helper()
	var probs []int
	report := func(p int, _ string, _ map[string]any) { probs = append(probs, p) }
	out, err := (&hookTool{}).Run(context.Background(),
		provider.TaskInput{Params: params, Files: map[string]string{"audio": genHookWav(t)}}, report)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out, probs
}

func TestHookToolRun(t *testing.T) {
	out, probs := runHook(t, nil)

	// 进度映射：5 探测 → 15 解码 → 45 分析 → 100 完成
	want := []int{5, 15, 45, 100}
	if len(probs) != len(want) {
		t.Fatalf("进度序列 = %v, want %v", probs, want)
	}
	for i := range want {
		if probs[i] != want[i] {
			t.Fatalf("进度序列 = %v, want %v", probs, want)
		}
	}

	// 零产物：候选全部承载于 Summary
	if len(out.Artifacts) != 0 {
		t.Errorf("hook 为零产物工具, got %d 个产物", len(out.Artifacts))
	}

	cands, ok := out.Summary["candidates"].([]map[string]any)
	if !ok || len(cands) == 0 {
		t.Fatalf("Summary.candidates 缺失或为空: %v", out.Summary["candidates"])
	}
	for i, c := range cands {
		start, end, score := c["start"].(float64), c["end"].(float64), c["score"].(float64)
		if end-start != 30 { // 默认 duration=30：选区长度锁定参数默认值
			t.Errorf("候选 %d 长度 = %g, want 30", i, end-start)
		}
		if math.IsNaN(score) {
			t.Fatalf("候选 %d 分数 NaN", i)
		}
		if score <= 0 || score > 1 { // 窗均值归一：恒定正弦能量分量 0.7 → (0,1]
			t.Errorf("候选 %d 评分 = %g, want ∈ (0,1]（score 0-1 契约）", i, score)
		}
	}
	if got := len(cands); got != 3 { // 默认 count=3
		t.Errorf("候选数 = %d, want 3", got)
	}

	if d := out.Summary["duration_sec"].(float64); math.Abs(d-200) > 0.5 {
		t.Errorf("duration_sec = %g, want ≈200（探测总时长，非参数）", d)
	}
	if s := out.Summary["source"].(string); s != "sample_song.wav" {
		t.Errorf("source = %q, want sample_song.wav", s)
	}
}

func TestHookToolRunClampsParams(t *testing.T) {
	// duration=3 → 5、count=-1 → 1：两个钳制都从候选行为反推
	out, _ := runHook(t, map[string]any{"duration": float64(3), "count": float64(-1)})
	cands, ok := out.Summary["candidates"].([]map[string]any)
	if !ok || len(cands) != 1 {
		t.Fatalf("count=-1 应钳到 1 个候选, got %d", len(cands))
	}
	if span := cands[0]["end"].(float64) - cands[0]["start"].(float64); span != 5 {
		t.Errorf("duration=3 应钳到 5s 选区, got %g", span)
	}

	// duration=0.5 → 5、count=99 → 5
	out, _ = runHook(t, map[string]any{"duration": float64(0.5), "count": float64(99)})
	cands, ok = out.Summary["candidates"].([]map[string]any)
	if !ok || len(cands) == 0 || len(cands) > 5 {
		t.Fatalf("count=99 应钳到 ≤5 个候选, got %d", len(cands))
	}
	for i, c := range cands {
		if span := c["end"].(float64) - c["start"].(float64); span != 5 {
			t.Errorf("候选 %d: duration=0.5 应钳到 5s 选区, got %g", i, span)
		}
	}
}

func TestHookToolRunErrors(t *testing.T) {
	if _, err := (&hookTool{}).Run(context.Background(),
		provider.TaskInput{}, func(int, string, map[string]any) {}); err == nil {
		t.Fatal("缺输入应报错")
	}
}
