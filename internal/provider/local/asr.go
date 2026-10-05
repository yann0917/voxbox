package local

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/subtitle"

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
	tts     *localruntime.TTSRuntime // audiocpp 常驻 server(R2T2 等 audiocpp ASR 条目消费)
	// 测试 seam
	transcribeFn func(ctx context.Context, binPath, modelDir, wav, language string, itn bool) (localruntime.SherpaResult, error)
	// audiocpp 路径 seam:缺省绑 TTSRuntime.Transcribe(与 TTS 同一 server 实例)
	transcribeAIFn func(ctx context.Context, req localruntime.TranscribeRequest) (localruntime.TranscribeResult, error)
	transcodeFn    func(ctx context.Context, src, dst string) error
	lookPath       func(string) (string, error)
}

func newASRTool(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime) *asrTool {
	a := &asrTool{dataDir: dataDir, models: models, tts: tts}
	a.transcribeFn = localruntime.Transcribe
	if tts != nil {
		// 测试路径 tts 可为 nil,缺省 transcribeAIFn 保持 nil,Run 前置守卫兜底;
		// 生产路径(RegisterAll/AllTools)tts 非 nil,必绑真实实现。
		a.transcribeAIFn = tts.Transcribe
	}
	a.transcodeFn = defaultTranscode
	a.lookPath = exec.LookPath
	return a
}

func (a *asrTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "asr", Title: "本地语音识别",
		Description: "本地识别（SenseVoice / R2T2）：中英日韩粤或 30 语自动检测，自动标点与 ITN；支持常见音频格式，非 wav 自动转码。", Group: "识别"}
}

// ResultTitle 结果标题自描述:识别文本前缀派生(与火山 ASR 同一契约,provider 包实现)。
func (a *asrTool) ResultTitle(summaryJSON string) string {
	return provider.ASRTitleFromSummary(summaryJSON)
}

func (a *asrTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		// 识别模型枚举选项取目录 kind=asr 条目(sensevoice-int8 / r2t2-q8_0);
		// 缺省 sensevoice:旧任务重跑无 model 参数走 sherpa 原路径(向后兼容)
		{Key: "model", Label: "识别模型", Type: provider.ParamEnum, Default: asrModelID, Group: "本地推理",
			Options: a.modelOptions()},
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "auto", Group: "本地推理",
			Options: []provider.ParamOption{
				{Value: "auto", Label: "自动"}, {Value: "zh", Label: "中文"}, {Value: "en", Label: "英文"},
				{Value: "ja", Label: "日语"}, {Value: "ko", Label: "韩语"}, {Value: "yue", Label: "粤语"},
			}},
		{Key: "itn", Label: "文本规整(ITN)", Type: provider.ParamBool, Default: true, Group: "本地推理"},
		// 热词/上下文仅 audiocpp 路径(R2T2)消费,经 JSON text 字段透传;sherpa 忽略
		{Key: "hotwords", Label: "热词", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "选填,逗号分隔;仅 R2T2 模型消费"},
	}
}

// modelOptions 识别模型枚举:目录 kind=asr 条目(目录序)。manager 未注入(纯单测)时
// 回落缺省项,保证参数声明恒有合法取值。
func (a *asrTool) modelOptions() []provider.ParamOption {
	var opts []provider.ParamOption
	if a.models != nil {
		for _, v := range a.models.List() {
			if v.Entry.Kind == "asr" {
				opts = append(opts, provider.ParamOption{Value: v.Entry.ID, Label: v.Entry.Name})
			}
		}
	}
	if len(opts) == 0 {
		opts = append(opts, provider.ParamOption{Value: asrModelID, Label: "SenseVoice 多语种识别(int8)"})
	}
	return opts
}

func (a *asrTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入:请上传音频文件")
	}
	// 模型路由:params.model 缺省回落 sensevoice(旧任务重跑无 model 参数必须走 sherpa
	// 原路径);条目 requires_engine 决定运行时分流(sherpa 一次性子进程 / audiocpp 常驻 server)。
	modelID := paramString(in.Params, "model", asrModelID)
	e, ok := a.models.GetEntry(modelID)
	if !ok || e.Kind != "asr" {
		return provider.TaskOutput{}, fmt.Errorf("未知本地识别模型: %s", modelID)
	}
	if e.RequiresEngine == asrEngineID {
		return a.runSherpa(ctx, in, report, modelID, src)
	}
	return a.runAudiocpp(ctx, in, report, e, src)
}

// runSherpa SenseVoice 一次性子进程识别(原 Run 主体逐行平移,modelID 参数化;
// 缺省模型即 sensevoice-int8,行为零变化)。
func (a *asrTool) runSherpa(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter, modelID, src string) (provider.TaskOutput, error) {
	if !a.models.Installed(asrEngineID) {
		return provider.TaskOutput{}, fmt.Errorf("本地引擎未安装:请到设置页「本地环境」下载 sherpa-onnx 引擎")
	}
	if !a.models.Installed(modelID) {
		return provider.TaskOutput{}, fmt.Errorf("本地模型未安装:请到设置页「本地环境」下载 SenseVoice")
	}
	bin, err := a.models.EngineBinary(asrEngineID)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	modelDir := filepath.Join(a.dataDir, "models", modelID)
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
	arts := []provider.Artifact{{
		Kind: "transcript", Path: rel, Format: "txt", Size: int64(len(res.Text)),
		Meta: map[string]any{"engine": asrEngineID, "model": modelID, "lang": res.Lang},
	}}

	// 分句+断句规范：SenseVoice tokens 与 timestamps 严格 1:1（spike 实测），先按句末
	// 标点聚句，再过断句规范（28 字上限下切/过短并合/时长钳制/间隙）——产出 SRT 产物
	// 并随 summary.segments 下发（与云端 ASR 同形状），字幕工坊「选择任务导入」按它取轴。
	// tokens 为 ITN 前的原始识别序列（含标点），字幕文本以它为准；转写产物仍是 ITN
	// 规整文本，两者在数字读法上可能不同属预期。
	summary := map[string]any{"engine": asrEngineID, "text": res.Text}
	if len(res.Tokens) > 0 && len(res.Tokens) == len(res.Timestamps) {
		words := make([]subtitle.WordSpan, 0, len(res.Tokens))
		for i, tok := range res.Tokens {
			ms := int64(math.Round(res.Timestamps[i] * 1000))
			words = append(words, subtitle.WordSpan{Text: tok, StartMS: ms, EndMS: ms})
		}
		if segs := subtitle.RefineTokens(words, nil); len(segs) > 0 {
			srtPath := strings.TrimSuffix(rel, filepath.Ext(rel)) + ".srt"
			srtContent := subtitle.BuildSRT(segs)
			if err := os.WriteFile(filepath.Join(a.dataDir, srtPath), srtContent, 0o644); err != nil {
				return provider.TaskOutput{}, fmt.Errorf("写入 SRT 字幕失败: %w", err)
			}
			arts = append(arts, provider.Artifact{
				Kind: "subtitle", Path: srtPath, Format: "srt", Size: int64(len(srtContent)),
			})
			summary["segments"] = segs
		}
	}
	report(100, "本地识别完成", nil)
	return provider.TaskOutput{Artifacts: arts, Summary: summary}, nil
}

// runAudiocpp R2T2 等 audiocpp ASR 条目:与 TTS 同一常驻 server(lazy_load/健康检查),
// JSON 分支传本机 wav 绝对路径。响应 plain 路由仅 text+timing(实测无 language/segments),
// summary 只带 text/engine/source(+duration_ms),无 segments——纪要区可用、字幕不可产(预期)。
func (a *asrTool) runAudiocpp(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter, e localmodel.Entry, src string) (provider.TaskOutput, error) {
	if a.transcribeAIFn == nil {
		// tts runtime 未注入(仅测试误用可达):提交期校验拦不住,这里显式拒绝
		return provider.TaskOutput{}, fmt.Errorf("本地识别引擎不可用:请重启服务后重试")
	}
	if !a.models.Installed(e.RequiresEngine) {
		return provider.TaskOutput{}, fmt.Errorf("本地引擎未安装:请到设置页「本地环境」下载 %s 引擎", e.RequiresEngine)
	}
	if !a.models.Installed(e.ID) {
		return provider.TaskOutput{}, fmt.Errorf("本地模型未安装:请到设置页「本地环境」下载 %s", e.Name)
	}
	wav, cleanup, err := a.ensureWav(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	report(10, "启动本地识别…", nil)
	// 语种:auto/空 = 自动检测不透传(缺省,UI 对 r2t2 不出语言选择);显式指定才强制
	language := paramString(in.Params, "language", "auto")
	if language == "auto" {
		language = ""
	}
	res, err := a.transcribeAIFn(ctx, localruntime.TranscribeRequest{
		ModelID: e.ID, Wav: wav, Language: language,
		Hotwords: paramString(in.Params, "hotwords", ""),
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(90, "写入产物…", nil)
	outDir := filepath.Join("asr", newReqID())
	if err := os.MkdirAll(filepath.Join(a.dataDir, outDir), 0o755); err != nil {
		return provider.TaskOutput{}, err
	}
	rel := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"_local.txt")
	if err := os.WriteFile(filepath.Join(a.dataDir, rel), []byte(res.Text), 0o644); err != nil {
		return provider.TaskOutput{}, err
	}
	// source 与云端 ASR 同语义(本地通道恒为 file);duration_ms 取 timing 实测值供播放器展示
	summary := map[string]any{"engine": e.RequiresEngine, "text": res.Text, "source": "file"}
	if ms := int64(res.Timing.AudioDurationMS); ms > 0 {
		summary["duration_ms"] = ms
	}
	report(100, "本地识别完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: rel, Format: "txt", Size: int64(len(res.Text)),
			Meta: map[string]any{"engine": e.RequiresEngine, "model": e.ID},
		}},
		Summary: summary,
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
