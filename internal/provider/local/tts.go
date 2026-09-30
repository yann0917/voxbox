package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/voicelib"
)

const (
	refMaxSeconds = 60
	refMaxBytes   = 20 << 20
)

// indexLanguages index_tts2 家族支持的语言码(空回落 auto)。
var indexLanguages = map[string]bool{"auto": true, "zh": true, "en": true, "ja": true, "es": true, "ar": true}

// qwen3Languages qwen3_tts 家族支持的语言全名(空回落 Chinese)。
var qwen3Languages = map[string]bool{"Chinese": true, "English": true, "Japanese": true, "Korean": true}

// kokoroLanguages kokoro 家族的语言码(空回落 auto):中英混读由文本驱动,
// 语言仅参与发音词典预处理,不透传合成引擎。
var kokoroLanguages = map[string]bool{"auto": true, "zh": true, "en": true}

// voxcpmLanguages voxcpm2 家族的语言码(空回落 auto):模型自动处理 30 语无需标签,
// 语言仅参与发音词典预处理,不透传合成引擎。
var voxcpmLanguages = map[string]bool{"auto": true, "zh": true, "en": true}

// chatterboxLanguages audio.cpp chatterbox 家族语言码(空回落 en;打包语言表 19 语
// 无中文,传 zh 不会报错但产物为噪声,须在入口拦截)。
var chatterboxLanguages = map[string]bool{
	"en": true, "ko": true, "de": true, "fr": true, "es": true, "it": true, "pt": true,
	"nl": true, "pl": true, "sv": true, "no": true, "da": true, "fi": true, "el": true,
	"hi": true, "ms": true, "sw": true, "tr": true, "ar": true,
}

type ttsTool struct {
	dataDir string
	models  *localmodel.Manager
	tts     *localruntime.TTSRuntime
	voices  *voicelib.Library // 音色库(可 nil,测试 seam):voice_id 克隆的直接来源

	// 测试 seam:缺省绑 TTSRuntime.Synthesize;转码 ffmpeg 可替换探测。
	synthesizeFn func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error)
	// kokoro 家族的合成 seam:缺省绑 KokoroTTS.Synthesize(sherpa 一次性子进程)。
	kokoroSynthesizeFn func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error)
	lookPath           func(string) (string, error)
}

func newTTSTool(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime, voices *voicelib.Library) *ttsTool {
	t := &ttsTool{dataDir: dataDir, models: models, tts: tts, voices: voices, lookPath: exec.LookPath}
	if tts != nil {
		// 测试路径 tts 可为 nil,缺省 synthesizeFn 保持 nil,Run 前置守卫兜底;
		// 生产路径(RegisterAll/AllTools)tts 非 nil,必绑真实实现。
		t.synthesizeFn = tts.Synthesize
	}
	if models != nil {
		t.kokoroSynthesizeFn = localruntime.NewKokoroTTS(dataDir, models).Synthesize
	}
	return t
}

func (t *ttsTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "local", Name: "tts", Title: "本地语音合成",
		Description: "本地合成（Qwen3-TTS / IndexTTS / Kokoro / Chatterbox / VoxCPM2）：参考音频、音色库克隆或预置音色，离线可用；IndexTTS 支持情感文本控制，VoxCPM2 支持多语含中文方言。", Group: "合成"}
}

func (t *ttsTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "合成文本", Type: provider.ParamText, Required: true, Group: "本地推理"},
		{Key: "model", Label: "本地模型", Type: provider.ParamEnum, Required: true, Group: "本地推理",
			Placeholder: "设置页已安装的本地 TTS 条目"},
		{Key: "mode", Label: "音色模式", Type: provider.ParamEnum, Required: true, Default: "clone",
			Options: []provider.ParamOption{{Value: "clone", Label: "参考音频克隆"}, {Value: "preset", Label: "预置音色"}}, Group: "本地推理"},
		{Key: "ref_text", Label: "参考音频转写", Type: provider.ParamText, Group: "本地推理",
			Placeholder: "参考音频实际说的内容(留空走纯音色克隆)"},
		{Key: "voice_id", Label: "音色库音色", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "从音色库选择,优先于临时上传"},
		{Key: "speaker", Label: "预置音色", Type: provider.ParamEnum, Group: "本地推理",
			Options: speakerOptions()},
		{Key: "instruct", Label: "风格指令", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "如:Very happy and energetic"},
		{Key: "style", Label: "音色描述", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "选填,VoxCPM2 直读/克隆时用自然语言描述音色,如:温柔的年轻女声"},
		// 语言取值由所选模型家族决定(qwen3 全名 / index 小写码),前端按家族渲染下拉
		{Key: "language", Label: "语言", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "由所选模型决定"},
		{Key: "emotion_text", Label: "情感文本", Type: provider.ParamString, Group: "本地推理",
			Placeholder: "选填,如:非常兴奋(IndexTTS 2.5)"},
		{Key: "emotion_alpha", Label: "情感强度", Type: provider.ParamFloat, Group: "本地推理",
			Placeholder: "0-1,默认 1.0"},
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
	// 家族与模式匹配:index_tts2/chatterbox 为纯克隆模型(无预置音色);kokoro 为纯
	// 预置模型(不支持克隆);voxcpm2 走克隆模式但参考音可留空(直读);qwen3 家族沿用
	// 文件名变体匹配校验。
	switch {
	case e.Family == "index_tts2" && mode == "preset":
		return provider.TaskOutput{}, fmt.Errorf("IndexTTS 为克隆模型,不支持预置音色")
	case e.Family == "chatterbox" && mode == "preset":
		return provider.TaskOutput{}, fmt.Errorf("Chatterbox 为克隆模型,不支持预置音色")
	case e.Family == "voxcpm2" && mode == "preset":
		return provider.TaskOutput{}, fmt.Errorf("VoxCPM2 为克隆模型,不支持预置音色")
	case e.Family == "kokoro" && mode == "clone":
		return provider.TaskOutput{}, fmt.Errorf("Kokoro 为预置音色模型,不支持参考音频克隆")
	case e.Family == "qwen3_tts":
		switch {
		case mode == "preset" && !strings.Contains(e.ID, "customvoice"):
			return provider.TaskOutput{}, fmt.Errorf("预置音色需要 CustomVoice 模型:当前 %s 为克隆模型,请切换音色模式或下载 CustomVoice 条目", e.Name)
		case mode == "clone" && strings.Contains(e.ID, "customvoice"):
			return provider.TaskOutput{}, fmt.Errorf("参考音频克隆需要 Base 模型:当前 %s 为预置音色模型,请切换音色模式或下载 Base 条目", e.Name)
		}
	}
	// 语言校验按家族:qwen3 收全名、index/kokoro 收小写码,非法值直述;空值回落家族默认
	language := paramString(in.Params, "language", "")
	switch e.Family {
	case "index_tts2":
		if language == "" {
			language = "auto"
		}
		if !indexLanguages[language] {
			return provider.TaskOutput{}, fmt.Errorf("参数错误: IndexTTS 语言仅支持 auto|zh|en|ja|es|ar,当前 %s", language)
		}
	case "kokoro":
		if language == "" {
			language = "auto"
		}
		if !kokoroLanguages[language] {
			return provider.TaskOutput{}, fmt.Errorf("参数错误: Kokoro 语言仅支持 auto|zh|en,当前 %s", language)
		}
	case "voxcpm2":
		if language == "" {
			language = "auto"
		}
		if !voxcpmLanguages[language] {
			return provider.TaskOutput{}, fmt.Errorf("参数错误: VoxCPM2 语言仅支持 auto|zh|en,当前 %s", language)
		}
	case "chatterbox":
		if language == "" {
			language = "en"
		}
		if !chatterboxLanguages[language] {
			return provider.TaskOutput{}, fmt.Errorf("参数错误: Chatterbox 暂不支持中文,语言仅支持 en|ko|de|fr|es|it|pt|nl|pl|sv|no|da|fi|el|hi|ms|sw|tr|ar,当前 %s", language)
		}
	default:
		if language == "" {
			language = "Chinese"
		}
		if !qwen3Languages[language] {
			return provider.TaskOutput{}, fmt.Errorf("参数错误: Qwen3-TTS 语言仅支持 Chinese|English|Japanese|Korean,当前 %s", language)
		}
	}

	req := localruntime.SynthRequest{ModelID: modelID, Family: e.Family, Language: language}
	// 情感参数仅 index_tts2 家族透传(qwen3 无此语义,直接忽略)
	if e.Family == "index_tts2" {
		req.EmotionText = paramString(in.Params, "emotion_text", "")
		req.EmotionAlpha = paramFloat(in.Params, "emotion_alpha")
	}
	if text := paramString(in.Params, "text", ""); text == "" {
		// 文本不在 params:与云端 TTS 一致由工具页 params.text 传入
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	} else {
		// 发音词典：合成前文本预处理（language 已是家族归一值，zh/Chinese/auto 等均能折叠匹配）
		req.Text = pronunciation.Apply(text, language)
	}
	// voxcpm2 音色设计:风格描述拼 "(style)" 前缀——在词典预处理之后拼,风格文本不受
	// 发音词典改写;audio.cpp 按上游约定解析 text 开头括号语法(长文本分块默认
	// tag_aware,风格标记随块保留)。语言模型自动处理,不透传 language 键。
	if e.Family == "voxcpm2" {
		if style := paramString(in.Params, "style", ""); style != "" {
			req.Text = "(" + style + ")" + req.Text
		}
		req.Language = ""
	}
	switch mode {
	case "clone":
		// 参考音频来源:params.voice_id(音色库,优先)> 临时上传(旧路,ffmpeg 转码);
		// voxcpm2 两者皆空=直读(模型默认音色),其余家族必填其一
		if voiceID := paramString(in.Params, "voice_id", ""); voiceID != "" {
			if t.voices == nil {
				return provider.TaskOutput{}, fmt.Errorf("音色不存在或未加载: %s", voiceID)
			}
			wav, err := t.voices.Path(voiceID)
			if err != nil {
				return provider.TaskOutput{}, fmt.Errorf("音色不存在或未加载: %s", voiceID)
			}
			// 库内成品已是 24kHz 单声道 pcm16,直接作参考音频,不经 convertRef 转码
			req.RefWav = wav
			req.RefText = paramString(in.Params, "ref_text", "")
		} else if ref := in.Files["audio"]; ref != "" {
			converted, err := t.convertRef(ctx, ref)
			if err != nil {
				return provider.TaskOutput{}, err
			}
			defer os.Remove(converted)
			req.RefWav = converted
			req.RefText = paramString(in.Params, "ref_text", "")
		} else if e.Family != "voxcpm2" {
			return provider.TaskOutput{}, fmt.Errorf("克隆模式需要参考音频:请从音色库选择,或上传/录制 3-60 秒清晰人声")
		}
	case "preset":
		sp := paramString(in.Params, "speaker", "")
		if sp == "" {
			return provider.TaskOutput{}, fmt.Errorf("预置模式需要选择音色: speaker")
		}
		req.Speaker = sp
		if e.Family == "kokoro" {
			// kokoro 的预置音色是 voices.bin 的 sid 位:名字 → sid 在此归一,
			// 未知音色直述(前端下拉来自 /api/voices,手拼参数在此拦截)
			sid, ok := KokoroVoiceSID(sp)
			if !ok {
				return provider.TaskOutput{}, fmt.Errorf("未知 Kokoro 音色: %s", sp)
			}
			req.SpeakerSID = sid
		} else {
			req.Instruct = paramString(in.Params, "instruct", "")
		}
	}
	// chatterbox 纯零样本克隆:参考转写无语义,不透传(server 只收 voice_ref)
	if e.Family == "chatterbox" {
		req.RefText = ""
	}

	report(5, "准备本地合成…", nil)
	// 运行时分流:kokoro 走 sherpa 一次性子进程,qwen3/index 走 audiocpp 常驻 server
	synth := t.synthesizeFn
	if e.Family == "kokoro" {
		if t.kokoroSynthesizeFn == nil {
			return provider.TaskOutput{}, fmt.Errorf("本地合成引擎不可用:请重启服务后重试")
		}
		synth = t.kokoroSynthesizeFn
	}
	out, err := synth(ctx, req, func(p int, note string) { report(p, note, nil) })
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
	// engine 元数据随家族:qwen3/index 为 audiocpp,kokoro 为 sherpa-onnx
	engine := e.RequiresEngine
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: rel, Format: "wav", Size: fi.Size(),
			Meta: map[string]any{"engine": engine, "model": modelID, "mode": mode},
		}},
		Summary: map[string]any{"engine": engine, "model": e.Name, "mode": mode},
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

// paramFloat 宽容取参(float64/int 及数字字符串均可;缺失/不可解析归 0,
// 与 paramString 同款宽容口径)。消费方按 (0,1) 开区间自行裁剪。
func paramFloat(params map[string]any, key string) float64 {
	v, ok := params[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}
