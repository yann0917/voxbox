package audiotool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// 造一段 4 秒音频：1s 声音 + 2s 静音 + 1s 声音（sine 440，静音段用 anullsrc）。
func makeSilencedWav(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过真机验证")
	}
	out := filepath.Join(dir, "silence-in.wav")
	args := []string{"-y", "-v", "error",
		"-f", "lavfi", "-t", "1", "-i", "sine=frequency=440",
		"-f", "lavfi", "-t", "2", "-i", "anullsrc=r=44100:cl=mono",
		"-f", "lavfi", "-t", "1", "-i", "sine=frequency=440",
		"-filter_complex", "[0:a][1:a][2:a]concat=n=3:v=0:a=1",
		"-c:a", "pcm_s16le", out}
	if err := exec.Command("ffmpeg", args...).Run(); err != nil {
		t.Fatalf("造测试音频失败: %v", err)
	}
	return out
}

// ffprobeDuration 秒。
func ffprobeDuration(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", path).Output()
	if err != nil {
		t.Fatalf("ffprobe 失败: %v", err)
	}
	var po struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &po); err != nil {
		t.Fatalf("解析 ffprobe 输出失败: %v", err)
	}
	d, err := strconv.ParseFloat(po.Format.Duration, 64)
	if err != nil {
		t.Fatalf("duration 非数字: %q", po.Format.Duration)
	}
	return d
}

// 真机验证剪静音：4s 输入（含 2s 静音）剪后应显著短于原长且仍有两段声音。
func TestSilenceCutReal(t *testing.T) {
	dir := t.TempDir()
	src := makeSilencedWav(t, dir)
	tool := &silenceCutTool{baseTool{outDir: dir}}
	var lastNote string
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{},
		Files:  map[string]string{"audio": src},
	}, func(_ int, note string, _ map[string]any) { lastNote = note })
	if err != nil {
		t.Fatalf("剪静音失败: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "audio" {
		t.Fatalf("应产出 1 个音频产物: %+v", out.Artifacts)
	}
	final := out.Artifacts[0].Path
	if !filepath.IsAbs(final) {
		final = filepath.Join(dir, final)
	}
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("产物文件不存在: %v", err)
	}
	got := ffprobeDuration(t, final)
	if got > 2.5 { // 2s 静音剪除后应在 ~2s 量级（含缓冲）
		t.Errorf("剪后时长 %v 应明显短于 4s（静音已剪除）", got)
	}
	if got < 1.2 {
		t.Errorf("剪后时长 %v 过短，声音段可能被误剪", got)
	}
	if out.Summary["removed_segments"] == 0 {
		t.Errorf("Summary 应报告剪除段数: %v", out.Summary)
	}
	t.Logf("4s → %.2fs（%s）", got, lastNote)
}

// 纯函数：切点 → 场景段（minScene 合并、末段收口）。
func TestScenesFromMarks(t *testing.T) {
	cuts := scenesFromMarks([]sceneMark{{Time: 2, Score: 0.8}, {Time: 2.3, Score: 0.5}, {Time: 8, Score: 0.7}}, 10, 1)
	if len(cuts) != 3 { // 2 个有效切点（2s/8s，2.3s 被合并）→ 3 段
		t.Fatalf("2.3s 切点应被 minScene 合并，得 3 段: %+v", cuts)
	}
	if cuts[0].Start != 0 || cuts[0].End != 2 {
		t.Errorf("首段应 0-2: %+v", cuts[0])
	}
	if cuts[len(cuts)-1].End != 10 {
		t.Errorf("末段应收口到总长: %+v", cuts[len(cuts)-1])
	}
	// 无切点：整段一个场景
	one := scenesFromMarks(nil, 7.5, 1)
	if len(one) != 1 || one[0].Duration != 7.5 {
		t.Errorf("无切点应为单场景: %+v", one)
	}
}

// 真机验证场景切点：红蓝各 2 秒的两镜视频应检出 1 个切点、2 个场景段。
func TestSceneDetectReal(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过真机验证")
	}
	src := filepath.Join(dir, "two-scenes.mp4")
	args := []string{"-y", "-v", "error",
		"-f", "lavfi", "-t", "2", "-i", "color=c=red:s=320x240:r=25",
		"-f", "lavfi", "-t", "2", "-i", "color=c=blue:s=320x240:r=25",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src}
	if err := exec.Command("ffmpeg", args...).Run(); err != nil {
		t.Fatalf("造测试视频失败: %v", err)
	}
	tool := &sceneDetectTool{baseTool{outDir: dir}}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{},
		Files:  map[string]string{"audio": src},
	}, func(_ int, _ string, _ map[string]any) {})
	if err != nil {
		t.Fatalf("场景检测失败: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "scenes" {
		t.Fatalf("应产出 scenes 产物: %+v", out.Artifacts)
	}
	final := filepath.Join(dir, out.Artifacts[0].Path)
	payload, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("读产物失败: %v", err)
	}
	var cuts []sceneCut
	if err := json.Unmarshal(payload, &cuts); err != nil {
		t.Fatalf("产物 JSON 解析失败: %v", err)
	}
	if len(cuts) != 2 {
		t.Fatalf("红蓝两镜应检出 2 个场景段: %+v", cuts)
	}
	if cuts[0].End < 1.8 || cuts[0].End > 2.2 {
		t.Errorf("切点应在 ~2s: %+v", cuts[0])
	}
	t.Logf("4s 视频检出 %d 段，切点 %.2fs", len(cuts), cuts[0].End)
}
