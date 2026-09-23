// 口播闪避 audio.duck：人声侧链压低 BGM（spec docs/plans/2026-09-20-clip-duck.md §2）。
// 输入约定 files["audio"]=BGM、files["audio2"]=人声（artifact_inputs 按序映射）；
// 该序即滤镜图序：[0:a]=BGM 主输入、[1:a]=人声侧链，不可颠倒。
package audiotool

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yann0917/voxbox/internal/provider"
)

// duckSidechainMin sidechaincompress threshold 合法下限（ffmpeg 硬域 0.000976563-1）。
const duckSidechainMin = 0.000976563

// duckP sidechaincompress 参数（duckParams 纯函数映射结果）。
type duckP struct {
	Threshold float64 // 线性阈值；负值=直通哨兵（depth=0 不压缩，见 duckParams）
	Ratio     float64
	Attack    float64 // ms
	Release   float64 // ms
}

// duckParams 闪避深度（dB，域 [-40,0]）+ 人声整体 RMS（dB）→ sidechaincompress 参数。
// 确定性映射（T5 审查裁定）：threshold 压在活动人声下方 |depth| dB（T_dB = vocalRMS + depth），
// 人声一说话就稳定超阈 |depth| dB → 实际压深 ≈ depth，不再依赖「信号超阈多少」的
// 信号相关不可控量（语音越密峰值越密，实际压深还会过冲上浮约 4~8dB——宁深勿浅）；
// ×0.707 峰值因子（正弦 RMS 比峰值低 3dB，压缩器按样本电平检测）；
// ratio 固定 20（近似砖墙）；attack/release 固定 25/350ms（口播闪避的常规动作速度）。
// 钳 sidechaincompress 硬域 [0.000976563, 1]：下限=极静人声（vocalRMS+depth 低于 −60dB）
// 时阈值贴底，可达深度受此限——静音人标本无闪避意义（probeRMS 对 -inf 已硬失败）；
// 上限=人声爆表（vocalRMS+depth 高于 +3dBFS）时优雅贴 1，不让 ffmpeg 报晦涩域错误。
// depth=0 → 直通：Threshold 置 -1 哨兵（合法域外），buildDuckArgs 据此出直通图，
// 语义三处（哨兵/图/页面文案）一致。
func duckParams(depth, vocalRMS float64) duckP {
	if depth == 0 {
		return duckP{Threshold: -1, Ratio: 20, Attack: 25, Release: 350}
	}
	return duckP{
		Threshold: math.Min(1, math.Max(math.Pow(10, (vocalRMS+depth)/20)*0.707, duckSidechainMin)),
		Ratio:     20,
		Attack:    25,
		Release:   350,
	}
}

// duckOpts duck 运行参数（Run 从请求参数提取后整包交 buildDuckArgs）。
type duckOpts struct {
	Depth    float64 // 闪避深度 dB，[-40,0]，0=直通不压
	BgmGain  float64 // BGM 基线增益 dB
	Loudness float64 // 响度归一 LUFS，0=关
	Format   string  // mp3 | wav
}

// parseDuckOpts 参数钳制：depth [-40,0] 默认 -12（0=直通，越界钳 0）；bgm_gain [-40,6]
// 默认 -6；loudness ∈ loudnorm 合法域 [-70,-5]（域外 ffmpeg 直接失败，0=关不动，mix 先例）；
// format 仅认 wav，其余一律 mp3（duck 是播客/口播场景，无 m4a/m4r）。
func parseDuckOpts(params map[string]any) duckOpts {
	p := duckOpts{
		Depth:    clampF(paramFloat(params, "depth", -12), -40, 0),
		BgmGain:  clampF(paramFloat(params, "bgm_gain", -6), -40, 6),
		Loudness: paramFloat(params, "loudness", -14),
		Format:   paramString(params, "format"),
	}
	if p.Loudness != 0 { // 0=关：保持 0 以区分「关」与「钳到域内」
		p.Loudness = clampF(p.Loudness, -70, -5)
	}
	if p.Format != "wav" {
		p.Format = "mp3"
	}
	return p
}

// buildDuckArgs 生成 ffmpeg 参数（不含 -i 与输出路径，runToArtifact 拼装）。
//
// 输入序铁律：0=BGM（主输入）、1=人声（侧链）——sidechaincompress 的 [main][sidechain]
// 序不可颠倒，颠倒了被压的是人声本体（方向性灾难且难以听感排查）。
// 人声本体不经 sidechaincompress（它只是控制信号），压缩后的 BGM 需与人声原轨相加
// 才是完整混音——人声流要同时喂 sidechain（控制信号）与 amix（本体），必须 asplit
// 成两路：ffmpeg 不允许同一标签被消费两次，重复标签的图重排行为不定（T5 实测 amix
// 被喂了 BGM、人声整体丢失），sidechain 吃 [voc]、amix 吃 [voc2]。
// [voc] 段两形态同形（volume='0dB' 直通占位，mix 先例）；depth=0（直通哨兵
// Threshold<0）→ 无 sidechaincompress 段，[bg][voc] 直连 amix（单引用，无需 asplit）。
func buildDuckArgs(p duckOpts, vocalRMS float64) []string {
	fc := fmt.Sprintf("[0:a]volume='%sdB'[bg];", envNum(p.BgmGain))
	if sp := duckParams(p.Depth, vocalRMS); sp.Threshold < 0 { // 直通哨兵
		fc += "[1:a]volume='0dB'[voc];[bg][voc]amix=inputs=2:duration=longest:normalize=0[am]"
	} else {
		fc += fmt.Sprintf(
			"[1:a]volume='0dB',asplit=2[voc][voc2];"+
				"[bg][voc]sidechaincompress=threshold=%s:ratio=%s:attack=%s:release=%s[mix];"+
				"[voc2][mix]amix=inputs=2:duration=longest:normalize=0[am]",
			envNum(sp.Threshold), envNum(sp.Ratio), envNum(sp.Attack), envNum(sp.Release))
	}
	outLabel := "[am]"
	if p.Loudness != 0 { // 0=关响度归一
		fc += fmt.Sprintf(";[am]loudnorm=I=%s:TP=-1.5:LRA=11[out]", envNum(p.Loudness))
		outLabel = "[out]"
	}
	args := []string{"-filter_complex", fc, "-map", outLabel}
	if p.Format == "wav" {
		args = append(args, "-c:a", "pcm_s16le")
	} else {
		// mp3 固定 128k（默认档纪律，与 mix 同）
		args = append(args, "-c:a", "libmp3lame", "-b:a", "128k")
	}
	return args
}

// rmsLevelRe astats 的 RMS 行（`    RMS level dB:    -18.42`，容忍任意空白）。
var rmsLevelRe = regexp.MustCompile(`RMS level dB:[ \t]+(\S+)`)

// probeRMS 快检音频整体 RMS（dB）：ffmpeg astats 一次性解码统计，供 duckParams 折算
// sidechaincompress 阈值。astats 逐通道输出后再打 Overall 段，取最后一个匹配=Overall
// （reset=0 下即全文件累计）。
func probeRMS(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-i", path,
		"-af", "astats=metadata=1:reset=0", "-f", "null", "-").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("分析音频 RMS 失败: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseRMSLevel(string(out))
}

// parseRMSLevel 从 astats 输出抓最后一个 RMS level dB 值；-inf/NaN 视作静音报错
// （静音人声没有闪避意义，硬失败优于静默产出压不出效果的文件）。
func parseRMSLevel(out string) (float64, error) {
	m := rmsLevelRe.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("astats 输出中未找到 RMS level dB 行")
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("人声静音或无法分析 RMS")
	}
	return v, nil
}

// duckTool 口播闪避：说话时 BGM 自动压低，单滑杆深度。
type duckTool struct{ baseTool }

func (t *duckTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "duck", Title: "口播闪避",
		Description: "说话时 BGM 自动压低：单滑杆闪避深度，人声侧链压缩+响度归一导出",
		Group:       "音频",
	}
}

func (t *duckTool) ParamSpecs() []provider.ParamSpec {
	return append([]provider.ParamSpec{
		{Key: "depth", Label: "闪避深度(dB)", Type: provider.ParamFloat, Default: "-12", Group: "主链",
			Placeholder: "-40 ~ 0，0=不压直通；绝对值越大说话时 BGM 压得越低；实际压深随语音密度过冲，约再加深 4~8dB（宁深勿浅）"},
		{Key: "bgm_gain", Label: "BGM 基线增益(dB)", Type: provider.ParamFloat, Default: "-6", Group: "主链",
			Placeholder: "-40 ~ 6"},
		{Key: "loudness", Label: "响度归一(LUFS)", Type: provider.ParamFloat, Default: "-14", Group: "母带",
			Placeholder: "0=关；有效域 -70~-5"},
	}, outputFormatSpecs()...)
}

func (t *duckTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	inputs := orderedInputs(in.Files)
	if len(inputs) != 2 {
		// 输入序即滤镜图序（0=BGM 主输入、1=人声侧链），报错文案双向写明
		return provider.TaskOutput{}, fmt.Errorf("口播闪避需要恰好 2 个输入（第 1 个=BGM，第 2 个=人声），收到 %d 个", len(inputs))
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	p := parseDuckOpts(in.Params)

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
	report(6, "正在分析人声响度…", nil)
	vocalRMS, err := probeRMS(ctx, inputs[1]) // 第 2 个输入=人声（侧链）
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("%s: %w", filepath.Base(inputs[1]), err)
	}
	args := buildDuckArgs(p, vocalRMS)
	artPath := filepath.Join("editor", newReqID(), srcBaseName(inputs[0])+"_duck."+p.Format)

	report(8, "开始闪避混音…", nil)
	final, size, durMS, err := t.runToArtifact(ctx, inputs, args, artPath, paramString(in.Params, "_out"), total, report, "闪避混音中")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "闪避混音完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: p.Format,
			Size: size, DurationMS: durMS,
		}},
		Summary: map[string]any{
			"depth": p.Depth, "bgm_gain": p.BgmGain,
			"loudness": p.Loudness, "format": p.Format,
			"vocal_rms": round2(vocalRMS),
		},
	}, nil
}
