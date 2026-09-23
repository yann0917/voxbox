package volcengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// 长文本合成轮询节奏（var 便于测试注入更短间隔）：起步间隔指数退避、退避上限、总超时。
// 官方未给出合成时长 SLA，10 万字符按分钟级估算，超时放宽到 1 小时兜底。
var (
	ttsLongPollInterval = 2 * time.Second
	ttsLongPollMax      = 15 * time.Second
	ttsLongPollTimeout  = time.Hour
)

const (
	// ttsLongMaxLength 单次提交文本上限（官方：最大 10 万字符）。
	ttsLongMaxLength = 100000
	// ttsLongMaxIllegalRatio 非法字符（ASCII 控制字符，不含 \t \n）占比上限，超过服务端拒绝执行。
	ttsLongMaxIllegalRatio = 0.10
	// ttsLongDefaultVoice 默认音色（2.0 音色，官方文档示例同款）。
	ttsLongDefaultVoice = "zh_female_vv_uranus_bigtts"
	// ttsLongMaxQueryErrors 轮询期间容忍的连续查询失败次数（长任务对瞬时网络抖动更敏感）。
	ttsLongMaxQueryErrors = 3
)

// TTSLongTool 长文本语音合成工具（火山异步 submit/query，seed-tts-2.0 资源）。
// 与同步 tts 工具并存：参数语义不同代际（speech_rate -50~100 vs speed_ratio 0.2-3.0），
// 格式集不同（无 wav），且天然支持 10 万字符与分句时间戳。
type TTSLongTool struct {
	client *TTSLongClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSLongTool(cred SpeechCred, outDir string) *TTSLongTool {
	return &TTSLongTool{client: NewTTSLongClient(cred), cred: cred, outDir: outDir}
}

// NewTTSLongToolWithBaseURL 供测试注入 mock 地址。
func NewTTSLongToolWithBaseURL(cred SpeechCred, outDir, baseURL string) *TTSLongTool {
	return &TTSLongTool{client: NewTTSLongClientWithBaseURL(cred, baseURL), cred: cred, outDir: outDir}
}

func (t *TTSLongTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "tts_long",
		Title:       "长文本语音合成",
		Description: "10 万字以内长文本异步合成（seed-tts-2.0），支持分句时间戳与 SRT 字幕",
		Group:       "语音",
	}
}

// ttsLongSampleRates 采样率枚举（ogg_opus 仅 48000）。
var ttsLongSampleRates = []provider.ParamOption{
	{Value: "8000", Label: "8000 Hz"}, {Value: "16000", Label: "16000 Hz"},
	{Value: "22050", Label: "22050 Hz"}, {Value: "24000", Label: "24000 Hz（默认）"},
	{Value: "32000", Label: "32000 Hz"}, {Value: "44100", Label: "44100 Hz"},
	{Value: "48000", Label: "48000 Hz"},
}

// ttsLongValidSampleRates 采样率合法性（官方文档枚举），CLI 自由输入时拦截。
var ttsLongValidSampleRates = map[int]bool{
	8000: true, 16000: true, 22050: true, 24000: true,
	32000: true, 44100: true, 48000: true,
}

func (t *TTSLongTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的长文本（≤100000 字符）", Group: "内容"},
		{Key: "voice", Label: "音色", Type: provider.ParamString,
			Default: ttsLongDefaultVoice, Group: "参数",
			Placeholder: "2.0/复刻音色 ID，用 voxbox voices list 查询"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum,
			Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "mp3", Label: "MP3"}, {Value: "pcm", Label: "PCM"},
				{Value: "ogg_opus", Label: "OGG Opus"},
			}},
		{Key: "sample_rate", Label: "采样率", Type: provider.ParamEnum,
			Default: "24000", Options: ttsLongSampleRates, Group: "参数"},
		{Key: "speech_rate", Label: "语速 (-50-100，100=2 倍速)", Type: provider.ParamInt,
			Default: 0, Group: "参数"},
		{Key: "loudness_rate", Label: "音量 (-50-100，100=2 倍音量)", Type: provider.ParamInt,
			Default: 0, Group: "参数"},
		{Key: "timestamps", Label: "生成时间戳（产出 SRT 字幕）", Type: provider.ParamBool,
			Default: false, Group: "输出"},
		{Key: "resource", Label: "资源（普通/复刻音色）", Type: provider.ParamEnum,
			Default: ttsLongResourceSeed, Group: "高级",
			Options: []provider.ParamOption{
				{Value: ttsLongResourceSeed, Label: "seed-tts-2.0（普通音色）"},
				{Value: ttsLongResourceICL, Label: "seed-icl-2.0（复刻音色）"},
			}},
		{Key: "model", Label: "复刻模型版本", Type: provider.ParamString,
			Placeholder: "仅复刻音色需指定（req_params.model）", Group: "高级"},
		{Key: "explicit_language", Label: "朗读语种", Type: provider.ParamEnum, Group: "高级",
			Options: []provider.ParamOption{
				{Value: "", Label: "不指定"},
				{Value: "zh-cn", Label: "中文（中英混读）"},
				{Value: "en", Label: "英语"},
				{Value: "es-mx", Label: "墨西哥语"},
				{Value: "id", Label: "印尼语"},
				{Value: "pt-br", Label: "巴西葡萄牙语"},
			}},
		{Key: "pitch", Label: "音调 (-12-12)", Type: provider.ParamInt, Default: 0, Group: "高级"},
		{Key: "bit_rate", Label: "比特率 (bps)", Type: provider.ParamEnum, Group: "高级",
			Options: []provider.ParamOption{
				{Value: "", Label: "默认"},
				{Value: "64000", Label: "64000"},
				{Value: "160000", Label: "160000"},
			}},
		{Key: "aigc_watermark", Label: "AIGC 生成标识", Type: provider.ParamBool,
			Default: false, Group: "高级"},
	}
}

func (t *TTSLongTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（退出码 2），不被凭证校验（退出码 4）掩盖。
	text, _ := in.Params["text"].(string)
	text = strings.TrimRight(text, "\n") // 容忍结尾换行（--file 读入常见），不占字符配额
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if err := validateLongText(text); err != nil {
		return provider.TaskOutput{}, err
	}
	if err := t.cred.Validate(); err != nil {
		return provider.TaskOutput{}, err
	}

	format := paramString(in.Params, "format")
	if format == "" {
		format = "mp3"
	}
	switch format {
	case "mp3", "pcm", "ogg_opus":
	default:
		return provider.TaskOutput{}, fmt.Errorf("不支持的音频格式 %s（长文本合成仅支持 mp3/pcm/ogg_opus）", format)
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
	if format == "pcm" && bitRate != 0 {
		return provider.TaskOutput{}, fmt.Errorf("pcm 格式不支持指定比特率，请留空")
	}

	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = ttsLongDefaultVoice
	}
	resource := paramString(in.Params, "resource")
	if resource == "" {
		resource = ttsLongResourceSeed
	}
	speechRate := toInt(in.Params["speech_rate"], 0)
	loudnessRate := toInt(in.Params["loudness_rate"], 0)
	pitch := toInt(in.Params["pitch"], 0)
	timestamps := paramBool(in.Params, "timestamps", "enable_timestamp")

	req := TTSLongSubmitReq{
		Text: text, Speaker: voice, Resource: resource,
		Model:  paramString(in.Params, "model"),
		Format: format, SampleRate: sampleRate, BitRate: bitRate,
		SpeechRate: speechRate, LoudnessRate: loudnessRate,
		EnableTimestamp:  timestamps,
		ExplicitLanguage: paramString(in.Params, "explicit_language"),
		Pitch:            pitch,
		AIGCWatermark:    paramBool(in.Params, "aigc_watermark"),
	}

	report(5, "提交长文本合成任务", nil)
	taskID, err := t.client.Submit(ctx, req)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(15, "任务已提交，等待合成", map[string]any{"task_id": taskID})

	result, err := t.poll(ctx, taskID, resource, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if result.AudioURL == "" {
		return provider.TaskOutput{}, fmt.Errorf("任务已完成但未返回音频下载链接（任务 %s）", taskID)
	}
	report(92, "下载合成音频", nil)
	audio, err := t.client.Download(ctx, result.AudioURL)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	return t.saveArtifacts(in, text, taskID, result, audio, format, timestamps)
}

// poll 轮询任务直到 Success/Failure：起步 interval 指数退避至 max，总时长兜底；
// 连续 ttsLongMaxQueryErrors 次查询失败视为不可恢复（瞬时抖动不终断长任务）。
func (t *TTSLongTool) poll(ctx context.Context, taskID, resource string, report provider.ProgressReporter) (TTSLongQueryResult, error) {
	deadline := time.Now().Add(ttsLongPollTimeout)
	interval := ttsLongPollInterval
	consecutiveErrs := 0
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return TTSLongQueryResult{}, fmt.Errorf("长文本合成已取消（上游任务 %s 仍会在服务端保留，音频保留 7 天）: %w", taskID, err)
		}
		if time.Now().After(deadline) {
			return TTSLongQueryResult{}, fmt.Errorf("等待长文本合成结果超时（1 小时，上游任务 %s，音频在服务端保留 7 天）", taskID)
		}
		result, err := t.client.Query(ctx, taskID, resource)
		if err != nil {
			consecutiveErrs++
			if consecutiveErrs >= ttsLongMaxQueryErrors {
				return TTSLongQueryResult{}, fmt.Errorf("查询任务连续 %d 次失败: %w", consecutiveErrs, err)
			}
			report(15, fmt.Sprintf("查询瞬时失败（%d/%d），继续等待", consecutiveErrs, ttsLongMaxQueryErrors), nil)
		} else {
			consecutiveErrs = 0
			switch result.Status {
			case "Success":
				return result, nil
			case "Failure":
				msg := result.Message
				if msg == "" || msg == "OK" {
					msg = "未知原因"
				}
				return TTSLongQueryResult{}, fmt.Errorf("上游合成失败（任务 %s）: %s", taskID, msg)
			default:
				progress := 30 + n*2
				if progress > 90 {
					progress = 90
				}
				report(progress, "合成中（长文本任务耗时与文本量正相关）",
					map[string]any{"task_id": taskID, "poll": n, "status": result.Status})
			}
		}
		select {
		case <-ctx.Done():
			return TTSLongQueryResult{}, fmt.Errorf("长文本合成已取消（上游任务 %s 仍会在服务端保留，音频保留 7 天）: %w", taskID, ctx.Err())
		case <-time.After(interval):
		}
		interval *= 2
		if interval > ttsLongPollMax {
			interval = ttsLongPollMax
		}
	}
}

// saveArtifacts 落盘音频（tts_long/<uuid>.<ext>）与可选 SRT 字幕，按 M2 契约处理 _out 重定向。
func (t *TTSLongTool) saveArtifacts(in provider.TaskInput, text, taskID string, result TTSLongQueryResult, audio []byte, format string, timestamps bool) (provider.TaskOutput, error) {
	reqID := uuid.NewString()
	audioPath := filepath.Join("tts_long", reqID+"."+extOf(format))
	// _out 参数（CLI --out）重定向产物路径；不进 ParamSpecs，属机器约定。
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		audioPath = outParam
	}
	// srt 路径跟随音频路径：仅换扩展名（_out 为绝对路径时 srt 同为绝对；相对时在相对段上替换）。
	srtPath := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".srt"

	audioAbs := audioPath
	if !filepath.IsAbs(audioAbs) {
		audioAbs = filepath.Join(t.outDir, audioPath)
		audioPath, _ = filepath.Rel(t.outDir, audioAbs)
	}
	if err := os.MkdirAll(filepath.Dir(audioAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(audioAbs, audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}

	arts := []provider.Artifact{{
		Kind: "audio", Path: audioPath, Format: format,
		Size: int64(len(audio)), DurationMS: lastEndMS(result.Sentences),
	}}

	// 时间戳开启且服务端返回分句时产出 SRT；未开启或空分句则跳过（与 ASR 行为一致）。
	if timestamps && len(result.Sentences) > 0 {
		segs := make([]ASRSegment, 0, len(result.Sentences))
		for _, s := range result.Sentences {
			segs = append(segs, ASRSegment{Text: s.Text, StartMS: s.StartMS, EndMS: s.EndMS})
		}
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

	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"char_count":        utf8.RuneCountInString(text),
			"synthesized_chars": result.SynthesizeTextLength,
			"sentence_count":    len(result.Sentences),
			"upstream_task_id":  taskID,
			"req_text_length":   result.ReqTextLength,
		},
	}, nil
}

// lastEndMS 取最后一个分句的结束时间作为音频时长（未开启时间戳时返回 0）。
func lastEndMS(sentences []TTSLongSentence) int64 {
	if len(sentences) == 0 {
		return 0
	}
	return sentences[len(sentences)-1].EndMS
}

// validateLongText 官方约束预检：≤10 万字符；非法 ASCII 控制字符（不含 \t \n，\r 算非法）
// 占比 >10% 时服务端拒绝执行，提前拦截给出明确指引。
func validateLongText(text string) error {
	n := utf8.RuneCountInString(text)
	if n > ttsLongMaxLength {
		return fmt.Errorf("文本超出长度限制：当前 %d 字符，上限 %d（请分段提交）", n, ttsLongMaxLength)
	}
	illegal := 0
	for _, r := range text {
		if r < 0x20 && r != '\t' && r != '\n' {
			illegal++
		}
	}
	if ratio := float64(illegal) / float64(n); illegal > 0 && ratio > ttsLongMaxIllegalRatio {
		return fmt.Errorf("文本非法字符占比 %.0f%% 超过 10%%（非法字符指除制表符与换行外的 ASCII 控制字符，共 %d 个），请清理后重新提交",
			ratio*100, illegal)
	}
	return nil
}

// toInt 取整型参数：兼容 int/int64/float64/string（JSON 反序列化数值恒为 float64）。
func toInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var i int
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &i); err == nil {
			return i
		}
	}
	return def
}

// paramBool 取布尔参数（默认 false；兼容 bool 与字符串 "true"/"false"，别名兼容旧参数名）。
func paramBool(params map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch v := params[key].(type) {
		case bool:
			return v
		case string:
			if strings.EqualFold(v, "true") {
				return true
			}
		}
	}
	return false
}
