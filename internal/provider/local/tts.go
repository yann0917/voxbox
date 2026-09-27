package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
)

const (
	refMaxSeconds = 60
	refMaxBytes   = 20 << 20
)

type ttsTool struct {
	dataDir string
	models  *localmodel.Manager
	tts     *localruntime.TTSRuntime

	// 测试 seam:缺省绑 TTSRuntime.Synthesize;转码 ffmpeg 可替换探测。
	synthesizeFn func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error)
	lookPath     func(string) (string, error)
}

func newTTSTool(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) *ttsTool {
	t := &ttsTool{dataDir: dataDir, models: models, tts: tts, lookPath: exec.LookPath}
	if tts != nil {
		// 测试路径 tts 可为 nil,缺省 synthesizeFn 保持 nil,Run 前置守卫兜底;
		// 生产路径(RegisterAll/AllTools)tts 非 nil,必绑真实实现。
		t.synthesizeFn = tts.Synthesize
	}
	return t
}

func (t *ttsTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "tts", Title: "本地语音合成",
		Description: "audio.cpp 引擎驱动已安装的 Qwen3-TTS:参考音频克隆或预置音色,离线合成。", Group: "合成"}
}

func (t *ttsTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "合成文本", Type: provider.ParamText, Required: true, Group: "本地推理"},
		{Key: "model", Label: "本地模型", Type: provider.ParamEnum, Required: true, Group: "本地推理",
			Placeholder: "设置页已安装的 Qwen3-TTS 条目"},
		{Key: "mode", Label: "音色模式", Type: provider.ParamEnum, Required: true, Default: "clone",
			Options: []provider.ParamOption{{Value: "clone", Label: "参考音频克隆"}, {Value: "preset", Label: "预置音色"}}, Group: "本地推理"},
		{Key: "ref_text", Label: "参考音频转写", Type: provider.ParamText, Group: "本地推理",
			Placeholder: "参考音频实际说的内容(留空走纯音色克隆)"},
		{Key: "speaker", Label: "预置音色", Type: provider.ParamEnum, Group: "本地推理",
			Options: speakerOptions()},
		{Key: "instruct", Label: "风格指令", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "如:Very happy and energetic"},
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "Chinese",
			Options: []provider.ParamOption{{Value: "Chinese", Label: "中文"}, {Value: "English", Label: "英文"},
				{Value: "Japanese", Label: "日语"}, {Value: "Korean", Label: "韩语"}}, Group: "本地推理"},
	}
}

func speakerOptions() []provider.ParamOption {
	var opts []provider.ParamOption
	for _, v := range CustomVoices() {
		opts = append(opts, provider.ParamOption{Value: v.ID, Label: v.Name})
	}
	return opts
}

func (t *ttsTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	if t.synthesizeFn == nil {
		// tts runtime 未注入(仅测试误用可达):引擎提交期校验拦不住,这里显式拒绝
		return provider.TaskOutput{}, fmt.Errorf("本地合成引擎不可用:请重启服务后重试")
	}
	modelID, _ := in.Params["model"].(string)
	mode, _ := in.Params["mode"].(string)
	if modelID == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: model")
	}
	if mode != "clone" && mode != "preset" {
		return provider.TaskOutput{}, fmt.Errorf("参数错误: mode 仅支持 clone|preset")
	}
	// 引擎与模型安装校验(错误文案直述去哪装)
	e, ok := t.models.GetEntry(modelID)
	if !ok {
		return provider.TaskOutput{}, fmt.Errorf("未知本地模型: %s", modelID)
	}
	if !t.models.Installed(e.RequiresEngine) {
		return provider.TaskOutput{}, fmt.Errorf("本地引擎未安装:请到设置页「本地环境」下载 %s 引擎", e.RequiresEngine)
	}
	if !t.models.Installed(modelID) {
		return provider.TaskOutput{}, fmt.Errorf("本地模型未安装:请到设置页「本地环境」下载 %s", e.Name)
	}
	// 模式与模型变体匹配:文件名含 customvoice 才支持 preset;含 base 才支持 clone
	switch {
	case mode == "preset" && !strings.Contains(e.ID, "customvoice"):
		return provider.TaskOutput{}, fmt.Errorf("预置音色需要 CustomVoice 模型:当前 %s 为克隆模型,请切换音色模式或下载 CustomVoice 条目", e.Name)
	case mode == "clone" && strings.Contains(e.ID, "customvoice"):
		return provider.TaskOutput{}, fmt.Errorf("参考音频克隆需要 Base 模型:当前 %s 为预置音色模型,请切换音色模式或下载 Base 条目", e.Name)
	}

	req := localruntime.SynthRequest{ModelID: modelID, Language: paramString(in.Params, "language", "Chinese")}
	if text := paramString(in.Params, "text", ""); text == "" {
		// 文本不在 params:与云端 TTS 一致由工具页 params.text 传入
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	} else {
		req.Text = text
	}
	switch mode {
	case "clone":
		ref := in.Files["audio"]
		if ref == "" {
			return provider.TaskOutput{}, fmt.Errorf("克隆模式需要参考音频:请上传或录制 3-60 秒清晰人声")
		}
		converted, err := t.convertRef(ctx, ref)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		defer os.Remove(converted)
		req.RefWav = converted
		req.RefText = paramString(in.Params, "ref_text", "")
	case "preset":
		sp := paramString(in.Params, "speaker", "")
		if sp == "" {
			return provider.TaskOutput{}, fmt.Errorf("预置模式需要选择音色: speaker")
		}
		req.Speaker = sp
		req.Instruct = paramString(in.Params, "instruct", "")
	}

	report(5, "准备本地合成…", nil)
	out, err := t.synthesizeFn(ctx, req, func(p int, note string) { report(p, note, nil) })
	if err != nil {
		return provider.TaskOutput{}, err
	}
	rel, err := filepath.Rel(t.dataDir, out)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	fi, err := os.Stat(out)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "本地合成完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: rel, Format: "wav", Size: fi.Size(),
			Meta: map[string]any{"engine": "audiocpp", "model": modelID, "mode": mode},
		}},
		Summary: map[string]any{"engine": "audiocpp", "model": e.Name, "mode": mode},
	}, nil
}

// convertRef 参考音频统一转 24kHz 单声道 pcm16(ffmpeg;限 60 秒/20MB)。
func (t *ttsTool) convertRef(ctx context.Context, src string) (string, error) {
	if _, err := t.lookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("参考音频转码需要 ffmpeg:请安装后重试(剪辑工具同款依赖)")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("参考音频不存在: %s", src)
	}
	if fi.Size() > refMaxBytes {
		return "", fmt.Errorf("参考音频超过 %dMB 上限", refMaxBytes>>20)
	}
	out := filepath.Join(t.dataDir, "tts", fmt.Sprintf("ref_%d.wav", time.Now().UnixNano()))
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-y", "-v", "error",
		"-i", src, "-t", fmt.Sprintf("%d", refMaxSeconds),
		"-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", out)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("任务已取消")
		}
		return "", fmt.Errorf("参考音频转码失败:请确认文件为有效音频")
	}
	return out, nil
}

// paramString 宽容取参(string/数值均收,audiotool 同款)。
func paramString(params map[string]any, key, def string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	switch s := v.(type) {
	case string:
		if s == "" {
			return def
		}
		return s
	default:
		return def
	}
}
