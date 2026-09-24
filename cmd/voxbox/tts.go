package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/task"
)

func newTTSCommand() *cobra.Command {
	var (
		textFile     string
		voice        string
		format       string
		speedRatio   float64
		volumeRatio  float64
		engine       string
		qwenModel    string
		mimoModel    string
		instructions string
		outPath      string
		jsonOut      bool
	)
	cmd := &cobra.Command{
		Use:   "tts <text>",
		Short: "语音合成：文本转语音",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			text := ""
			if len(args) == 1 {
				text = args[0]
			}
			if textFile != "" {
				raw, err := os.ReadFile(textFile)
				if err != nil {
					return fmt.Errorf("读取文本文件失败: %w", err)
				}
				text = string(raw)
			}
			// 各引擎音色默认值不同：用户显式传 --voice 则尊重其选择
			if !c.Flags().Changed("voice") {
				switch engine {
				case "qianwen":
					voice = "Cherry"
				case "xiaomi":
					voice = "" // 后端回落官方默认 mimo_default
				}
			}
			var params map[string]any
			switch engine {
			case "volcengine":
				params = map[string]any{"text": text, "voice": voice, "format": format,
					"speed_ratio": speedRatio, "volume_ratio": volumeRatio}
			case "qianwen":
				// 千问无 format 请求参数，产物格式由上游音频 URL 实际容器决定
				params = map[string]any{"text": text, "voice": voice,
					"model": qwenModel, "instructions": instructions}
			case "xiaomi":
				// 小米 format 走 audio.format（wav|mp3）；voice 仅预置音色模型生效，
				// voicedesign 由 --instructions 音色描述生成（tool 层校验必填）
				params = map[string]any{"text": text, "voice": voice, "format": format,
					"model": mimoModel, "instructions": instructions}
			default:
				return fmt.Errorf("不支持的引擎 %q（可选 volcengine / qianwen / xiaomi）", engine)
			}
			return runToolSync(c, engine, "tts", params, nil, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&textFile, "file", "", "从文件读取文本")
	f.StringVar(&engine, "engine", "volcengine", "合成引擎: volcengine（火山引擎）| qianwen（千问）| xiaomi（小米 MiMo）")
	f.StringVar(&voice, "voice", "zh_female_cancan_mars_bigtts", "音色 ID（--engine qianwen 默认 Cherry，可选 48 官方音色；--engine xiaomi 默认 mimo_default，可选 9 官方预置音色）")
	f.StringVar(&format, "format", "mp3", "音频格式: mp3|wav|pcm|ogg_opus（火山全支持；小米仅 wav|mp3；千问由上游决定）")
	f.StringVar(&qwenModel, "qwen-model", "qwen3-tts-flash", "千问模型: qwen3-tts-flash|qwen3-tts-instruct-flash（仅 --engine qianwen 生效）")
	f.StringVar(&mimoModel, "mimo-model", "mimo-v2.5-tts", "小米模型: mimo-v2.5-tts|mimo-v2.5-tts-voicedesign（仅 --engine xiaomi 生效）")
	f.StringVar(&instructions, "instructions", "", "风格指令（千问 instruct 模型生效；小米全系生效，voicedesign 模型下为音色描述必填）")
	f.Float64Var(&speedRatio, "speed-ratio", 1.0, "语速 0.2-3.0")
	f.Float64Var(&volumeRatio, "volume-ratio", 1.0, "音量 0.2-3.0")
	f.StringVar(&outPath, "out", "", "产物输出路径（默认数据目录自动命名）")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

// runToolSync 同步执行工具：CLI 共享入口，处理 --out 重定位与 JSON/退出码。
// files 为任务文件输入（如 asr 的本地音频，key 与 Tool 约定一致），无文件传 nil。
func runToolSync(c *cobra.Command, providerName, toolName string, params map[string]any, files map[string]string, outPath string, jsonOut bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	svc, err := service.New(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()
	// 人类模式订阅引擎事件，progress 打到 stderr（\r 原地刷新）；--json 保持静默（stdout 纯 JSON）。
	notify := func(e task.Event) {
		if e.Type == "progress" {
			eprintf("\r[%s] %s %d%%", toolName, e.Note, e.Progress)
		}
	}
	if jsonOut {
		notify = nil
	}
	svc.StartEngine(notify, 1)

	if outPath != "" {
		params["_out"] = outPath // provider 侧支持 _out 参数指定产物绝对路径
	}
	result, err := runToolCore(c.Context(), svc, providerName, toolName, params, files)
	// 进度行以 \r 原地刷新且无结尾换行，终态输出前补一个换行分开两行（--json 模式无进度输出，跳过）。
	if !jsonOut {
		eprintf("\n")
	}
	if err != nil {
		eprintf("错误: %v\n", err)
		os.Exit(exitCodeFor(err))
	}
	if jsonOut {
		printJSON(result)
	} else {
		eprintf("完成，耗时 %dms\n", result.CostMS)
		for _, a := range result.Artifacts {
			fmt.Printf("%s: %s\n", a.Kind, a.Path)
		}
	}
	return nil
}
