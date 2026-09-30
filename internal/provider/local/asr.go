package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	transcodeFn  func(ctx context.Context, src, dst string) error
	lookPath     func(string) (string, error)
}

func newASRTool(dataDir string, models *localmodel.Manager) *asrTool {
	a := &asrTool{dataDir: dataDir, models: models}
	a.transcribeFn = localruntime.Transcribe
	a.transcodeFn = defaultTranscode
	a.lookPath = exec.LookPath
	return a
}

func (a *asrTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "asr", Title: "本地语音识别",
		Description: "本地识别（SenseVoice）：中英日韩粤，自动标点与数字规整；支持常见音频格式，非 wav 自动转码。", Group: "识别"}
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
	src := in.Files["audio"]
	if src == "" {
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
	// 配音即字幕链路:TTS 产物多为 mp3,非 wav 输入经 ffmpeg 转 24kHz 单声道后进 sherpa
	wav, cleanup, err := a.ensureWav(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if cleanup != nil {
		defer cleanup()
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
	// 转写文本以原始输入名为基(转码产物 conv_*.wav 不出现在产物命名里)
	rel := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"_local.txt")
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

// ensureWav 非 wav 输入转 24kHz 单声道 pcm16 wav(wav 原样透传,sherpa 自带重采样);
// 返回转码产物与其清理函数(直传时无清理)。
func (a *asrTool) ensureWav(ctx context.Context, src string) (string, func(), error) {
	if strings.EqualFold(filepath.Ext(src), ".wav") {
		return src, nil, nil
	}
	if _, err := a.lookPath("ffmpeg"); err != nil {
		return "", nil, fmt.Errorf("输入为 %s 格式,本地识别转码需要 ffmpeg:请安装后重试(剪辑工具同款依赖)",
			strings.TrimPrefix(filepath.Ext(src), "."))
	}
	out := filepath.Join(a.dataDir, "asr", fmt.Sprintf("conv_%d.wav", time.Now().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", nil, err
	}
	if err := a.transcodeFn(ctx, src, out); err != nil {
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("任务已取消")
		}
		return "", nil, fmt.Errorf("音频转码失败:请确认文件为有效音频")
	}
	return out, func() { _ = os.Remove(out) }, nil
}

// defaultTranscode ffmpeg 统一转 24kHz 单声道 pcm16(与音色库入库口径一致)。
func defaultTranscode(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-y", "-v", "error",
		"-i", src, "-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", dst)
	return cmd.Run()
}

func newReqID() string {
	// uuid 工具与 audiotool 一致(直接引 uuid 包)
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}
