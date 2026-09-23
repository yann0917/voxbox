package main

import (
	"fmt"
	"math"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/task"
)

// audio 命令族：本地 ffmpeg 音频剪辑（切割/合并/垫音混音/变调变速/调与BPM查询/
// 均衡器/音量响度/淡入淡出/倒放）。产物默认落数据目录，--out 指定绝对路径；--json 输出
// 与其他工具同一契约（artifacts[].path 为绝对路径）。
func newAudioCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audio",
		Short: "音频剪辑：切割/合并/垫音混音/变调变速/调与BPM查询/均衡器/音量/淡入淡出/倒放/副歌切片/口播闪避",
		Long: `本地音频剪辑工具集（依赖 ffmpeg/ffprobe，产物落数据目录或 --out 指定路径）。
子命令：trim 切割、merge 合并、mix 垫音混音、pitch 变调变速、analyze 调与BPM查询、
eq 均衡器、volume 音量与响度、fade 淡入淡出、reverse 倒放、hook 副歌候选、clip 副歌切片、
duck 口播闪避。
通用输出参数：--format auto|mp3|m4a|wav|flac|ogg，--bitrate 128k|192k|256k|320k
（mix/clip/duck 例外：工具级 --format（mix 仅 mp3|wav，clip 仅 mp3|m4a|m4r，duck 仅
mp3|wav）固定 128k，无 --bitrate；hook 零产物：仅 --json，无 --out）。`,
	}
	cmd.AddCommand(
		newAudioTrimCmd(),
		newAudioMergeCmd(),
		newAudioMixCmd(),
		newAudioHookCmd(),
		newAudioClipCmd(),
		newAudioDuckCmd(),
		newAudioPitchCmd(),
		newAudioAnalyzeCmd(),
		newAudioEqCmd(),
		newAudioVolumeCmd(),
		newAudioFadeCmd(),
		newAudioReverseCmd(),
	)
	return cmd
}

// audioOutFlags 通用输出参数绑定（--format/--bitrate/--out/--json）。
type audioOutFlags struct {
	format  string
	bitrate string
	out     string
	jsonOut bool
}

// bindAudioOut 把通用输出参数绑定到调用方持有的结构上。
func bindAudioOut(f *pflag.FlagSet, o *audioOutFlags) {
	f.StringVar(&o.format, "format", "auto", "输出格式: auto|mp3|m4a|wav|flac|ogg")
	f.StringVar(&o.bitrate, "bitrate", "192k", "码率（无损格式忽略）: 128k|192k|256k|320k")
	f.StringVar(&o.out, "out", "", "产物输出绝对路径（默认数据目录自动命名）")
	f.BoolVar(&o.jsonOut, "json", false, "stdout 输出机器可读 JSON")
}

func (o *audioOutFlags) params() map[string]any {
	p := map[string]any{"format": o.format, "bitrate": o.bitrate}
	if o.out != "" {
		p["_out"] = o.out
	}
	return p
}

func requireFile(file string) error {
	if file == "" {
		return fmt.Errorf("请提供输入音频文件路径")
	}
	return nil
}

func newAudioTrimCmd() *cobra.Command {
	var (
		file   string
		start  float64
		end    float64
		cutout bool
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "trim <file>",
		Short: "切割：保留选区，或挖除选区保留其余（去广告/去口误）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			params := o.params()
			params["start"] = start
			params["end"] = end
			params["cutout"] = cutout
			return runToolSync(c, "audio", "trim", params, map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&start, "start", 0, "起点（秒）")
	cmd.Flags().Float64Var(&end, "end", 0, "终点（秒），0=到结尾")
	cmd.Flags().BoolVar(&cutout, "cutout", false, "挖除选区（保留选区以外的部分）")
	bindAudioOut(cmd.Flags(), o)
	return cmd
}

func newAudioMergeCmd() *cobra.Command {
	var crossfade float64
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "merge <file1> <file2> [file3...]",
		Short: "合并：多个音频按顺序合并为一个，可选接缝交叉淡化",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			files := map[string]string{}
			for i, f := range args {
				key := "audio"
				if i > 0 {
					key = fmt.Sprintf("audio%d", i+1)
				}
				files[key] = f
			}
			params := o.params()
			params["crossfade"] = crossfade
			return runToolSync(c, "audio", "merge", params, files, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&crossfade, "crossfade", 0, "接缝交叉淡化秒数（0=直接拼接）")
	bindAudioOut(cmd.Flags(), o)
	return cmd
}

func newAudioMixCmd() *cobra.Command {
	var (
		vocalGain float64
		env       string
		vocalHP   float64
		musicGain float64
		loudness  float64
		format    string
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "mix <伴奏> <人声>",
		Short: "垫音混音：伴奏+人声双轨混音，人声增益/分段包络/低切，响度归一导出",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := requireFile(args[0]); err != nil {
				return err
			}
			if err := requireFile(args[1]); err != nil {
				return err
			}
			params := map[string]any{
				"vocal_gain": vocalGain, "vocal_highpass": vocalHP,
				"music_gain": musicGain, "master_loudness": loudness,
			}
			if env != "" {
				params["vocal_env"] = env
			}
			if format != "" {
				params["format"] = format
			}
			if o.out != "" {
				params["_out"] = o.out
			}
			return runToolSync(c, "audio", "mix", params,
				map[string]string{"audio": args[0], "audio2": args[1]}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&vocalGain, "vocal-gain", -16, "人声增益 dB（-99 纯伴奏 ~ 0 原声）")
	cmd.Flags().StringVar(&env, "env", "", "垫音包络 JSON，如 '[[30,-60],[35,-16]]'（同刻双点=跳变）")
	cmd.Flags().Float64Var(&vocalHP, "vocal-highpass", 120, "人声低切 Hz（0=关）")
	cmd.Flags().Float64Var(&musicGain, "music-gain", 0, "伴奏增益 dB")
	cmd.Flags().Float64Var(&loudness, "loudness", -14, "响度归一 LUFS（0=关）")
	cmd.Flags().StringVar(&format, "format", "mp3", "输出格式 mp3|wav")
	// mix 不走 bindAudioOut：后端 format 仅 mp3|wav（mp3 固定 128k，无 bitrate 档），
	// 与通用 --format 冲突，故只绑定同款 --out/--json，format 走上面的工具级参数。
	cmd.Flags().StringVar(&o.out, "out", "", "产物输出绝对路径（默认数据目录自动命名）")
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

func newAudioHookCmd() *cobra.Command {
	var (
		file     string
		duration float64
		count    float64
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "hook <file>",
		Short: "副歌候选检测：能量评分 top-N 候选（零产物，本地 DSP）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			params := map[string]any{"duration": duration, "count": count}
			return runAudioHookSync(c, params, map[string]string{"audio": file}, o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&duration, "duration", 30, "选区时长秒（5~60，副歌片段目标长度）")
	cmd.Flags().Float64Var(&count, "count", 3, "候选数量（1~5，按得分取 top-N）")
	// hook 零产物，--out 无意义（计划明令不提供）；--json 与 mix 同款直绑 audioOutFlags 字段。
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

// runAudioHookSync hook 专用同步执行：runToolSync 不回传结果，而 hook 零产物、候选
// 只在 Summary 里，人类模式需渲染候选表，故按 runToolSync 同款链路本地复刻（同一
// runToolCore 提交链）；--json 契约与 runToolSync 完全一致（printJSON 整包输出）。
func runAudioHookSync(c *cobra.Command, params map[string]any, files map[string]string, jsonOut bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	svc, err := service.New(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()
	// 与 runToolSync 同款：人类模式进度打 stderr（\r 原地刷新），--json 保持静默。
	notify := func(e task.Event) {
		if e.Type == "progress" {
			eprintf("\r[%s] %s %d%%", "hook", e.Note, e.Progress)
		}
	}
	if jsonOut {
		notify = nil
	}
	svc.StartEngine(notify, 1)
	result, err := runToolCore(c.Context(), svc, "audio", "hook", params, files)
	if !jsonOut {
		eprintf("\n")
	}
	if err != nil {
		eprintf("错误: %v\n", err)
		os.Exit(exitCodeFor(err))
	}
	if jsonOut {
		printJSON(result)
		return nil
	}
	eprintf("完成，耗时 %dms\n", result.CostMS)
	printHookCands(result.Summary)
	return nil
}

// printHookCands 候选表：时间码 mm:ss.s + 评分两位小数（candidates 来自结果 Summary）。
func printHookCands(summary map[string]any) {
	cands, _ := summary["candidates"].([]any)
	if len(cands) == 0 {
		return
	}
	title := fmt.Sprintf("副歌候选 top-%d", len(cands))
	if src, _ := summary["source"].(string); src != "" {
		title += "（" + src
		if dur, ok := summary["duration_sec"].(float64); ok {
			title += "，总时长 " + mmss(dur)
		}
		title += "）"
	}
	fmt.Println(title)
	fmt.Println("#  起点      终点      评分")
	for i, raw := range cands {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		st, _ := m["start"].(float64)
		en, _ := m["end"].(float64)
		sc, _ := m["score"].(float64)
		fmt.Printf("%d  %-7s  %-7s  %.2f\n", i+1, mmss(st), mmss(en), sc)
	}
}

// mmss 秒 → mm:ss.s 时间码（先按显示精度 0.1s 舍入，避免 59.96 显示成 0:60.0）。
func mmss(t float64) string {
	if t < 0 {
		t = 0
	}
	t = math.Round(t*10) / 10
	return fmt.Sprintf("%d:%04.1f", int(t)/60, math.Mod(t, 60))
}

func newAudioClipCmd() *cobra.Command {
	var (
		file     string
		start    float64
		end      float64
		fadeIn   float64
		fadeOut  float64
		loudness float64
		format   string
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "clip <file>",
		Short: "副歌切片导出：选区淡入淡出+响度归一，mp3/m4a/m4r 铃声",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			// start=0 合法（从开头切铃声），只拒负值；end>start 的合法性交后端 buildClipArgs
			if !c.Flags().Changed("start") || start < 0 {
				return fmt.Errorf("需提供 --start")
			}
			if !c.Flags().Changed("end") || end <= 0 {
				return fmt.Errorf("需提供 --end")
			}
			params := map[string]any{
				"start": start, "end": end,
				"fade_in": fadeIn, "fade_out": fadeOut, "loudness": loudness,
			}
			if format != "" {
				params["format"] = format
			}
			if o.out != "" {
				params["_out"] = o.out
			}
			return runToolSync(c, "audio", "clip", params, map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&start, "start", 0, "起点（秒），必填且不小于 0")
	cmd.Flags().Float64Var(&end, "end", 0, "终点（秒），必填、需大于起点，选区最长 600 秒")
	cmd.Flags().Float64Var(&fadeIn, "fade-in", 1, "淡入秒数（0=不淡入，超选区一半自动折半）")
	cmd.Flags().Float64Var(&fadeOut, "fade-out", 0.5, "淡出秒数（0=不淡出，超选区一半自动折半）")
	cmd.Flags().Float64Var(&loudness, "loudness", -14, "响度归一 LUFS（0=关，有效域 -70~-5）")
	cmd.Flags().StringVar(&format, "format", "mp3", "输出格式 mp3|m4a|m4r")
	// clip 不走 bindAudioOut（同 mix）：工具级 --format（mp3|m4a|m4r，固定 128k）与
	// 通用 --format 冲突，故只绑定同款 --out/--json，format 走上面的工具级参数。
	cmd.Flags().StringVar(&o.out, "out", "", "产物输出绝对路径（默认数据目录自动命名）")
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

func newAudioDuckCmd() *cobra.Command {
	var (
		depth    float64
		bgmGain  float64
		loudness float64
		format   string
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "duck <BGM> <人声>",
		Short: "口播闪避：说话时 BGM 自动压低，单滑杆深度",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := requireFile(args[0]); err != nil {
				return err
			}
			if err := requireFile(args[1]); err != nil {
				return err
			}
			params := map[string]any{
				"depth": depth, "bgm_gain": bgmGain, "loudness": loudness,
			}
			if format != "" {
				params["format"] = format
			}
			if o.out != "" {
				params["_out"] = o.out
			}
			// 输入序铁律（同 audiotool duck 滤镜图序）：audio=BGM 主输入、audio2=人声侧链，
			// 颠倒了被压的是人声本体。
			return runToolSync(c, "audio", "duck", params,
				map[string]string{"audio": args[0], "audio2": args[1]}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&depth, "depth", -12, "闪避深度 dB（-40~0，0=不压直通，绝对值越大说话时 BGM 压得越低；实际压深随语音密度过冲，约再加深 4~8dB，宁深勿浅）")
	cmd.Flags().Float64Var(&bgmGain, "bgm-gain", -6, "BGM 基线增益 dB（-40~6）")
	cmd.Flags().Float64Var(&loudness, "loudness", -14, "响度归一 LUFS（0=关，有效域 -70~-5）")
	cmd.Flags().StringVar(&format, "format", "mp3", "输出格式 mp3|wav")
	// duck 不走 bindAudioOut（同 mix/clip）：工具级 --format（mp3|wav，固定 128k）与
	// 通用 --format 冲突，故只绑定同款 --out/--json，format 走上面的工具级参数。
	cmd.Flags().StringVar(&o.out, "out", "", "产物输出绝对路径（默认数据目录自动命名）")
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

func newAudioPitchCmd() *cobra.Command {
	var (
		file      string
		semitones float64
		tempo     float64
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "pitch <file>",
		Short: "变调变速：乐调（半音）与节奏（速度倍率）双轴独立调整",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			params := o.params()
			params["semitones"] = semitones
			params["tempo"] = tempo
			return runToolSync(c, "audio", "pitch", params, map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&semitones, "semitones", 0, "变调半音数（-12~12，1 半音 = 1 个乐调）")
	cmd.Flags().Float64Var(&tempo, "tempo", 1, "速度倍率（0.25~4，1=不变速）")
	bindAudioOut(cmd.Flags(), o)
	return cmd
}

func newAudioAnalyzeCmd() *cobra.Command {
	var (
		file    string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "analyze <file>",
		Short: "调与 BPM 查询：分析调（key）、音阶、Camelot 编码与 BPM",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			return runToolSync(c, "audio", "analyze", nil, map[string]string{"audio": file}, "", jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

func newAudioEqCmd() *cobra.Command {
	var (
		file   string
		preset string
		gains  string
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "eq <file>",
		Short: "均衡器：10 段图示均衡（预设或自定义增益）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			params := o.params()
			params["preset"] = preset
			params["gains"] = gains
			return runToolSync(c, "audio", "equalizer", params, map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	cmd.Flags().StringVar(&preset, "preset", "flat", "预设: flat|pop|rock|jazz|classical|vocal|bass|treble|loudness")
	cmd.Flags().StringVar(&gains, "gains", "", "10 段增益逗号分隔（dB，±12），如 3,0,0,-2,0,1,2,3,4,4；非空覆盖 --preset")
	bindAudioOut(cmd.Flags(), o)
	return cmd
}

func newAudioVolumeCmd() *cobra.Command {
	var (
		file      string
		gainDB    float64
		normalize bool
		lufs      float64
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "volume <file>",
		Short: "音量与响度：增益微调或响度归一化到目标 LUFS",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			params := o.params()
			params["gain_db"] = gainDB
			params["normalize"] = normalize
			params["lufs"] = lufs
			return runToolSync(c, "audio", "volume", params, map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&gainDB, "gain-db", 0, "增益 dB（-30~12）")
	cmd.Flags().BoolVar(&normalize, "normalize", false, "响度归一化（loudnorm，覆盖增益）")
	cmd.Flags().Float64Var(&lufs, "lufs", -14, "目标响度 LUFS（-14 流媒体 / -16 播客 / -23 广播）")
	bindAudioOut(cmd.Flags(), o)
	return cmd
}

func newAudioFadeCmd() *cobra.Command {
	var (
		file    string
		fadeIn  float64
		fadeOut float64
		curve   string
	)
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "fade <file>",
		Short: "淡入淡出：片头淡入与片尾淡出",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			params := o.params()
			params["fade_in"] = fadeIn
			params["fade_out"] = fadeOut
			params["curve"] = curve
			return runToolSync(c, "audio", "fade", params, map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	cmd.Flags().Float64Var(&fadeIn, "fade-in", 0, "淡入秒数")
	cmd.Flags().Float64Var(&fadeOut, "fade-out", 0, "淡出秒数")
	cmd.Flags().StringVar(&curve, "curve", "linear", "曲线: linear|log|exp")
	bindAudioOut(cmd.Flags(), o)
	return cmd
}

func newAudioReverseCmd() *cobra.Command {
	var file string
	o := &audioOutFlags{}
	cmd := &cobra.Command{
		Use:   "reverse <file>",
		Short: "倒放：整段音频反向播放",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				file = args[0]
			}
			if err := requireFile(file); err != nil {
				return err
			}
			return runToolSync(c, "audio", "reverse", o.params(), map[string]string{"audio": file}, "", o.jsonOut)
		},
	}
	bindAudioOut(cmd.Flags(), o)
	return cmd
}
