package volcengine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
)

const longTextThreshold = 1000

// TTSTool 语音合成工具（火山 TTS HTTP V1）。
type TTSTool struct {
	client *TTSClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSTool(cred SpeechCred, outDir string) *TTSTool {
	return &TTSTool{client: NewTTSClient(cred), cred: cred, outDir: outDir}
}

func (t *TTSTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "tts",
		Title:       "语音合成",
		Description: "将文本合成为语音，支持多音色、语速音量调节与长文本分段",
		Group:       "语音",
	}
}

func (t *TTSTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的文本", Group: "内容"},
		{Key: "voice", Label: "音色", Type: provider.ParamString,
			Default: "zh_female_cancan_mars_bigtts", Group: "参数",
			Placeholder: "音色 ID，用 voxbox voices list 查询"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum,
			Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "mp3", Label: "MP3"}, {Value: "wav", Label: "WAV"},
				{Value: "pcm", Label: "PCM"}, {Value: "ogg_opus", Label: "OGG Opus"},
			}},
		{Key: "speed_ratio", Label: "语速 (0.2-3.0)", Type: provider.ParamFloat, Default: 1.0, Group: "参数"},
		{Key: "volume_ratio", Label: "音量 (0.2-3.0)", Type: provider.ParamFloat, Default: 1.0, Group: "参数"},
	}
}

func (t *TTSTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	if err := t.cred.Validate(); err != nil {
		return provider.TaskOutput{}, err
	}
	text, _ := in.Params["text"].(string)
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	format, _ := in.Params["format"].(string)
	if format == "" {
		format = "mp3"
	}
	voice, _ := in.Params["voice"].(string)
	speed, volume := toFloat(in.Params["speed_ratio"], 1.0), toFloat(in.Params["volume_ratio"], 1.0)

	segments := splitText(text, longTextThreshold)
	if len(segments) > 1 && format != "mp3" {
		return provider.TaskOutput{}, fmt.Errorf("长文本分段合成仅支持 mp3 格式（当前 %s），请改用 mp3 或缩短文本", format)
	}

	var audio bytes.Buffer
	var durationMS int64
	for i, seg := range segments {
		report(100*(i)/len(segments), fmt.Sprintf("正在合成第 %d/%d 段", i+1, len(segments)), nil)
		resp, err := t.client.Synthesize(ctx, TTSSynthesizeReq{
			Text: seg, VoiceType: voice, Format: format,
			SpeedRatio: speed, VolumeRatio: volume,
		})
		if err != nil {
			return provider.TaskOutput{}, err
		}
		audio.Write(resp.Audio)
		durationMS += resp.DurationMS
	}
	report(90, "保存音频文件", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+"."+extOf(format))
	// _out 参数（CLI --out）重定向产物路径；不进 ParamSpecs，属机器约定。
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath := relPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.outDir, relPath)
		relPath, _ = filepath.Rel(t.outDir, absPath)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, audio.Bytes(), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}

	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: format,
			Size: int64(audio.Len()), DurationMS: durationMS,
		}},
		Summary: map[string]any{
			"char_count":  utf8.RuneCountInString(text),
			"segment_num": len(segments),
		},
	}, nil
}

func extOf(format string) string {
	if format == "ogg_opus" {
		return "ogg"
	}
	return format
}

func toFloat(v any, def float64) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%g", &f); err == nil {
			return f
		}
	}
	return def
}
