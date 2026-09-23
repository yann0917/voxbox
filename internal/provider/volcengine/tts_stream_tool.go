package volcengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// TTSStreamTool 单向流式语音合成工具（火山 HTTP Chunked，seed-tts-2.0 资源）。
// 定位：2.0 模型低延迟合成，20 语种 + 8 方言 + 字级时间戳字幕 + 语音指令。
type TTSStreamTool struct {
	client *TTSStreamClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSStreamTool(cred SpeechCred, outDir string) *TTSStreamTool {
	return &TTSStreamTool{client: NewTTSStreamClient(cred), cred: cred, outDir: outDir}
}

// NewTTSStreamToolWithBaseURL 供测试注入 mock 地址。
func NewTTSStreamToolWithBaseURL(cred SpeechCred, outDir, baseURL string) *TTSStreamTool {
	return &TTSStreamTool{client: NewTTSStreamClientWithBaseURL(cred, baseURL), cred: cred, outDir: outDir}
}

func (t *TTSStreamTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "tts_stream",
		Title:       "流式语音合成",
		Description: "单向流式合成（seed-tts-2.0），低延迟返回，支持 20 语种、8 方言、字级时间戳字幕与语音指令",
		Group:       "语音",
	}
}

// ttsStreamLanguages 单向流式支持语种（官方文档 6561/2528925，20 种）。
var ttsStreamLanguages = []provider.ParamOption{
	{Value: "", Label: "不指定"},
	{Value: "zh-cn", Label: "中文（中英混读）"},
	{Value: "en", Label: "英语"},
	{Value: "ja", Label: "日语"},
	{Value: "es-mx", Label: "墨西哥语"},
	{Value: "id", Label: "印尼语"},
	{Value: "pt-br", Label: "巴西葡萄牙语"},
	{Value: "pt", Label: "葡萄牙语"},
	{Value: "ko", Label: "韩语"},
	{Value: "it", Label: "意大利语"},
	{Value: "de", Label: "德语"},
	{Value: "fr", Label: "法语"},
	{Value: "th", Label: "泰语"},
	{Value: "vi", Label: "越南语"},
	{Value: "ru", Label: "俄语"},
	{Value: "fil", Label: "菲律宾语"},
	{Value: "ms", Label: "马来语"},
	{Value: "ar", Label: "阿拉伯语"},
	{Value: "pl", Label: "波兰语"},
	{Value: "tr", Label: "土耳其语"},
	{Value: "sv", Label: "瑞典语"},
}

// ttsStreamDialects 支持方言（speaker 需为支持方言的音色）。
var ttsStreamDialects = []provider.ParamOption{
	{Value: "", Label: "不指定"},
	{Value: "beijing", Label: "北京话"},
	{Value: "dongbei", Label: "东北话"},
	{Value: "henan", Label: "河南话"},
	{Value: "shaanxi", Label: "陕西话"},
	{Value: "shanghai", Label: "上海话"},
	{Value: "sichuan", Label: "四川话"},
	{Value: "tianjin", Label: "天津话"},
	{Value: "yue", Label: "粤语"},
}

func (t *TTSStreamTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "voice", Label: "音色", Type: provider.ParamString,
			Default: ttsLongDefaultVoice, Group: "参数",
			Placeholder: "2.0/复刻音色 ID，用 voxbox voices list 查询"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum,
			Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "mp3", Label: "MP3"}, {Value: "pcm", Label: "PCM（流式推荐）"},
				{Value: "ogg_opus", Label: "OGG Opus"}, {Value: "wav", Label: "WAV（不建议）"},
			}},
		{Key: "sample_rate", Label: "采样率", Type: provider.ParamEnum,
			Default: "24000", Options: ttsLongSampleRates, Group: "参数"},
		{Key: "speech_rate", Label: "语速 (-50-100，100=2 倍速)", Type: provider.ParamInt,
			Default: 0, Group: "参数"},
		{Key: "loudness_rate", Label: "音量 (-50-100，100=2 倍音量)", Type: provider.ParamInt,
			Default: 0, Group: "参数"},
		{Key: "subtitle", Label: "字级时间戳（产出 SRT 字幕，仅中英）", Type: provider.ParamBool,
			Default: false, Group: "输出"},
		{Key: "resource", Label: "资源（普通/复刻音色）", Type: provider.ParamEnum,
			Default: ttsLongResourceSeed, Group: "高级",
			Options: []provider.ParamOption{
				{Value: ttsLongResourceSeed, Label: "seed-tts-2.0（普通音色）"},
				{Value: ttsLongResourceICL, Label: "seed-icl-2.0（复刻音色）"},
			}},
		{Key: "model", Label: "复刻模型版本", Type: provider.ParamString,
			Placeholder: "仅复刻音色需指定；指定后不支持语音指令", Group: "高级"},
		{Key: "explicit_language", Label: "朗读语种", Type: provider.ParamEnum,
			Options: ttsStreamLanguages, Group: "高级"},
		{Key: "explicit_dialect", Label: "方言", Type: provider.ParamEnum,
			Options: ttsStreamDialects, Group: "高级"},
		{Key: "pitch", Label: "音调 (-12-12)", Type: provider.ParamInt, Default: 0, Group: "高级"},
		{Key: "bit_rate", Label: "比特率 (bps)", Type: provider.ParamEnum, Group: "高级",
			Options: []provider.ParamOption{
				{Value: "", Label: "默认"},
				{Value: "64000", Label: "64000"},
				{Value: "160000", Label: "160000"},
			}},
		{Key: "silence_duration", Label: "末尾静音 (ms，0-30000)", Type: provider.ParamInt,
			Default: 0, Group: "高级"},
		{Key: "context_text", Label: "语音指令", Type: provider.ParamString,
			Placeholder: "如：你可以用特别痛心的语气说话吗", Group: "高级"},
		{Key: "tone_fidelity", Label: "还原模式（仅复刻音色）", Type: provider.ParamBool,
			Default: false, Group: "高级"},
		{Key: "aigc_watermark", Label: "AIGC 生成标识", Type: provider.ParamBool,
			Default: false, Group: "高级"},
	}
}

func (t *TTSStreamTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（退出码 2），不被凭证校验（退出码 4）掩盖。
	text, _ := in.Params["text"].(string)
	text = strings.TrimRight(text, "\n")
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if err := t.cred.Validate(); err != nil {
		return provider.TaskOutput{}, err
	}

	format := paramString(in.Params, "format")
	if format == "" {
		format = "mp3"
	}
	switch format {
	case "mp3", "pcm", "ogg_opus", "wav":
	default:
		return provider.TaskOutput{}, fmt.Errorf("不支持的音频格式 %s（流式合成仅支持 mp3/pcm/ogg_opus/wav）", format)
	}
	sampleRate := toInt(in.Params["sample_rate"], 24000)
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	if format == "ogg_opus" {
		sampleRate = 48000 // 官方约束：ogg_opus 仅支持 48000
	} else if !ttsLongValidSampleRates[sampleRate] {
		return provider.TaskOutput{}, fmt.Errorf("不支持的采样率 %d（可选 8000/16000/22050/24000/32000/44100/48000）", sampleRate)
	}
	bitRate := toInt(in.Params["bit_rate"], 0)
	if format == "wav" || format == "pcm" {
		if bitRate != 0 {
			return provider.TaskOutput{}, fmt.Errorf("%s 格式不支持指定比特率，请留空", format)
		}
	}

	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = ttsLongDefaultVoice
	}
	resource := paramString(in.Params, "resource")
	if resource == "" {
		resource = ttsLongResourceSeed
	}

	req := TTSStreamSubmitReq{
		Text: text, Speaker: voice, Resource: resource,
		Model:  paramString(in.Params, "model"),
		Format: format, SampleRate: sampleRate, BitRate: bitRate,
		SpeechRate:       toInt(in.Params["speech_rate"], 0),
		LoudnessRate:     toInt(in.Params["loudness_rate"], 0),
		EnableSubtitle:   paramBool(in.Params, "subtitle", "enable_subtitle"),
		ExplicitLanguage: paramString(in.Params, "explicit_language"),
		ExplicitDialect:  paramString(in.Params, "explicit_dialect"),
		Pitch:            toInt(in.Params["pitch"], 0),
		SilenceDuration:  toInt(in.Params["silence_duration"], 0),
		ContextText:      paramString(in.Params, "context_text"),
		ToneFidelity:     paramBool(in.Params, "tone_fidelity"),
		AIGCWatermark:    paramBool(in.Params, "aigc_watermark"),
	}

	report(10, "建立流式连接", nil)
	result, err := t.client.SynthesizeStream(ctx, req, func(chunks, _ int) {
		progress := 30 + chunks*5
		if progress > 90 {
			progress = 90
		}
		report(progress, fmt.Sprintf("接收音频分片（已收 %d 片）", chunks), map[string]any{"chunks": chunks})
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if len(result.Audio) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("流式合成完成但未返回音频数据")
	}
	report(95, "保存音频文件", nil)
	return t.saveArtifacts(in, text, result, format)
}

// saveArtifacts 落盘音频（tts_stream/<uuid>.<ext>）与可选 SRT 字幕，按 M2 契约处理 _out 重定向。
func (t *TTSStreamTool) saveArtifacts(in provider.TaskInput, text string, result TTSStreamResult, format string) (provider.TaskOutput, error) {
	reqID := uuid.NewString()
	audioPath := filepath.Join("tts_stream", reqID+"."+extOf(format))
	// _out 参数（CLI --out）重定向产物路径；不进 ParamSpecs，属机器约定。
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		audioPath = outParam
	}
	srtPath := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".srt"

	audioAbs := audioPath
	if !filepath.IsAbs(audioAbs) {
		audioAbs = filepath.Join(t.outDir, audioPath)
		audioPath, _ = filepath.Rel(t.outDir, audioAbs)
	}
	if err := os.MkdirAll(filepath.Dir(audioAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(audioAbs, result.Audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}

	arts := []provider.Artifact{{
		Kind: "audio", Path: audioPath, Format: format,
		Size: int64(len(result.Audio)), DurationMS: lastWordEndMS(result.Words),
	}}

	// 字级时间戳按句末标点聚合为句级 SRT（字级条目过碎，不适合字幕阅读）。
	if paramBool(in.Params, "subtitle", "enable_subtitle") && len(result.Words) > 0 {
		segs := aggregateSubtitleSegments(result.Words)
		if len(segs) > 0 {
			srtContent := BuildSRT(segs)
			srtAbs := srtPath
			if !filepath.IsAbs(srtAbs) {
				srtAbs = filepath.Join(t.outDir, srtPath)
				srtPath, _ = filepath.Rel(t.outDir, srtAbs)
			}
			if err := os.WriteFile(srtAbs, []byte(srtContent), 0o644); err != nil {
				return provider.TaskOutput{}, fmt.Errorf("写入 SRT 字幕失败: %w", err)
			}
			arts = append(arts, provider.Artifact{
				Kind: "subtitle", Path: srtPath, Format: "srt", Size: int64(len(srtContent)),
			})
		}
	}

	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"char_count":   utf8.RuneCountInString(text),
			"billed_chars": result.BilledWords,
			"chunks":       result.Chunks,
			"duration_ms":  lastWordEndMS(result.Words),
		},
	}, nil
}

// lastWordEndMS 取最后一个字的结束时间作为音频时长（未开启字幕时为 0）。
func lastWordEndMS(words []TTSStreamWord) int64 {
	if len(words) == 0 {
		return 0
	}
	return words[len(words)-1].EndMS
}

// ttsSentenceEndPuncts 句末标点：字文本包含任一即视为一句结束。
const ttsSentenceEndPuncts = "。！？；…!?;"

// aggregateSubtitleSegments 把字级时间戳按句末标点聚合成句级段（供 BuildSRT）。
// 无句末标点的残余尾部也成一句；无任何字时返回空。
func aggregateSubtitleSegments(words []TTSStreamWord) []ASRSegment {
	segs := make([]ASRSegment, 0)
	var (
		text    strings.Builder
		startMS int64
		endMS   int64
		opened  bool
	)
	flush := func() {
		if !opened {
			return
		}
		segs = append(segs, ASRSegment{Text: strings.TrimSpace(text.String()), StartMS: startMS, EndMS: endMS})
		text.Reset()
		opened = false
	}
	for _, w := range words {
		if !opened {
			startMS = w.StartMS
			opened = true
		}
		text.WriteString(w.Word)
		endMS = w.EndMS
		if strings.ContainsAny(w.Word, ttsSentenceEndPuncts) {
			flush()
		}
	}
	flush()
	return segs
}
