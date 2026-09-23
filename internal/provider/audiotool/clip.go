// 选区切片导出 audio.clip：atrim 截取 + afade 淡入淡出 + 可选 loudnorm 响度归一，
// mp3/m4a/m4r 三档固定 128k（无 bitrate 参数）。参数构建（纯函数）与进程执行分离，
// 构建层全部可单测。
package audiotool

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yann0917/voxbox/internal/provider"
)

// clipMaxSpan 单次切片选区上限（秒）：防误传超长选区任务。
const clipMaxSpan = 600

// clipParams clip 参数编译载体（Run 从请求参数提取后整包交 buildClipArgs）。
type clipParams struct {
	Start    float64
	End      float64
	FadeIn   float64
	FadeOut  float64
	Loudness float64
	Format   string
}

// parseClipParams 从请求参数提取切片标量参数：loudness ∈ loudnorm 合法域
// [-70,-5] LUFS（域外 ffmpeg 直接失败 Result too large），0=关不动；format 仅认
// m4a/m4r，其余一律 mp3。
func parseClipParams(params map[string]any) clipParams {
	p := clipParams{
		Start:    paramFloat(params, "start", 0),
		End:      paramFloat(params, "end", 0),
		FadeIn:   paramFloat(params, "fade_in", 1),
		FadeOut:  paramFloat(params, "fade_out", 0.5),
		Loudness: paramFloat(params, "loudness", -14),
		Format:   strings.ToLower(paramString(params, "format")),
	}
	if p.Loudness != 0 { // 0=关：保持 0 以区分「关」与「钳到域内」
		p.Loudness = clampF(p.Loudness, -70, -5)
	}
	switch p.Format {
	case "m4a", "m4r":
	default:
		p.Format = "mp3"
	}
	return p
}

// clipArtName 产物文件名：<源基名>_clip_<起点>s-<终点>s.<ext>（秒数 int 截断）。
func clipArtName(srcPath string, start, end float64, ext string) string {
	return fmt.Sprintf("%s_clip_%ds-%ds.%s", srcBaseName(srcPath), int(start), int(end), ext)
}

// clipFade 选区内淡入淡出时长钳制：负值归 0（省略该 afade），超选区一半折半
// （淡入/淡出各自最长占选区一半）。
func clipFade(d, span float64) float64 { return clampF(d, 0, span/2) }

// buildClipArgs 编译切片参数（total 为选区跨度秒数 end-start，供淡出定位与跨度校验）。
// 滤镜图恒为 [0:a] 单链：atrim 截取 → asetpts 时间戳归零（切片后时间轴从 0 起算，
// 淡出定位与播放器 seek 才不漂）→ 可选 afade in（st=0）/ afade out（st=span−fo'，
// 用钳制后的 fo'）→ loudness≠0 时尾接 loudnorm（0=关）。编码三档固定 128k + 显式
// -ar 44100（loudnorm 内部按 192k 处理，不显式归一则三格式采样率分裂），m4r 走
// ipod muxer（= m4a 容器的铃声兼容结构）。
func buildClipArgs(p clipParams, total float64) ([]string, error) {
	if p.Start < 0 {
		return nil, fmt.Errorf("起点不能为负，收到 %g", p.Start)
	}
	if p.End <= p.Start {
		return nil, fmt.Errorf("终点（%gs）需大于起点（%gs）", p.End, p.Start)
	}
	if total > clipMaxSpan {
		return nil, fmt.Errorf("选区跨度（%gs）超上限 %d 秒", total, clipMaxSpan)
	}

	chain := fmt.Sprintf("[0:a]atrim=start=%s:end=%s,asetpts=PTS-STARTPTS",
		trimFloat(p.Start), trimFloat(p.End))
	if fi := clipFade(p.FadeIn, total); fi > 0 {
		chain += fmt.Sprintf(",afade=t=in:st=0:d=%s", trimFloat(fi))
	}
	if fo := clipFade(p.FadeOut, total); fo > 0 {
		chain += fmt.Sprintf(",afade=t=out:st=%s:d=%s", trimFloat(total-fo), trimFloat(fo))
	}
	chain += "[a]"
	label := "[a]"
	if p.Loudness != 0 { // 0=关响度归一
		chain += fmt.Sprintf(";[a]loudnorm=I=%s:TP=-1.5:LRA=11[out]", envNum(p.Loudness))
		label = "[out]"
	}
	args := []string{"-filter_complex", chain, "-map", label}
	// 三格式统一采样率 44.1k：loudnorm 内部上采 192k，不显式归一则 mp3/m4a/m4r
	// 采样率分裂（同一首歌切出三种采样率的产物）。
	args = append(args, "-ar", "44100")
	switch p.Format {
	case "m4r": // ipod muxer：m4a 容器的铃声兼容结构
		args = append(args, "-f", "ipod", "-c:a", "aac", "-b:a", "128k")
	case "m4a":
		args = append(args, "-c:a", "aac", "-b:a", "128k")
	default: // mp3 固定 128k（默认档纪律，无 bitrate 参数）
		args = append(args, "-c:a", "libmp3lame", "-b:a", "128k")
	}
	return args, nil
}

// ============================================================
// 选区切片导出 audio.clip
// ============================================================

// clipTool 选区切片导出：高潮/hook 候选选区一键成片（重编码精确定位）。
type clipTool struct{ baseTool }

func (t *clipTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "clip", Title: "选区切片",
		Description: "截取音频选区并加淡入淡出与响度归一导出，支持 mp3/m4a/m4r（m4r 即 iPhone 铃声），本地 ffmpeg 精确定位",
		Group:       "音频",
	}
}

func (t *clipTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "start", Label: "起点（秒）", Type: provider.ParamFloat, Required: true, Group: "选区",
			Placeholder: "如 61.5"},
		{Key: "end", Label: "终点（秒）", Type: provider.ParamFloat, Required: true, Group: "选区",
			Placeholder: "选区最长 600 秒"},
		{Key: "fade_in", Label: "淡入（秒）", Type: provider.ParamFloat, Default: "1", Group: "选区",
			Placeholder: "0=不淡入；超选区一半自动折半"},
		{Key: "fade_out", Label: "淡出（秒）", Type: provider.ParamFloat, Default: "0.5", Group: "选区",
			Placeholder: "0=不淡出；超选区一半自动折半"},
		{Key: "loudness", Label: "响度归一(LUFS)", Type: provider.ParamFloat, Default: "-14", Group: "输出",
			Placeholder: "0=关；有效域 -70~-5"},
		{Key: "format", Label: "输出格式", Type: provider.ParamEnum, Default: "mp3", Group: "输出",
			Options: []provider.ParamOption{
				{Value: "mp3", Label: "MP3"},
				{Value: "m4a", Label: "M4A (AAC)"},
				{Value: "m4r", Label: "M4R (iPhone 铃声)"},
			}},
	}
}

func (t *clipTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := parseClipParams(in.Params)
	report(5, "正在探测音频信息…", nil)
	info, err := probeAudio(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	span := p.End - p.Start
	if span > 0 && p.End > info.Duration+0.01 { // 容差 0.01s：probe 时长取整误差
		return provider.TaskOutput{}, fmt.Errorf("终点（%gs）超出音频时长（%gs）", p.End, info.Duration)
	}
	args, err := buildClipArgs(p, span)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	artPath := filepath.Join("editor", newReqID(), clipArtName(src, p.Start, p.End, p.Format))
	report(8, "开始切片…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), span, report, "切片中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "切片完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: p.Format,
			Size: size, DurationMS: durMS,
			Meta: map[string]any{"op": "clip", "source": filepath.Base(src)},
		}},
		Summary: map[string]any{
			"start": p.Start, "end": p.End,
			"fade_in": clipFade(p.FadeIn, span), "fade_out": clipFade(p.FadeOut, span),
			"loudness": p.Loudness, "format": p.Format,
			"duration_sec": round2(span),
		},
	}, nil
}
