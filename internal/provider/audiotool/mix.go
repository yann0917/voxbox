// 垫音混音 audio.mix：伴奏+人声双轨混音（spec docs/plans/2026-09-19-mixer-workbench.md §5.2）。
// 输入约定 files["audio"]=伴奏、files["audio2"]=人声（artifact_inputs 按序映射）。
package audiotool

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yann0917/voxbox/internal/provider"
)

// 已知轨道后缀：产物基名剥离用（与分离产物命名对应）。
var stemSuffixes = []string{"_instrumental", "_background", "_vocals", "_voice"}

// mixBaseName 伴奏文件名基名：去扩展名再剥已知轨道后缀（稻香_instrumental.mp3 → 稻香）。
func mixBaseName(path string) string {
	name := srcBaseName(path)
	for _, suf := range stemSuffixes {
		if strings.HasSuffix(name, suf) {
			name = strings.TrimSuffix(name, suf)
			break
		}
	}
	return name
}

// mixParams mix 参数编译载体（Run 从请求参数提取后整包交 buildMixArgs）。
type mixParams struct {
	VocalGain     float64
	VocalHighpass float64
	MusicGain     float64
	Loudness      float64
	EnvExpr       string // 空=常量 VocalGain；非空=已编译包络表达式（整值单引号包裹）
	Format        string // mp3 | wav
}

// buildMixArgs 生成 ffmpeg 参数（不含 -i 与输出路径，runToArtifact 拼装）。
// 输入序：0=伴奏 1=人声，两轨 amix 输入序 [伴奏][人声] 不可颠倒；
// amix normalize=0 必须显式（默认的自动电平归半会破坏推子语义）。
// music_gain=0 也保留伴奏增益链（volume='0dB' 直通）：滤镜图形状恒定，断言与排障都简单。
func buildMixArgs(p mixParams) ([]string, error) {
	var vocal []string
	if p.VocalHighpass > 0 { // 0=关低切
		vocal = append(vocal, fmt.Sprintf("highpass=f=%s", envNum(p.VocalHighpass)))
	}
	if p.EnvExpr != "" {
		// 包络表达式按帧求值；值已是线性幅度倍率（compileEnvExpr 发射前完成 dB→线性换算，
		// ffmpeg 表达式结果无 dB 语义）；整值单引号包裹（逗号/括号才不会被 ffmpeg 拆参）
		vocal = append(vocal, fmt.Sprintf("volume='%s':eval=frame", p.EnvExpr))
	} else {
		vocal = append(vocal, fmt.Sprintf("volume='%sdB'", envNum(p.VocalGain)))
	}
	fc := fmt.Sprintf("[0:a]volume='%sdB'[m];[1:a]%s[v];[m][v]amix=inputs=2:duration=longest:normalize=0[am]",
		envNum(p.MusicGain), strings.Join(vocal, ","))
	outLabel := "[am]"
	if p.Loudness != 0 { // 0=关响度归一
		fc += fmt.Sprintf(";[am]loudnorm=I=%s:TP=-1.5:LRA=11[out]", envNum(p.Loudness))
		outLabel = "[out]"
	}
	args := []string{"-filter_complex", fc, "-map", outLabel}
	if p.Format == "wav" {
		args = append(args, "-c:a", "pcm_s16le")
	} else {
		// mp3 固定 128k（默认档纪律；mix 不提供 bitrate 参数）
		args = append(args, "-c:a", "libmp3lame", "-b:a", "128k")
	}
	return args, nil
}

// mixTool 垫音混音：分离产物（伴奏/人声）再加工的合轨出口。
type mixTool struct{ baseTool }

func (t *mixTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "mix", Title: "垫音混音",
		Description: "伴奏+人声双轨混音：人声增益/分段垫音包络/低切，响度归一导出",
		Group:       "音频",
	}
}

func (t *mixTool) ParamSpecs() []provider.ParamSpec {
	return append([]provider.ParamSpec{
		{Key: "vocal_gain", Label: "人声增益(dB)", Type: provider.ParamFloat, Default: "-16", Group: "人声",
			Placeholder: "-99 纯伴奏 ~ 0 原声；垫音建议 -16"},
		{Key: "vocal_env", Label: "垫音包络", Type: provider.ParamText, Group: "人声",
			Placeholder: "JSON [[秒,dB]…]，同刻双点=跳变；空=恒增益"},
		{Key: "vocal_highpass", Label: "人声低切(Hz)", Type: provider.ParamFloat, Default: "120", Group: "人声",
			Placeholder: "0=关，上限 20000"},
		{Key: "music_gain", Label: "伴奏增益(dB)", Type: provider.ParamFloat, Default: "0", Group: "伴奏"},
		{Key: "master_loudness", Label: "响度归一(LUFS)", Type: provider.ParamFloat, Default: "-14", Group: "母带",
			Placeholder: "0=关；有效域 -70~-5"},
	}, outputFormatSpecs()...)
}

// outputFormatSpecs mix 专用输出参数：仅 mp3（固定 128k）/wav，无 bitrate 档。
func outputFormatSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "format", Label: "输出格式", Type: provider.ParamEnum, Default: "mp3", Group: "输出",
			Options: []provider.ParamOption{
				{Value: "mp3", Label: "MP3"},
				{Value: "wav", Label: "WAV (无损)"},
			}},
	}
}

// parseMixParams 从请求参数提取混音标量参数并钳制到各自合法域：
// vocal_gain [-99,6]、music_gain [-60,12]、vocal_highpass [0,20000]Hz（域外 ffmpeg
// highpass 拒绝/失真）；master_loudness ∈ loudnorm 合法域 [-70,-5] LUFS（域外 ffmpeg
// 直接失败 Result too large），0=关不动；format 仅认 wav，其余一律 mp3。
func parseMixParams(params map[string]any) mixParams {
	p := mixParams{
		VocalGain:     clampF(paramFloat(params, "vocal_gain", -16), -99, 6),
		VocalHighpass: clampF(paramFloat(params, "vocal_highpass", 120), 0, 20000),
		MusicGain:     clampF(paramFloat(params, "music_gain", 0), -60, 12),
		Loudness:      paramFloat(params, "master_loudness", -14),
		Format:        paramString(params, "format"),
	}
	if p.Loudness != 0 { // 0=关：保持 0 以区分「关」与「钳到域内」
		p.Loudness = clampF(p.Loudness, -70, -5)
	}
	if p.Format != "wav" {
		p.Format = "mp3"
	}
	return p
}

func (t *mixTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	inputs := orderedInputs(in.Files)
	if len(inputs) != 2 {
		return provider.TaskOutput{}, fmt.Errorf("混音需要恰好 2 个输入（伴奏、人声），收到 %d 个", len(inputs))
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := parseMixParams(in.Params)
	pts, err := parseEnvParam(in.Params, "vocal_env")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if len(pts) > 0 {
		expr, err := compileEnvExpr(pts)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		p.EnvExpr = expr
	}

	report(4, "正在探测输入时长…", nil)
	var total float64
	for _, path := range inputs {
		info, err := probeAudio(ctx, path)
		if err != nil {
			return provider.TaskOutput{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if info.Duration > total {
			total = info.Duration
		}
	}
	args, err := buildMixArgs(p)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	artPath := filepath.Join("editor", newReqID(), mixBaseName(inputs[0])+"_mix."+p.Format)

	report(8, "开始混音…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, inputs, args, artPath, paramString(in.Params, "_out"), total, report, "混音中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "混音完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: p.Format,
			Size: size, DurationMS: durMS,
		}},
		Summary: map[string]any{
			"vocal_gain": p.VocalGain, "vocal_highpass": p.VocalHighpass, "music_gain": p.MusicGain,
			"master_loudness": p.Loudness, "envelope": len(pts),
		},
	}, nil
}
