package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newTTSStreamCommand() *cobra.Command {
	var (
		textFile         string
		voice            string
		format           string
		sampleRate       int
		speechRate       int
		loudnessRate     int
		pitch            int
		bitRate          int
		silenceDuration  int
		subtitle         bool
		aigcWatermark    bool
		toneFidelity     bool
		resource         string
		model            string
		explicitLanguage string
		explicitDialect  string
		contextText      string
		outPath          string
		jsonOut          bool
	)
	cmd := &cobra.Command{
		Use:   "tts-stream <text>",
		Short: "流式语音合成：单向流式低延迟合成（seed-tts-2.0）",
		Long: `单向流式语音合成（HTTP Chunked）：一次性输入文本，流式返回音频。
支持 20 语种、8 种方言、字级时间戳字幕与语音指令；音频分片到齐后落盘。`,
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
			if c.Flags().Changed("silence-duration") {
				params["silence_duration"] = silenceDuration
			}
			if subtitle {
				params["subtitle"] = true
			}
			if aigcWatermark {
				params["aigc_watermark"] = true
			}
			if toneFidelity {
				params["tone_fidelity"] = true
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
			if explicitDialect != "" {
				params["explicit_dialect"] = explicitDialect
			}
			if contextText != "" {
				params["context_text"] = contextText
			}
			return runToolSync(c, "volcengine", "tts_stream", params, nil, outPath, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&textFile, "file", "", "从文件读取文本")
	f.StringVar(&voice, "voice", "zh_female_vv_uranus_bigtts", "音色 ID（2.0/复刻音色）")
	f.StringVar(&format, "format", "mp3", "音频格式: mp3|pcm|ogg_opus|wav")
	f.IntVar(&sampleRate, "sample-rate", 24000, "采样率 Hz: 8000/16000/22050/24000/32000/44100/48000")
	f.IntVar(&speechRate, "speech-rate", 0, "语速 -50-100（100=2 倍速）")
	f.IntVar(&loudnessRate, "loudness-rate", 0, "音量 -50-100（100=2 倍音量）")
	f.IntVar(&pitch, "pitch", 0, "音调 -12-12")
	f.IntVar(&bitRate, "bit-rate", 0, "比特率 bps: 64000/160000（wav/pcm 不支持）")
	f.IntVar(&silenceDuration, "silence-duration", 0, "文本末尾静音 ms（0-30000）")
	f.BoolVar(&subtitle, "subtitle", false, "开启字级时间戳，聚合产出 SRT 字幕（仅中英）")
	f.BoolVar(&aigcWatermark, "aigc-watermark", false, "音频结尾添加 AIGC 节奏标识")
	f.BoolVar(&toneFidelity, "tone-fidelity", false, "还原模式：尽量复刻训练音频音色风格（仅复刻音色）")
	f.StringVar(&resource, "resource", "seed-tts-2.0", "资源: seed-tts-2.0（普通音色）| seed-icl-2.0（复刻音色）")
	f.StringVar(&model, "model", "", "复刻模型版本（仅复刻音色需指定；指定后不支持语音指令）")
	f.StringVar(&explicitLanguage, "explicit-language", "", "朗读语种: zh-cn|en|ja|ko|de|fr|ru 等 20 种")
	f.StringVar(&explicitDialect, "explicit-dialect", "", "方言: beijing|dongbei|henan|shaanxi|shanghai|sichuan|tianjin|yue")
	f.StringVar(&contextText, "context-text", "", "语音指令（仅 2.0 音色，不参与计费）")
	f.StringVar(&outPath, "out", "", "产物输出路径（默认数据目录自动命名）")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	return cmd
}
