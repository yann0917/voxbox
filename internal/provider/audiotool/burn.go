// 字幕烧录 audio.subtitle_burn：SRT/ASS 烧进视频（本地 ffmpeg subtitles 滤镜）。
// 输入形态刻意单通道：视频走文件通道（Files["audio"]），字幕全文走文本参数——
// 双文件通道（file_ids XOR artifact_inputs 互斥）无法混传上传视频+产物字幕，
// 字幕通常几十 KB 内联最稳；ASS 保真自带样式，SRT 可选三档 force_style 预设。
// 思路借鉴 ffmpeg-skill 的 caption.py（MIT），能力面收敛到 voxbox 字幕链闭环。
package audiotool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// subtitleStylePresets SRT 的 force_style 三档预设（ASS 自带样式不走 force_style）。
var subtitleStylePresets = []provider.ParamOption{
	{Value: "white-outline", Label: "白字黑边"},
	{Value: "yellow-outline", Label: "黄字黑边"},
	{Value: "white-box", Label: "白字半透明底"},
}

// 键名取 libass Style 字段（PrimaryColour 而非 drawtext 的 fontcolor）；
// 键间分隔符须写 \:（滤镜 option 值的冒号转义，整值再由调用方用引号包裹）。
func subtitleForceStyle(preset string) string {
	const base = `FontName=Sans\:FontSize=24`
	switch preset {
	case "yellow-outline":
		return base + `\:PrimaryColour=#00FFFF&H\:BorderStyle=1\:Outline=2\:Shadow=1`
	case "white-box":
		return base + `\:PrimaryColour=#FFFFFF&H\:BorderStyle=4\:Outline=1\:Shadow=0`
	default: // white-outline
		return base + `\:PrimaryColour=#FFFFFF&H\:BorderStyle=1\:Outline=2\:Shadow=1`
	}
}

// ensureSubtitleFilter 探测 subtitles 滤镜（libass）可用性。macOS brew 默认
// ffmpeg 是无 libass 的精简构建（ffmpeg-full 才带），缺失时报可行动指引。
func ensureSubtitleFilter(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-filters").Output()
	if err == nil && strings.Contains(string(out), " subtitles") {
		return nil
	}
	return fmt.Errorf("当前 ffmpeg 未编译字幕支持（libass）：macOS 请 brew install ffmpeg-full 替换精简版 ffmpeg，其他平台请安装带 libass 的 ffmpeg 后重试")
}

// escapeFilterPath subtitles 滤镜的文件路径转义（: 与 ' 与 \ 需转义，
// dataDir 含空格/中文时也不炸）。
func escapeFilterPath(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`)
	return r.Replace(p)
}

// ============================================================
// 字幕烧录 audio.subtitle_burn
// ============================================================

// burnTool 字幕烧录：字幕链（转写→翻译→样式化）的最后一步——烧进视频成品。
type burnTool struct{ baseTool }

func (t *burnTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "subtitle_burn", Title: "字幕烧录",
		Description: "把 SRT/ASS 字幕烧进视频（本地处理）：ASS 保留全部样式，SRT 三档样式预设；视频重编码、音频直通",
		Group:       "音频",
	}
}

func (t *burnTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "subtitle_text", Label: "字幕内容", Type: provider.ParamText, Required: true, Group: "字幕",
			Placeholder: "SRT 或 ASS 全文（工作流可引用输入槽 {{inputs.xxx}}）"},
		{Key: "subtitle_format", Label: "字幕格式", Type: provider.ParamEnum, Default: "srt", Group: "字幕",
			Options: []provider.ParamOption{{Value: "srt", Label: "SRT"}, {Value: "ass", Label: "ASS（保留样式）"}}},
		{Key: "style", Label: "样式（仅 SRT）", Type: provider.ParamEnum, Default: "white-outline", Group: "字幕",
			Options: subtitleStylePresets},
	}
}

func (t *burnTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传视频文件")
	}
	text := paramString(in.Params, "subtitle_text")
	if text == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少字幕内容")
	}
	format := paramString(in.Params, "subtitle_format")
	if format == "" {
		format = "srt"
	}
	switch format {
	case "srt", "ass":
	default:
		return provider.TaskOutput{}, fmt.Errorf("字幕格式仅支持 srt/ass，收到 %q", format)
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	if err := ensureSubtitleFilter(ctx); err != nil {
		return provider.TaskOutput{}, err
	}
	report(5, "正在探测视频信息…", nil)
	totalSec := probeVideoDuration(ctx, src)
	if totalSec <= 0 {
		return provider.TaskOutput{}, fmt.Errorf("无法读取视频时长（文件可能不是有效视频）")
	}

	// 字幕落临时文件（产物同目录，命名受控无特殊字符；滤镜路径转义防 dataDir 含空格/冒号）
	reqID := uuid.NewString()[:8]
	dir := filepath.Join(t.outDir, "editor", reqID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	subFile := filepath.Join(dir, "sub."+format)
	if err := os.WriteFile(subFile, []byte(text), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入字幕失败: %w", err)
	}
	defer os.Remove(subFile)

	// 字幕计数（SRT 数空行分界，ASS 数 Dialogue 行）供 Summary
	lines := strings.Split(text, "\n")
	n := 0
	if format == "ass" {
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "Dialogue:") {
				n++
			}
		}
	} else {
		n = len(strings.Split(strings.TrimSpace(text), "\n\n"))
	}

	filter := fmt.Sprintf("subtitles='%s'", escapeFilterPath(subFile))
	if format == "srt" {
		filter = fmt.Sprintf("subtitles='%s':force_style='%s'", escapeFilterPath(subFile), subtitleForceStyle(paramString(in.Params, "style")))
	}
	// 视频必须重编码（滤镜作用于解码帧）；音频直通，无音频流时 -an
	args := []string{"-vf", filter, "-c:v", "libx264", "-crf", "23", "-preset", "medium", "-c:a", "copy"}
	if !hasAudioStream(ctx, src) {
		args = append(args[:len(args)-2], "-an") // 替换 -c:a copy
	}
	artPath := filepath.Join("editor", reqID, fmt.Sprintf("%s_subtitled.mp4", srcBaseName(src)))
	report(10, "正在烧录字幕（视频重编码）…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), totalSec, report, "烧录字幕")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "字幕烧录完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "video", Path: final, Format: "mp4", Size: size, DurationMS: durMS,
		}},
		Summary: map[string]any{
			"subtitle_format": format,
			"subtitle_lines":  n,
			"duration_sec":    round2(totalSec),
			"source":          srcBaseName(src),
		},
	}, nil
}

// hasAudioStream 探测是否含音频流（决定 -c:a copy 还是 -an）。
func hasAudioStream(ctx context.Context, path string) bool {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet",
		"-print_format", "json", "-show_streams", path).Output()
	if err != nil {
		return true // 探测失败按有音频处理，让 ffmpeg 报可定位错误
	}
	return strings.Contains(string(out), `"codec_type":"audio"`)
}
