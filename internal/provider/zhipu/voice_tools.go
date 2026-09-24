package zhipu

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yann0917/voxbox/internal/provider"
)

// VoiceCloneTool 音色复刻（glm-tts-clone）：示例音频经文件接口上传换 file_id，
// 复刻得到 voice_clone_* 音色（可直接用于本平台 tts 的 voice 参数）。
// 当前仅对接接口（CLI/任务通道可用），前端暂不展示页面——等复刻能力与价格的
// 平台对比后再定展示形态（与小米 voiceclone、火山声音复刻同待对比）。
type VoiceCloneTool struct {
	client *VoiceClient
	apiKey string
}

func NewVoiceCloneTool(apiKey string) *VoiceCloneTool {
	return &VoiceCloneTool{client: NewVoiceClient(apiKey, BaseURL), apiKey: apiKey}
}

func (t *VoiceCloneTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "zhipu",
		Name:        "voice_clone",
		Title:       "音色复刻（智谱）",
		Description: "glm-tts-clone 基于示例音频复刻音色（wav/mp3 ≤10MB、建议 3-30 秒），返回可直接用于合成的音色 ID",
		Group:       "语音",
	}
}

func (t *VoiceCloneTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "voice_name", Label: "音色名称", Type: provider.ParamString, Required: true, Group: "复刻",
			Placeholder: "唯一音色名，如 my_voice_001"},
		{Key: "input", Label: "试听文本", Type: provider.ParamText, Required: true, Group: "复刻",
			Placeholder: "生成试听音频的目标文本"},
		{Key: "text", Label: "示例文本", Type: provider.ParamText, Group: "复刻",
			Placeholder: "示例音频对应的文字内容（选填，有助提升复刻质量）"},
	}
}

func (t *VoiceCloneTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	name := paramString(in.Params, "voice_name")
	input := paramString(in.Params, "input")
	if name == "" || input == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: voice_name / input")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置智谱 API Key，或 voxbox config set zhipu.api_key", ErrNoCred)
	}
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少复刻示例音频：请以文件输入提供 wav/mp3 音频（≤10MB，建议 3-30 秒）")
	}
	if ext := strings.ToLower(filepath.Ext(src)); ext != ".wav" && ext != ".mp3" {
		return provider.TaskOutput{}, fmt.Errorf("参数错误：复刻示例音频仅支持 wav / mp3（当前 %s）", ext)
	}

	report(20, "上传示例音频", nil)
	fileID, err := t.client.UploadVoiceSample(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(60, "正在复刻音色", nil)
	voice, err := t.client.VoiceClone(ctx, name, fileID, input, paramString(in.Params, "text"))
	if err != nil {
		return provider.TaskOutput{}, err
	}
	return provider.TaskOutput{
		Summary: map[string]any{
			"voice":      voice,
			"voice_name": name,
			"file_id":    fileID,
			"hint":       "该音色可直接用于 zhipu.tts 的 voice 参数",
		},
	}, nil
}

// VoiceDeleteTool 删除音色（复刻音色管理配套；与 VoiceCloneTool 同样仅对接接口）。
type VoiceDeleteTool struct {
	client *VoiceClient
	apiKey string
}

func NewVoiceDeleteTool(apiKey string) *VoiceDeleteTool {
	return &VoiceDeleteTool{client: NewVoiceClient(apiKey, BaseURL), apiKey: apiKey}
}

func (t *VoiceDeleteTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "zhipu",
		Name:        "voice_delete",
		Title:       "删除音色（智谱）",
		Description: "删除指定音色（含复刻音色 voice_clone_*），不可恢复",
		Group:       "语音",
	}
}

func (t *VoiceDeleteTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "voice", Label: "音色 ID", Type: provider.ParamString, Required: true, Group: "删除",
			Placeholder: "voice_clone_20240315_143052_001"},
	}
}

func (t *VoiceDeleteTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	voice := paramString(in.Params, "voice")
	if voice == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: voice")
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置智谱 API Key，或 voxbox config set zhipu.api_key", ErrNoCred)
	}
	report(30, "正在删除音色", nil)
	if err := t.client.VoiceDelete(ctx, voice); err != nil {
		return provider.TaskOutput{}, err
	}
	return provider.TaskOutput{
		Summary: map[string]any{"voice": voice, "status": "deleted"},
	}, nil
}
