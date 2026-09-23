package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newTTSLongCommand() *cobra.Command {
	var (
		textFile         string
		voice            string
		format           string
		sampleRate       int
		speechRate       int
		loudnessRate     int
		pitch            int
		bitRate          int
		timestamps       bool
		aigcWatermark    bool
		resource         string
		model            string
		explicitLanguage string
		outPath          string
		jsonOut          bool
	)
	cmd := &cobra.Command{
		Use:   "tts-long <text>",
		Short: "长文本语音合成：10 万字以内异步合成（seed-tts-2.0）",
		Long: `长文本语音合成：提交异步任务后轮询产出音频，支持分句时间戳与 SRT 字幕。
大段文本建议用 --file 从文件读取；合成耗时与文本量正相关（分钟级）。`,
		Args: cobra.MaximumNArgs(1),
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
			params := map[string]any{"text": text}
			if voice != "" {
				params["voice"] = voice
			}
			if format != "" {
				params["format"] = format
			}
			if c.Flags().Changed("sample-rate") {
				params["sample_rate"] = sampleRate
			}
			if c.Flags().Changed("speech-rate") {
				params["speech_rate"] = speechRate
			}
			if c.Flags().Changed("loudness-rate") {
				params["loudness_rate"] = loudnessRate
			}
			if c.Flags().Changed("pitch") {
				params["pitch"] = pitch
			}
			if c.Flags().Changed("bit-rate") {
				params["bit_rate"] = bitRate
			}
			if timestamps {
				params["timestamps"] = true
			}
			if aigcWatermark {
				params["aigc_watermark"] = true
			}
			if resource != "" {
				params["resource"] = resource
			}
			if model != "" {
				params["model"] = model
			}
			if explicitLanguage != "" {
				params["explicit_language"] = explicitLanguage
			}
			return runToolSync(c, "volcengine", "tts_long", params, nil, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&textFile, "file", "", "从文件读取文本（大段文本推荐）")
	f.StringVar(&voice, "voice", "zh_female_vv_uranus_bigtts", "音色 ID（2.0/复刻音色）")
	f.StringVar(&format, "format", "mp3", "音频格式: mp3|pcm|ogg_opus")
	f.IntVar(&sampleRate, "sample-rate", 24000, "采样率 Hz: 8000/16000/22050/24000/32000/44100/48000")
	f.IntVar(&speechRate, "speech-rate", 0, "语速 -50-100（100=2 倍速）")
	f.IntVar(&loudnessRate, "loudness-rate", 0, "音量 -50-100（100=2 倍音量）")
	f.IntVar(&pitch, "pitch", 0, "音调 -12-12")
	f.IntVar(&bitRate, "bit-rate", 0, "比特率 bps: 64000/160000（pcm 不支持）")
	f.BoolVar(&timestamps, "timestamps", false, "开启时间戳，额外产出 SRT 字幕")
	f.BoolVar(&aigcWatermark, "aigc-watermark", false, "音频结尾添加 AIGC 节奏标识")
	f.StringVar(&resource, "resource", "seed-tts-2.0", "资源: seed-tts-2.0（普通音色）| seed-icl-2.0（复刻音色）")
	f.StringVar(&model, "model", "", "复刻模型版本（仅复刻音色需指定）")
	f.StringVar(&explicitLanguage, "explicit-language", "", "朗读语种: zh-cn|en|es-mx|id|pt-br")
	f.StringVar(&outPath, "out", "", "产物输出路径（默认数据目录自动命名）")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}
