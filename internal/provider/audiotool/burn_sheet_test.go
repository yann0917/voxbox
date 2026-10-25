package audiotool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// makeTwoSceneMP4 红蓝双镜测试视频（各 2s），带音轨可选。
func makeTwoSceneMP4(t *testing.T, dir string, withAudio bool) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过真机验证")
	}
	out := filepath.Join(dir, "two-scenes.mp4")
	args := []string{"-y", "-v", "error",
		"-f", "lavfi", "-t", "2", "-i", "color=c=red:s=320x240:r=25",
		"-f", "lavfi", "-t", "2", "-i", "color=c=blue:s=320x240:r=25"}
	if withAudio {
		args = append(args, "-f", "lavfi", "-t", "4", "-i", "sine=frequency=440:r=44100")
	}
	args = append(args, "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]")
	if withAudio {
		args[len(args)-1] = "[0:v][1:v]concat=n=2:v=1:a=0[v];[2:a]anull[a]"
		args = append(args, "-map", "[v]", "-map", "[a]", "-c:a", "aac")
	} else {
		args = append(args, "-map", "[v]")
	}
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if err := exec.Command("ffmpeg", args...).Run(); err != nil {
		t.Fatalf("造测试视频失败: %v", err)
	}
	return out
}

// 联络表：场景切点任务应附带 image 产物（PNG 网格）。
func TestSceneDetectContactSheet(t *testing.T) {
	dir := t.TempDir()
	src := makeTwoSceneMP4(t, dir, false)
	tool := &sceneDetectTool{baseTool: baseTool{outDir: dir}}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{},
		Files:  map[string]string{"audio": src},
	}, func(_ int, _ string, _ map[string]any) {})
	if err != nil {
		t.Fatalf("场景检测失败: %v", err)
	}
	var sheet string
	for _, a := range out.Artifacts {
		if a.Kind == "image" {
			sheet = a.Path
		}
	}
	if sheet == "" {
		t.Fatalf("应附带联络表 image 产物: %+v", out.Artifacts)
	}
	if !strings.HasSuffix(sheet, ".png") {
		t.Errorf("联络表应为 PNG: %s", sheet)
	}
	full := sheet
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, full)
	}
	// PNG 魔数校验（真图片而非空文件）
	head := make([]byte, 8)
	f, err := os.Open(full)
	if err != nil {
		t.Fatalf("联络表文件不存在: %v", err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil || string(head[1:4]) != "PNG" {
		t.Errorf("联络表不是有效 PNG: %v", head)
	}
	if out.Summary["contact_sheet"] != true {
		t.Errorf("Summary 应标记 contact_sheet: %v", out.Summary)
	}
}

// 字幕烧录（SRT）：输出应为 MP4 且时长不变、字幕生效（流数不校验——烧录后字幕
// 不是流，是画面的一部分；校验时长与可解码性 + Summary 行数）。
func TestSubtitleBurnSRT(t *testing.T) {
	dir := t.TempDir()
	src := makeTwoSceneMP4(t, dir, true)
	tool := &burnTool{baseTool: baseTool{outDir: dir}}
	srt := "1\n00:00:00,000 --> 00:00:01,500\n红色场景\n\n2\n00:00:02,000 --> 00:00:03,500\n蓝色场景\n"
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"subtitle_text": srt, "subtitle_format": "srt"},
		Files:  map[string]string{"audio": src},
	}, func(_ int, _ string, _ map[string]any) {})
	if err != nil {
		t.Fatalf("烧录失败: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "video" {
		t.Fatalf("应产出 video 产物: %+v", out.Artifacts)
	}
	full := out.Artifacts[0].Path
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, full)
	}
	// 时长保持 ~4s
	dur := probeDurationGeneric(t, full)
	if dur < 3.8 || dur > 4.3 {
		t.Errorf("烧录后时长 %.2f 应保持 ~4s", dur)
	}
	if out.Summary["subtitle_lines"] != 2 {
		t.Errorf("Summary 应计 2 条字幕: %v", out.Summary["subtitle_lines"])
	}
}

// 字幕烧录（ASS）：ASS 全文保真（无 force_style）路径可跑通。
func TestSubtitleBurnASS(t *testing.T) {
	dir := t.TempDir()
	src := makeTwoSceneMP4(t, dir, false) // 无音轨：验证 -an 分支
	tool := &burnTool{baseTool: baseTool{outDir: dir}}
	ass := "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname\n\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:00.00,0:00:01.50,Default,,0,0,0,,红\nDialogue: 0,0:00:02.00,0:00:03.50,Default,,0,0,0,,蓝\n"
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"subtitle_text": ass, "subtitle_format": "ass"},
		Files:  map[string]string{"audio": src},
	}, func(_ int, _ string, _ map[string]any) {})
	if err != nil {
		t.Fatalf("ASS 烧录失败: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "video" {
		t.Fatalf("应产出 video 产物: %+v", out.Artifacts)
	}
	if out.Summary["subtitle_lines"] != 2 {
		t.Errorf("ASS Dialogue 计数应 2: %v", out.Summary["subtitle_lines"])
	}
}

// 烧录双文件通道（流水线形态）：Files[audio]=视频、Files[audio2]=SRT 文件，
// 扩展名探测格式，subtitle_text 参数不填——工作流连线的真实路径。
func TestSubtitleBurnFileChannel(t *testing.T) {
	dir := t.TempDir()
	src := makeTwoSceneMP4(t, dir, true)
	sub := filepath.Join(dir, "sub.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,000 --> 00:00:01,500\n红色场景\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &burnTool{baseTool: baseTool{outDir: dir}}
	out, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{}, // 全空参数：字幕只能来自文件通道
		Files:  map[string]string{"audio": src, "audio2": sub},
	}, func(_ int, _ string, _ map[string]any) {})
	if err != nil {
		t.Fatalf("文件通道烧录失败: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Kind != "video" {
		t.Fatalf("应产出 video 产物: %+v", out.Artifacts)
	}
	if out.Summary["subtitle_format"] != "srt" {
		t.Errorf("格式应按扩展名探测 srt: %v", out.Summary["subtitle_format"])
	}
}

// 双通道都缺：明确报错（不静默产出无字幕视频）。
func TestSubtitleBurnMissingBoth(t *testing.T) {
	dir := t.TempDir()
	src := makeTwoSceneMP4(t, dir, false)
	tool := &burnTool{baseTool: baseTool{outDir: dir}}
	_, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{},
		Files:  map[string]string{"audio": src},
	}, func(_ int, _ string, _ map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "缺少字幕") {
		t.Fatalf("双通道全缺应报错，得到: %v", err)
	}
}

func probeDurationGeneric(t *testing.T, path string) float64 {
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
		t.Fatalf("解析失败: %v", err)
	}
	d, _ := strconv.ParseFloat(po.Format.Duration, 64)
	return d
}
