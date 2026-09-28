package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
)

const (
	asrEngineID = "sherpa-onnx"
	asrModelID  = "sensevoice-int8"
)

type asrTool struct {
	dataDir string
	models  *localmodel.Manager
	// 测试 seam
	transcribeFn func(ctx context.Context, binPath, modelDir, wav, language string, itn bool) (localruntime.SherpaResult, error)
	langOptions  []provider.ParamOption
}

func newASRTool(dataDir string, models *localmodel.Manager) *asrTool {
	a := &asrTool{dataDir: dataDir, models: models}
	a.transcribeFn = localruntime.Transcribe
	return a
}

func (a *asrTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "asr", Title: "本地语音识别",
		Description: "本地识别（SenseVoice）：中英日韩粤，自动标点与数字规整。", Group: "识别"}
}

func (a *asrTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "auto", Group: "本地推理",
			Options: []provider.ParamOption{
				{Value: "auto", Label: "自动"}, {Value: "zh", Label: "中文"}, {Value: "en", Label: "英文"},
				{Value: "ja", Label: "日语"}, {Value: "ko", Label: "韩语"}, {Value: "yue", Label: "粤语"},
			}},
		{Key: "itn", Label: "文本规整(ITN)", Type: provider.ParamBool, Default: true, Group: "本地推理"},
	}
}

func (a *asrTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	wav := in.Files["audio"]
	if wav == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入:请上传音频文件")
	}
	if !a.models.Installed(asrEngineID) {
		return provider.TaskOutput{}, fmt.Errorf("本地引擎未安装:请到设置页「本地环境」下载 sherpa-onnx 引擎")
	}
	if !a.models.Installed(asrModelID) {
		return provider.TaskOutput{}, fmt.Errorf("本地模型未安装:请到设置页「本地环境」下载 SenseVoice")
	}
	bin, err := a.models.EngineBinary(asrEngineID)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	modelDir := filepath.Join(a.dataDir, "models", asrModelID)
	language := paramString(in.Params, "language", "auto")
	itn := true
	if v, ok := in.Params["itn"].(bool); ok {
		itn = v
	}
	report(10, "启动本地识别…", nil)
	res, err := a.transcribeFn(ctx, bin, modelDir, wav, language, itn)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(90, "写入产物…", nil)
	outDir := filepath.Join("asr", newReqID())
	if err := os.MkdirAll(filepath.Join(a.dataDir, outDir), 0o755); err != nil {
		return provider.TaskOutput{}, err
	}
	rel := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(wav), filepath.Ext(wav))+"_local.txt")
	if err := os.WriteFile(filepath.Join(a.dataDir, rel), []byte(res.Text), 0o644); err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "本地识别完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: rel, Format: "txt", Size: int64(len(res.Text)),
			Meta: map[string]any{"engine": asrEngineID, "model": asrModelID, "lang": res.Lang},
		}},
		Summary: map[string]any{"engine": asrEngineID, "text": res.Text},
	}, nil
}

func newReqID() string {
	// uuid 工具与 audiotool 一致(直接引 uuid 包)
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}
