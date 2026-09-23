package audiotool

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// TestDuckParams 深度+人声 RMS → sidechaincompress 参数映射（T5 审查后的确定性映射锚）。
// 定案：depth=0 直通用负 Threshold 哨兵（不加独立 bool 字段）——sidechaincompress
// threshold 合法域 [0.000976563,1]，-1 天然非法，不会误触发压缩。
func TestDuckParams(t *testing.T) {
	// depth=0 → 直通哨兵：Threshold<0 表示旁路
	p0 := duckParams(0, -18)
	if p0.Threshold >= 0 {
		t.Errorf("depth=0 应为直通哨兵, got %+v", p0)
	}
	// 确定性映射：threshold 压在活动人声下方 |depth| dB（T_dB=vocalRMS+depth）×0.707 峰值因子
	// depth=-12, vocalRMS=-18 → threshold=10^((-18-12)/20)*0.707≈0.02236；ratio 恒 20
	p := duckParams(-12, -18)
	if math.Abs(p.Threshold-0.02236) > 0.001 {
		t.Errorf("threshold = %v", p.Threshold)
	}
	if p.Ratio != 20 {
		t.Errorf("ratio = %v, want 20（固定近似砖墙）", p.Ratio)
	}
	if p.Attack != 25 || p.Release != 350 {
		t.Errorf("ar = %v/%v", p.Attack, p.Release)
	}
	// ratio 全域恒 20（旧映射的钳制语义随 ratio 定值消解）
	for _, d := range []float64{-6, -13.5, -15, -40} {
		if got := duckParams(d, -18).Ratio; got != 20 {
			t.Errorf("ratio(%v) = %v, want 20", d, got)
		}
	}
	// 阈值随人声电平联动：RMS -12dB → 10^((-12-12)/20)*0.707≈0.0446
	if got := duckParams(-12, -12).Threshold; math.Abs(got-0.0446) > 0.001 {
		t.Errorf("threshold(-12dB) = %v, want ≈0.0446", got)
	}
	// threshold 下限钳制：极静人声 → 不低于 sidechaincompress 下限 0.000976563
	// （此时可达深度受限：阈值贴底，超阈量由人声实际电平决定）
	if got := duckParams(-12, -80).Threshold; got < 0.000976563 {
		t.Errorf("threshold min = %v", got)
	}
	// threshold 上限钳制：人声爆表（vocalRMS=+6、depth=-1 → 10^(0.25)*0.707≈1.257）
	// → 优雅贴 sidechaincompress 域上限 1，不让 ffmpeg 报晦涩域错误
	if got := duckParams(-1, 6).Threshold; got != 1 {
		t.Errorf("threshold max = %v, want 1", got)
	}
}

// TestBuildDuckArgs 滤镜图逐段精确锚定：duck 形态、直通形态、loudnorm 分支、wav 编码分支。
// 两形态的 [voc] 段同形（volume='0dB' 直通占位，mix 先例）；duck 形态人声必须 asplit 双路
// （sidechain 吃 [voc]、amix 吃 [voc2]——T5 Critical #1：重复消费标签=图重排未定行为）。
func TestBuildDuckArgs(t *testing.T) {
	// depth=-12 → 完整 duck 图：asplit 双路、threshold=10^((-18-12)/20)*0.707、ratio 恒 20；
	// loudness=-14 → loudnorm；mp3
	th := envNum(math.Pow(10, (-18.0-12.0)/20.0) * 0.707)
	args := buildDuckArgs(duckOpts{Depth: -12, BgmGain: -6, Loudness: -14, Format: "mp3"}, -18)
	joined := strings.Join(args, " ")
	want := "[0:a]volume='-6dB'[bg];[1:a]volume='0dB',asplit=2[voc][voc2];" +
		"[bg][voc]sidechaincompress=threshold=" + th + ":ratio=20:attack=25:release=350[mix];" +
		"[voc2][mix]amix=inputs=2:duration=longest:normalize=0[am];" +
		"[am]loudnorm=I=-14:TP=-1.5:LRA=11[out]"
	if !strings.Contains(joined, want) {
		t.Errorf("duck 滤镜图错误:\n got: %s\nwant: %s", joined, want)
	}
	// Critical #1 回归锚：amix 必须吃 [voc2]，不得无 asplit 地重复消费 [voc]
	if strings.Contains(joined, "[voc][mix]amix") {
		t.Errorf("amix 不得消费 [voc]（应 asplit 后吃 [voc2]）: %s", joined)
	}
	for _, w := range []string{"-map [out]", "-c:a libmp3lame -b:a 128k"} {
		if !strings.Contains(joined, w) {
			t.Errorf("duck 缺 %q: %s", w, joined)
		}
	}

	// depth=0 → 直通形态：无 sidechaincompress、无 asplit，[voc] 同形直连 amix；
	// loudness=0 → 无 loudnorm；wav
	j2 := strings.Join(buildDuckArgs(duckOpts{Depth: 0, BgmGain: -6, Loudness: 0, Format: "wav"}, -18), " ")
	want2 := "[0:a]volume='-6dB'[bg];[1:a]volume='0dB'[voc];" +
		"[bg][voc]amix=inputs=2:duration=longest:normalize=0[am]"
	if !strings.Contains(j2, want2) || strings.Contains(j2, "sidechaincompress") ||
		strings.Contains(j2, "asplit") ||
		strings.Contains(j2, "loudnorm") || !strings.Contains(j2, "-map [am]") ||
		!strings.Contains(j2, "-c:a pcm_s16le") {
		t.Errorf("直通形态错误: %s", j2)
	}

	// duck 形态 + loudness=0 + bgm_gain=0：同一 sidechaincompress 段，仅无 loudnorm 且 -map [am]
	j3 := strings.Join(buildDuckArgs(duckOpts{Depth: -12, BgmGain: 0, Loudness: 0, Format: "mp3"}, -18), " ")
	want3 := "[0:a]volume='0dB'[bg];[1:a]volume='0dB',asplit=2[voc][voc2];" +
		"[bg][voc]sidechaincompress=threshold=" + th + ":ratio=20:attack=25:release=350[mix];" +
		"[voc2][mix]amix=inputs=2:duration=longest:normalize=0[am]"
	if !strings.Contains(j3, want3) || strings.Contains(j3, "loudnorm") ||
		!strings.Contains(j3, "-map [am]") {
		t.Errorf("duck(无响度归一) 错误: %s", j3)
	}

	// 阈值随人声电平联动插值：vocalRMS=-12 → 10^((-12-12)/20)*0.707
	th2 := envNum(math.Pow(10, (-12.0-12.0)/20.0) * 0.707)
	j4 := strings.Join(buildDuckArgs(duckOpts{Depth: -12, BgmGain: -6, Loudness: 0, Format: "mp3"}, -12), " ")
	if !strings.Contains(j4, "sidechaincompress=threshold="+th2+":ratio=20:attack=25:release=350[mix]") {
		t.Errorf("threshold 插值错误: %s", j4)
	}
}

// TestParseDuckOpts 参数域钳制：depth [-40,0] 默认 -12；bgm_gain [-40,6] 默认 -6；
// loudness ∈ loudnorm 合法域 [-70,-5]（0=关不钳，mix 先例）；format 仅认 wav，其余一律 mp3。
func TestParseDuckOpts(t *testing.T) {
	// 缺省 = 默认档
	if p := parseDuckOpts(map[string]any{}); p.Depth != -12 || p.BgmGain != -6 ||
		p.Loudness != -14 || p.Format != "mp3" {
		t.Errorf("缺省参数错误: %+v", p)
	}
	// 域外钳到边界
	if p := parseDuckOpts(map[string]any{"depth": -99.0, "bgm_gain": 99.0, "loudness": -99.0, "format": "flac"}); p.Depth != -40 ||
		p.BgmGain != 6 || p.Loudness != -70 || p.Format != "mp3" {
		t.Errorf("下/上界钳制错误: %+v", p)
	}
	// 正向越界：depth 钳 0（直通）；loudness 钳上界 -5
	if p := parseDuckOpts(map[string]any{"depth": 5.0, "loudness": -1.0}); p.Depth != 0 || p.Loudness != -5 {
		t.Errorf("上界钳制错误: %+v", p)
	}
	// loudness=0=关（不钳到 -5）
	if p := parseDuckOpts(map[string]any{"loudness": 0.0}); p.Loudness != 0 {
		t.Errorf("loudness 0=关应保持 0: %+v", p)
	}
	// format 仅认 wav
	if p := parseDuckOpts(map[string]any{"format": "m4r"}); p.Format != "mp3" {
		t.Errorf("format 非 wav 应归 mp3: %+v", p)
	}
	// 字符串数值（CLI/MCP 通道）
	p := parseDuckOpts(map[string]any{"depth": "-20", "bgm_gain": "-3", "loudness": "-16"})
	if p.Depth != -20 || p.BgmGain != -3 || p.Loudness != -16 {
		t.Errorf("字符串数值解析错误: %+v", p)
	}
}

// TestParseRMSLevel astats 输出解析：取最后一个 RMS 行（逐通道在前、Overall 在最后）；
// 缺行报错；-inf（静音）报错。
func TestParseRMSLevel(t *testing.T) {
	out := "[Parsed_astats_0 @ 0x0] Channel: 1\n" +
		"    RMS level dB:    -19.10\n" +
		"[Parsed_astats_0 @ 0x0] Channel: 2\n" +
		"    RMS level  dB:   -17.90\n" +
		"Overall:\n" +
		"    RMS level dB:    -18.42\n"
	got, err := parseRMSLevel(out)
	if err != nil || got != -18.42 {
		t.Errorf("parse = %v, %v, want -18.42（最后一个匹配）", got, err)
	}
	if _, err := parseRMSLevel("no rms line here"); err == nil {
		t.Error("缺 RMS 行应报错")
	}
	if _, err := parseRMSLevel("    RMS level dB:      -inf\n"); err == nil {
		t.Error("-inf（静音）应报错")
	}
}

// TestProbeRMS 真实 ffmpeg 冒烟：lavfi sine 峰值幅度固定 1/8 → 峰值电平 -18.06dBFS、
// RMS = 20log10((1/8)/√2) ≈ -21.07dB（计划「≈-18dB RMS」实为峰值电平，实测修正锚定），
// 断言 ±2dB 容差；静音 → -inf → 报错。
func TestProbeRMS(t *testing.T) {
	if err := lookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过")
	}
	dir := t.TempDir()
	sine := filepath.Join(dir, "sine.wav")
	if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=3", sine).CombinedOutput(); err != nil {
		t.Fatalf("生成正弦失败: %v: %s", err, out)
	}
	got, err := probeRMS(context.Background(), sine)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-(-21.07)) > 2 {
		t.Errorf("RMS = %g, want ≈-21.07 (±2)", got)
	}

	silent := filepath.Join(dir, "silent.wav")
	if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "anullsrc=r=44100:cl=mono", "-t", "1", silent).CombinedOutput(); err != nil {
		t.Fatalf("生成静音失败: %v: %s", err, out)
	}
	if _, err := probeRMS(context.Background(), silent); err == nil || !strings.Contains(err.Error(), "RMS") {
		t.Errorf("静音（-inf RMS）应报错, got %v", err)
	}

	if _, err := probeRMS(context.Background(), filepath.Join(dir, "missing.wav")); err == nil {
		t.Error("无效文件应报错")
	}
}

// genDuckInputs 生成 Run 层 e2e 样本：BGM=30s 恒幅正弦；人声=30s（前 10s 近静音 + 后 20s 正弦）。
func genDuckInputs(t *testing.T) (bgm, vocal string) {
	t.Helper()
	if err := lookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过")
	}
	if err := lookPath("ffprobe"); err != nil {
		t.Skip("本机无 ffprobe，跳过")
	}
	dir := t.TempDir()
	bgm = filepath.Join(dir, "bgm.wav")
	if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=220:duration=30", bgm).CombinedOutput(); err != nil {
		t.Fatalf("生成 BGM 失败: %v: %s", err, out)
	}
	// 人声：10s 近静音（0.0001 直流）+ 20s 440Hz 正弦，concat 拼接成 30s
	vocal = filepath.Join(dir, "vocal.wav")
	cmd := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", "aevalsrc=0.0001:d=10:s=44100",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=20",
		"-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1[out]",
		"-map", "[out]", vocal)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成人声失败: %v: %s", err, out)
	}
	return bgm, vocal
}

// segBandRMS 产物分段频点能量：atrim 分段 + bandpass 提取频点 + astats 总 RMS（dB）。
// 闪避验收探针（T5 Critical 教训：字符串锚抓不住方向性错误，必须真信号分段对比）。
func segBandRMS(t *testing.T, path, start, end string, freq int) float64 {
	t.Helper()
	seg := fmt.Sprintf("atrim=start=%s:end=%s,", start, end)
	if end == "" {
		seg = fmt.Sprintf("atrim=start=%s,", start)
	}
	af := seg + "asetpts=PTS-STARTPTS,bandpass=f=" + strconv.Itoa(freq) +
		":width_type=h:w=100,astats=metadata=1:reset=0"
	out, err := exec.Command("ffmpeg", "-hide_banner", "-i", path,
		"-af", af, "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("分段探测失败(%s %s-%s): %v: %s", path, start, end, err, out)
	}
	v, err := parseRMSLevel(string(out))
	if err != nil {
		t.Fatalf("分段 RMS 解析失败(%s %s-%s): %v", path, start, end, err)
	}
	return v
}

// TestDuckToolRun Run 层 e2e（无 ffmpeg 自动跳过）：默认档 duck + depth=0 直通 wav 两条真链路，
// 断言产物落盘/时长=max(两输入)/Summary 键（有效参数回显 + 人声 RMS），
// 并以分段频点探针锁定闪避方向与深度（≈depth±5）、直通无压差（±1.5）。
func TestDuckToolRun(t *testing.T) {
	bgm, vocal := genDuckInputs(t)
	var probs []int
	out, err := (&duckTool{baseTool{outDir: t.TempDir()}}).Run(context.Background(),
		provider.TaskInput{Files: map[string]string{"audio": bgm, "audio2": vocal}},
		func(p int, _ string, _ map[string]any) { probs = append(probs, p) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 进度骨架：4 探测时长 → 6 人声 RMS → 8 混音（8-95 桥接段条数随 ffmpeg 实际进度不定）→ 100
	if len(probs) < 4 || probs[0] != 4 || probs[1] != 6 || probs[2] != 8 || probs[len(probs)-1] != 100 {
		t.Fatalf("进度序列异常: %v", probs)
	}

	if len(out.Artifacts) != 1 {
		t.Fatalf("产物数 = %d, want 1", len(out.Artifacts))
	}
	art := out.Artifacts[0]
	if art.Kind != "audio" || art.Format != "mp3" || art.Size <= 0 {
		t.Errorf("产物头错误: %+v", art)
	}
	if got := filepath.Base(art.Path); got != "bgm_duck.mp3" {
		t.Errorf("产物名 = %q, want bgm_duck.mp3（BGM 基名，不剥轨道后缀）", got)
	}
	if _, err := os.Stat(art.Path); err != nil {
		t.Errorf("产物未落盘: %v", err)
	}
	if d := float64(art.DurationMS); math.Abs(d-30000) > 1500 {
		t.Errorf("产物时长 = %gms, want ≈30000（max 两输入）", d)
	}

	// Summary 键：有效参数回显 + 人声 RMS（含响段 → 有界负 dB）
	if out.Summary["depth"].(float64) != -12 || out.Summary["bgm_gain"].(float64) != -6 ||
		out.Summary["loudness"].(float64) != -14 || out.Summary["format"].(string) != "mp3" {
		t.Errorf("Summary 参数错误: %v", out.Summary)
	}
	rms := out.Summary["vocal_rms"].(float64)
	if math.IsNaN(rms) || rms > 0 || rms < -60 {
		t.Errorf("vocal_rms = %g, want 有界负 dB（前静后响）", rms)
	}

	// 信号级方向锁：BGM=220Hz 频点能量，前 10s（人声近静音，不压）vs 后 20s（人声响，
	// 应压低 ≈depth=12dB）。T5 Critical 教训：只有真信号分段对比才抓得住方向性错误。
	front := segBandRMS(t, art.Path, "0", "10", 220)
	back := segBandRMS(t, art.Path, "10", "", 220)
	if d := back - front; d > -8 || d < -17 {
		t.Errorf("闪避深度/方向错误: front=%g back=%g delta=%g, want ≈-12±5", front, back, d)
	}

	// depth=0 直通 + wav + loudness=0（隔离响度归一动态）：直通滤镜图真 ffmpeg 冒烟，
	// BGM 段间能量差应 ≈0
	out2, err := (&duckTool{baseTool{outDir: t.TempDir()}}).Run(context.Background(),
		provider.TaskInput{Params: map[string]any{"depth": float64(0), "format": "wav", "loudness": float64(0)},
			Files: map[string]string{"audio": bgm, "audio2": vocal}},
		func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("直通 Run: %v", err)
	}
	art2 := out2.Artifacts[0]
	if art2.Format != "wav" || !strings.HasSuffix(art2.Path, "_duck.wav") || art2.Size <= 0 {
		t.Errorf("直通产物错误: %+v", art2)
	}
	if float64(art2.DurationMS) < 29000 {
		t.Errorf("直通产物时长 = %dms, want ≈30000", art2.DurationMS)
	}
	front2 := segBandRMS(t, art2.Path, "0", "10", 220)
	back2 := segBandRMS(t, art2.Path, "10", "", 220)
	if d := back2 - front2; math.Abs(d) > 1.5 {
		t.Errorf("直通应无压差: front=%g back=%g delta=%g", front2, back2, d)
	}
}

// TestDuckToolRunErrors 输入数与输入序报错：缺输入、单输入（文案注明第 1=BGM、第 2=人声）。
func TestDuckToolRunErrors(t *testing.T) {
	if _, err := (&duckTool{}).Run(context.Background(),
		provider.TaskInput{}, func(int, string, map[string]any) {}); err == nil {
		t.Fatal("缺输入应报错")
	}
	bgm, _ := genDuckInputs(t)
	_, err := (&duckTool{}).Run(context.Background(),
		provider.TaskInput{Files: map[string]string{"audio": bgm}}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "第 1 个=BGM") {
		t.Fatalf("单输入应报错并注明输入序, got %v", err)
	}
}
