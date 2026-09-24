package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newASRCommand() *cobra.Command {
	var (
		audioURL    string
		outPath     string
		srt         bool
		hotwords    string
		language    string
		version     string
		engine      string
		qwenModel   string
		diarization bool
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "asr <file>",
		Short: "语音识别：一句话识别（本地文件同步）与录音文件识别（URL 异步）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			file := ""
			if len(args) == 1 {
				file = args[0]
			}
			if file != "" && audioURL != "" {
				return fmt.Errorf("音频文件与 URL 只能提供其一")
			}
			if file == "" && audioURL == "" {
				return fmt.Errorf("请提供音频文件或 --url")
			}
			if engine != "volcengine" && engine != "qianwen" && engine != "xiaomi" && engine != "zhipu" {
				return fmt.Errorf("不支持的引擎 %q（可选 volcengine / qianwen / xiaomi / zhipu）", engine)
			}
			// 智谱仅提供短音频同步转写（≤30 秒，无版本概念），显式传 --version 视为参数冲突
			if engine == "zhipu" {
				if c.Flags().Changed("version") {
					return fmt.Errorf("参数冲突：--engine zhipu 不支持 --version（智谱仅提供短音频同步转写）")
				}
				if srt && c.Flags().Changed("srt") {
					return fmt.Errorf("参数冲突：--engine zhipu 无时间戳，不支持 --srt")
				}
				files, err := localAudioFile(file)
				if err != nil {
					return err
				}
				params := map[string]any{"prompt": "", "hotwords": hotwords}
				if audioURL != "" {
					params["url"] = audioURL
				}
				return runToolSync(c, "zhipu", "asr", params, files, outPath, jsonOut)
			}
			// 小米仅提供同步转写（无版本概念），显式传 --version 视为参数冲突
			if engine == "xiaomi" {
				if c.Flags().Changed("version") {
					return fmt.Errorf("参数冲突：--engine xiaomi 不支持 --version（小米仅提供同步转写）")
				}
				switch language {
				case "", "zh", "en":
				default:
					return fmt.Errorf("参数错误：--engine xiaomi 的识别语言仅支持 zh / en（留空自动）")
				}
				// 小米走 base64 直传：本地文件直读即可，无需对象存储中转
				files, err := localAudioFile(file)
				if err != nil {
					return err
				}
				params := map[string]any{"language": language}
				if audioURL != "" {
					params["url"] = audioURL
				}
				return runToolSync(c, "xiaomi", "asr", params, files, outPath, jsonOut)
			}
			// 千问仅提供录音文件转写（无多版本概念），显式传 --version 视为参数冲突
			if engine == "qianwen" {
				if c.Flags().Changed("version") {
					return fmt.Errorf("参数冲突：--engine qianwen 不支持 --version（千问仅提供录音文件转写）")
				}
				files, err := localAudioFile(file)
				if err != nil {
					return err
				}
				params := map[string]any{
					"srt": srt, "model": qwenModel,
					"language_hints": language, "diarization_enabled": diarization,
				}
				if audioURL != "" {
					params["url"] = audioURL
				}
				return runToolSync(c, "qianwen", "asr", params, files, outPath, jsonOut)
			}
			// 版本缺省按输入方式推断：本地文件 → 一句话识别；URL → 标准版
			if version == "" {
				if file != "" {
					version = "sentence"
				} else {
					version = "standard"
				}
			}
			switch version {
			case "sentence", "standard", "idle", "flash":
			default:
				return fmt.Errorf("识别版本 version 仅支持 sentence（一句话，本地文件）/ standard（录音文件识别）/ idle / flash")
			}
			// 文件+URL 版本的组合交给工具层裁决：配置了对象存储时自动中转走对应版本，
			// 未配置时 standard 降级一句话、idle/flash 给出明确的设置指引。
			if audioURL != "" && version == "sentence" {
				return fmt.Errorf("一句话识别仅支持本地音频文件；URL 请使用 --version standard / idle / flash")
			}

			files, err := localAudioFile(file)
			if err != nil {
				return err
			}

			params := map[string]any{"hotwords": hotwords, "language": language, "srt": srt, "version": version}
			if audioURL != "" {
				params["url"] = audioURL
			}
			return runToolSync(c, "volcengine", "asr", params, files, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&audioURL, "url", "", "公网音频 URL（与位置参数二选一）")
	f.StringVar(&outPath, "out", "", "转写文本输出路径（默认数据目录自动命名）")
	f.BoolVar(&srt, "srt", true, "额外产出 SRT 字幕（--srt=false 关闭；火山/千问生效，小米无时间戳不产 SRT）")
	f.StringVar(&engine, "engine", "volcengine", "识别引擎: volcengine（火山引擎）| qianwen（千问，仅录音文件转写）| xiaomi（小米，同步转写 mp3/wav ≤7.5MB）| zhipu（智谱，短音频同步转写 wav/mp3 ≤25MB/30 秒）")
	f.StringVar(&qwenModel, "qwen-model", "qwen3-asr-flash-filetrans", "千问转写模型: qwen3-asr-flash-filetrans|qwen-audio-3.1-asr-flash-filetrans（仅 --engine qianwen 生效）")
	f.BoolVar(&diarization, "diarization", false, "说话人分离（仅千问引擎；≤2 小时且单声道音频）")
	f.StringVar(&hotwords, "hotwords", "", "热词，逗号分隔（原样透传）")
	f.StringVar(&language, "language", "", "识别语言（留空自动识别中文/英文/常见方言；可选 zh-CN/en-US/ja-JP/yue-CN 等 25 种；--engine qianwen 时取值 zh/en/ja/ko/de/fr/ru/es/pt/it，留空自动；--engine xiaomi 时取值 zh/en，留空自动）")
	f.StringVar(&version, "version", "", "识别版本（缺省按输入推断：本地文件→sentence，URL→standard）：sentence 一句话识别（本地文件，同步秒级）/ standard 标准版（录音文件识别）/ idle 闲时版（24h 内完成）/ flash 极速版（秒级）；本地文件+standard/idle/flash 需配置对象存储中转")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}

// localAudioFile 校验本地音频文件并包装为任务文件输入（key 与 Tool 约定一致），空文件名返回 nil。
func localAudioFile(file string) (map[string]string, error) {
	if file == "" {
		return nil, nil
	}
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("音频文件不存在: %s", file)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("解析音频文件路径失败: %w", err)
	}
	return map[string]string{"audio": abs}, nil
}
