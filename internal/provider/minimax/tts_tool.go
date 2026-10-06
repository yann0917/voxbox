package minimax

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
)

// minimaxTTSMaxChars 同步合成单次文本上限内的分段预算：官方硬上限 10000 字符，
// 大于 3000 推荐流式——voxbox 以非流式分段规避，段预算取 2000 字符
// （约 7-8 分钟音频/段，响应体积与耗时都在稳妥区间），超限文本服务端按句分段
// 逐段合成后拼接，对前端透明。
const minimaxTTSMaxChars = 2000

// 情绪枚举（官方 voice_setting.emotion）：空 = 模型按文本自动匹配（官方推荐默认）。
var ttsEmotions = []provider.ParamOption{
	{Value: "", Label: "自动（按文本匹配）"},
	{Value: "happy", Label: "高兴"}, {Value: "sad", Label: "悲伤"},
	{Value: "angry", Label: "愤怒"}, {Value: "fearful", Label: "害怕"},
	{Value: "disgusted", Label: "厌恶"}, {Value: "surprised", Label: "惊讶"},
	{Value: "calm", Label: "中性"}, {Value: "fluent", Label: "生动"},
	{Value: "whisper", Label: "低语"},
}

// 语言增强枚举（官方 speech-t2a-http language_boost 全量 enum，2026-10-05）：
// 空 = 不传（上游默认）；粤语音色官方要求 Chinese,Yue。
var ttsLanguageBoosts = []provider.ParamOption{
	{Value: "", Label: "不启用"},
	{Value: "auto", Label: "auto（自动判断语种）"},
	{Value: "Chinese", Label: "Chinese（中文）"},
	{Value: "Chinese,Yue", Label: "Chinese,Yue（粤语）"},
	{Value: "English", Label: "English（英语）"}, {Value: "Arabic", Label: "Arabic（阿拉伯语）"},
	{Value: "Russian", Label: "Russian（俄语）"}, {Value: "Spanish", Label: "Spanish（西班牙语）"},
	{Value: "French", Label: "French（法语）"}, {Value: "Portuguese", Label: "Portuguese（葡萄牙语）"},
	{Value: "German", Label: "German（德语）"}, {Value: "Turkish", Label: "Turkish（土耳其语）"},
	{Value: "Dutch", Label: "Dutch（荷兰语）"}, {Value: "Ukrainian", Label: "Ukrainian（乌克兰语）"},
	{Value: "Vietnamese", Label: "Vietnamese（越南语）"}, {Value: "Indonesian", Label: "Indonesian（印尼语）"},
	{Value: "Japanese", Label: "Japanese（日语）"}, {Value: "Italian", Label: "Italian（意大利语）"},
	{Value: "Korean", Label: "Korean（韩语）"}, {Value: "Thai", Label: "Thai（泰语）"},
	{Value: "Polish", Label: "Polish（波兰语）"}, {Value: "Romanian", Label: "Romanian（罗马尼亚语）"},
	{Value: "Greek", Label: "Greek（希腊语）"}, {Value: "Czech", Label: "Czech（捷克语）"},
	{Value: "Finnish", Label: "Finnish（芬兰语）"}, {Value: "Hindi", Label: "Hindi（印地语）"},
	{Value: "Bulgarian", Label: "Bulgarian（保加利亚语）"}, {Value: "Danish", Label: "Danish（丹麦语）"},
	{Value: "Hebrew", Label: "Hebrew（希伯来语）"}, {Value: "Malay", Label: "Malay（马来语）"},
	{Value: "Persian", Label: "Persian（波斯语）"}, {Value: "Slovak", Label: "Slovak（斯洛伐克语）"},
	{Value: "Swedish", Label: "Swedish（瑞典语）"}, {Value: "Croatian", Label: "Croatian（克罗地亚语）"},
	{Value: "Filipino", Label: "Filipino（菲律宾语）"}, {Value: "Hungarian", Label: "Hungarian（匈牙利语）"},
	{Value: "Norwegian", Label: "Norwegian（挪威语）"}, {Value: "Slovenian", Label: "Slovenian（斯洛文尼亚语）"},
	{Value: "Catalan", Label: "Catalan（加泰罗尼亚语）"}, {Value: "Nynorsk", Label: "Nynorsk（挪威尼诺斯克语）"},
	{Value: "Tamil", Label: "Tamil（泰米尔语）"}, {Value: "Afrikaans", Label: "Afrikaans（南非荷兰语）"},
}

// 音色特效枚举（voice_modify.sound_effects，单次仅能选一种；指南「音色特效」）。
var ttsSoundEffects = []provider.ParamOption{
	{Value: "", Label: "不启用"},
	{Value: "spacious_echo", Label: "空旷回音"},
	{Value: "auditorium_echo", Label: "礼堂广播"},
	{Value: "lofi_telephone", Label: "电话失真"},
	{Value: "robotic", Label: "电音"},
}

// 停顿标记 <#x#>（官方文本标记语法：x 为秒，[0.01,99.99]，最多两位小数，
// 须夹在两段可发音文本之间、不可连续使用）。标记与行内发音 (pinyin)、语气词
// 标签 (laughs) 均随文本透传，发音词典按整词替换不会命中；这里只做位置与
// 数值校验，以及分段边界的粘合修正。
var pauseMarkerRe = regexp.MustCompile(`<#([^#\n]*)#>`)

// leadingPauseRe 段首连续停顿标记（含前导空白）。
var leadingPauseRe = regexp.MustCompile(`^(?:\s*<#[^#]*#>)+`)

// consecutivePauseRe 连续停顿标记（标记间仅空白）。
var consecutivePauseRe = regexp.MustCompile(`<#[^#\n]*#>\s*<#[^#\n]*#>`)

// validatePauseMarkers 停顿标记预检：数值/精度/连续/首尾位置。出错即参数类错误，
// 在发起合成请求前拦截（重试无意义，不消耗配额）。
func validatePauseMarkers(text string) error {
	trimmed := strings.TrimSpace(text)
	if !strings.Contains(trimmed, "<#") {
		return nil
	}
	if consecutivePauseRe.MatchString(trimmed) {
		return fmt.Errorf("参数错误：停顿标记不可连续使用（两个 <#x#> 之间需要有可发音的文本）")
	}
	locs := pauseMarkerRe.FindAllStringIndex(trimmed, -1)
	if len(locs) > 0 && (locs[0][0] == 0 || locs[len(locs)-1][1] == len(trimmed)) {
		return fmt.Errorf("参数错误：停顿标记不能位于文本开头或结尾（需夹在两段可发音文本之间）")
	}
	for _, m := range pauseMarkerRe.FindAllStringSubmatch(trimmed, -1) {
		v := m[1]
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0.01 || f > 99.99 || (strings.Contains(v, ".") && len(strings.SplitN(v, ".", 2)[1]) > 2) {
			return fmt.Errorf("参数错误：停顿标记 <#%s#> 数值需在 0.01-99.99 之间（最多两位小数）", v)
		}
	}
	return nil
}

// gluePauseMarkers 分段边界的停顿标记粘合：SplitText 切句会把紧跟句号的标记归入
// 下一句，跨段时标记就孤立在段首（该请求里标记前没有可发音文本，上游拒绝）。
// 官方语义是「两段可发音文本之间的间隔」，把段首标记挪到前段尾部语义等价。
// 首段段首的标记属文本开头，由 validatePauseMarkers 拦截。
func gluePauseMarkers(segs []string) []string {
	for i := 1; i < len(segs); i++ {
		m := leadingPauseRe.FindString(segs[i])
		if m != "" {
			segs[i-1] += m
			segs[i] = segs[i][len(m):]
		}
	}
	return segs
}

// TTSTool MiniMax 同步语音合成（speech-2.8 系列，非流式，响应为 JSON 内 hex 编码音频）。
type TTSTool struct {
	client *TTSClient
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSTool(apiKey, outDir string) *TTSTool {
	return &TTSTool{client: NewTTSClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *TTSTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "minimax",
		Name:        "tts",
		Title:       "语音合成（MiniMax）",
		Description: "speech-2.8 同步合成，327 个系统音色（中/粤/英/日/韩等 24 语种），支持情绪/语速/音调调节；长文本自动分段合成后拼接",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本（长文本自动分段）", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: ModelTTSHD, Group: "参数",
			Options: []provider.ParamOption{
				{Value: ModelTTSHD, Label: "speech-2.8-hd（高清）"},
				{Value: ModelTTSTurbo, Label: "speech-2.8-turbo（提速降本）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamString, Default: DefaultVoice, Group: "参数",
			Placeholder: "音色 ID，完整列表见 Web 语音合成页下拉（GET /api/voices?provider=minimax）"},
		{Key: "speed", Label: "语速", Type: provider.ParamFloat, Group: "参数",
			Placeholder: "0.5-2.0，默认 1.0"},
		{Key: "volume", Label: "音量", Type: provider.ParamFloat, Group: "参数",
			Placeholder: "(0, 10]，默认 1.0"},
		{Key: "pitch", Label: "音调", Type: provider.ParamInt, Group: "参数",
			Placeholder: "-12 到 12，默认 0"},
		{Key: "emotion", Label: "情绪", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: ttsEmotions},
		{Key: "language_boost", Label: "语种增强", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: ttsLanguageBoosts},
		{Key: "sound_effects", Label: "音色特效", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: ttsSoundEffects},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	text = pronunciation.Apply(text, "") // 发音词典：合成前文本预处理（分段前应用）
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if err := validatePauseMarkers(text); err != nil {
		return provider.TaskOutput{}, err
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置 MiniMax API Key，或 voxbox config set minimax.api_key", ErrNoCred)
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	model := paramString(in.Params, "model")
	speed, volume := paramFloat(in.Params, "speed"), paramFloat(in.Params, "volume")
	pitch := int(paramFloat(in.Params, "pitch"))
	emotion := paramString(in.Params, "emotion")
	languageBoost := paramString(in.Params, "language_boost")
	soundEffects := paramString(in.Params, "sound_effects")

	// 单次 ≤2000 字符：按句分段逐段合成，段间 wav 拼接，前端无感；
	// 段首停顿标记粘合回前段尾部（官方要求标记前有可发音文本）
	segs := gluePauseMarkers(provider.SplitText(text, minimaxTTSMaxChars))
	chunks := make([][]byte, 0, len(segs))
	for i, seg := range segs {
		report(90*i/len(segs), fmt.Sprintf("正在合成第 %d/%d 段", i+1, len(segs)), nil)
		audio, err := t.client.Synthesize(ctx, TTSReq{
			Text: seg, Voice: voice, Model: model,
			Speed: speed, Volume: volume, Pitch: pitch,
			Emotion: emotion, LanguageBoost: languageBoost,
			SoundEffects: soundEffects,
		})
		if err != nil {
			return provider.TaskOutput{}, err
		}
		chunks = append(chunks, audio)
	}
	audio := chunks[0]
	if len(chunks) > 1 {
		report(90, "拼接分段音频", nil)
		var err error
		if audio, err = provider.ConcatWAV(chunks...); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("分段音频拼接失败: %w", err)
		}
	}
	report(95, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+".wav")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}
	summary := map[string]any{
		"char_count":  utf8.RuneCountInString(text),
		"model":       effectiveModel(model),
		"voice":       voice,
		"segment_num": len(segs),
	}
	if speed > 0 {
		summary["speed"] = speed
	}
	if volume > 0 {
		summary["volume"] = volume
	}
	if pitch != 0 {
		summary["pitch"] = pitch
	}
	if emotion != "" {
		summary["emotion"] = emotion
	}
	if languageBoost != "" {
		summary["language_boost"] = languageBoost
	}
	if soundEffects != "" {
		summary["sound_effects"] = soundEffects
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: "wav",
			Size: int64(len(audio)),
		}},
		Summary: summary,
	}, nil
}

// effectiveModel 请求模型为空回落高清版（summary 展示用，与 Synthesize 的回落同口径）。
func effectiveModel(m string) string {
	if m == "" {
		return ModelTTSHD
	}
	return m
}
