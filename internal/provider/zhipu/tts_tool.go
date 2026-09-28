package zhipu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
)

// zhipuTTSMaxChars glm-tts input 上限（官方 schema maxLength 1024）；
// 超限文本由服务端按句分段逐段合成后拼接，对前端透明。
const zhipuTTSMaxChars = 1024

// TTSTool 智谱语音合成（glm-tts，非流式，响应为 wav 二进制）。
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
		Provider:    "zhipu",
		Name:        "tts",
		Title:       "语音合成（智谱）",
		Description: "glm-tts 非流式合成，官方音色与复刻音色，支持语速/音量调节；长文本自动分段合成后拼接",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	opts := make([]provider.ParamOption, 0, len(OfficialVoices))
	for _, v := range OfficialVoices {
		opts = append(opts, provider.ParamOption{Value: v.Voice, Label: v.VoiceName})
	}
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本（长文本自动分段）", Group: "内容"},
		{Key: "voice", Label: "音色", Type: provider.ParamEnum, Default: DefaultVoice, Group: "参数",
			Options:     opts,
			Placeholder: "官方音色或复刻音色 ID（voice_clone_*）"},
		{Key: "speed", Label: "语速", Type: provider.ParamFloat, Group: "参数",
			Placeholder: "0.5-2.0，默认 1.0"},
		{Key: "volume", Label: "音量", Type: provider.ParamFloat, Group: "参数",
			Placeholder: "(0, 10]，默认 1.0"},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := paramString(in.Params, "text")
	text = pronunciation.Apply(text, "") // 发音词典：合成前文本预处理（分段前应用）
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置智谱 API Key，或 voxbox config set zhipu.api_key", ErrNoCred)
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	speed, volume := paramFloat(in.Params, "speed"), paramFloat(in.Params, "volume")

	// 官方单次 input ≤1024 字符：按句分段逐段合成，段间 wav 拼接，前端无感
	segs := provider.SplitText(text, zhipuTTSMaxChars)
	chunks := make([][]byte, 0, len(segs))
	for i, seg := range segs {
		report(90*i/len(segs), fmt.Sprintf("正在合成第 %d/%d 段", i+1, len(segs)), nil)
		audio, err := t.client.Synthesize(ctx, TTSSynthesizeReq{Text: seg, Voice: voice, Speed: speed, Volume: volume})
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
		"model":       "glm-tts",
		"voice":       voice,
		"segment_num": len(segs),
	}
	if speed > 0 {
		summary["speed"] = speed
	}
	if volume > 0 {
		summary["volume"] = volume
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: "wav",
			Size: int64(len(audio)),
		}},
		Summary: summary,
	}, nil
}

// paramFloat 取浮点参数（缺失/非法返回 0，由调用方决定默认语义）。
func paramFloat(params map[string]any, key string) float64 {
	switch v := params[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &f); err == nil {
			return f
		}
	}
	return 0
}
