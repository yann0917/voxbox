package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/voicelib"
)

func newTestPkg(t *testing.T) (string, *localmodel.Manager) {
	t.Helper()
	// 用未导出构造不可行——provider 包只拿 *localmodel.Manager;直接 NewManager 后靠 seam 注入不查盘。
	// 事实:工具的安装校验通过 models.Installed;测试用 manager 的真实目录播种即可跳过下载。
	dataDir := t.TempDir()
	return dataDir, localmodel.NewManager(dataDir)
}

func TestTTSRequiresEngineAndModel(t *testing.T) {
	_, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil, nil) // runtime/voices nil:synthesizeFn seam 会替换
	if _, err := tts.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"model": "qwen3-tts-base-q8", "mode": "clone",
	}}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("引擎未安装应报错")
	}
}

func TestTTSCloneRequiresRefAudio(t *testing.T) {
	dataDir, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil, nil)
	// 播种 audiocpp 引擎与 base 模型的已安装态,让 Run 走到参考音频校验分支
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "qwen3-tts-base-q8", "x.gguf")
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "qwen3-tts-base-q8", "mode": "clone"},
		Files:  map[string]string{},
	}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("clone 模式缺参考音频应报错")
	}
}

func TestTTSModeModelMismatch(t *testing.T) {
	dataDir, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil, nil)
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	// seedModel 安装一个 base 条目,然后 preset 模式选 speaker → 应报模式/模型不匹配
	seedModelFile(t, dataDir, m, "qwen3-tts-base-q8", "x.gguf")
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "qwen3-tts-base-q8", "mode": "preset", "speaker": "Vivian"},
	}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("base 模型配 preset 模式应报错")
	}
}

func TestASRRequiresInstallations(t *testing.T) {
	_, m := newTestPkg(t)
	asr := newASRTool(t.TempDir(), m)
	if _, err := asr.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"language": "auto", "itn": true},
		Files:  map[string]string{"audio": "in.wav"},
	}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("sherpa 引擎未安装应报错")
	}
}

func TestCustomVoicesNine(t *testing.T) {
	if len(CustomVoices()) != 9 {
		t.Fatalf("预置音色应为 9 个,实际 %d", len(CustomVoices()))
	}
}

func TestProviderCardShape(t *testing.T) {
	c := ProviderCard()
	if c.Name != "local" || c.Kind != provider.KindLocal || c.Order != 85 {
		t.Fatalf("卡声明不符: %+v", c)
	}
}

// TestTTSHappyPathPreset 预置音色全链路:引擎+模型已安装 → seam 注入假合成 →
// 产出 1 个 audio artifact(Path 为 dataDir 相对,Meta 含 engine/model/mode)。
func TestTTSHappyPathPreset(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "qwen3-tts-customvoice-q8", "qwen3-tts-12hz-1.7b-customvoice-q8_0.gguf")
	tts := newTTSTool(dataDir, m, nil, nil) // runtime/voices nil:synthesizeFn 走 seam 注入
	outWav := filepath.Join(dataDir, "tts", "local_fake.wav")
	if err := os.MkdirAll(filepath.Dir(outWav), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outWav, []byte("RIFF...."), 0o644); err != nil {
		t.Fatal(err)
	}
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		if req.ModelID != "qwen3-tts-customvoice-q8" || req.Speaker != "Vivian" || req.Text != "你好" {
			t.Errorf("SynthRequest 不符: %+v", req)
		}
		return outWav, nil
	}
	out, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "qwen3-tts-customvoice-q8", "mode": "preset", "speaker": "Vivian", "text": "你好"},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("应产出 1 个 artifact,实际 %d", len(out.Artifacts))
	}
	art := out.Artifacts[0]
	if art.Kind != "audio" || art.Format != "wav" {
		t.Fatalf("artifact 形状不符: %+v", art)
	}
	if want := filepath.Join("tts", "local_fake.wav"); art.Path != want {
		t.Fatalf("Path 应为 dataDir 相对 %q,实际 %q", want, art.Path)
	}
	if art.Meta["engine"] != "audiocpp" || art.Meta["model"] != "qwen3-tts-customvoice-q8" || art.Meta["mode"] != "preset" {
		t.Fatalf("artifact Meta 应含 engine/model/mode: %+v", art.Meta)
	}
}

// TestASRHappyPath 本地识别全链路:引擎+模型已安装 + 假 wav → seam 注入假转写 →
// txt 产物内容与 artifact 形状(transcript/txt/Meta engine/model/lang)。
func TestASRHappyPath(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "sherpa-onnx", "sherpa-onnx-offline")
	seedModelFile(t, dataDir, m, "sensevoice-int8", "model.onnx")
	wav := filepath.Join(dataDir, "in.wav")
	if err := os.WriteFile(wav, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	asr := newASRTool(dataDir, m)
	const text = "今天天气不错"
	asr.transcribeFn = func(ctx context.Context, binPath, modelDir, wavPath, language string, itn bool) (localruntime.SherpaResult, error) {
		if filepath.Base(binPath) != "sherpa-onnx-offline" {
			t.Errorf("binPath 应指向引擎二进制: %q", binPath)
		}
		if want := filepath.Join(dataDir, "models", "sensevoice-int8"); modelDir != want {
			t.Errorf("modelDir 不符: %q", modelDir)
		}
		if language != "zh" || !itn {
			t.Errorf("language/itn 不符: %q %v", language, itn)
		}
		return localruntime.SherpaResult{Text: text, Lang: "zh", Emotion: "happy"}, nil
	}
	out, err := asr.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"language": "zh", "itn": true},
		Files:  map[string]string{"audio": wav},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("应产出 1 个 artifact,实际 %d", len(out.Artifacts))
	}
	art := out.Artifacts[0]
	if art.Kind != "transcript" || art.Format != "txt" {
		t.Fatalf("artifact 形状不符: %+v", art)
	}
	if !strings.HasPrefix(art.Path, "asr") || !strings.HasSuffix(art.Path, "in_local.txt") {
		t.Fatalf("Path 应为 asr/<请求id>/in_local.txt: %q", art.Path)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, art.Path))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != text {
		t.Fatalf("txt 产物内容不符: %q", raw)
	}
	if art.Size != int64(len(text)) {
		t.Fatalf("Size 应为文本字节数 %d,实际 %d", len(text), art.Size)
	}
	if art.Meta["engine"] != "sherpa-onnx" || art.Meta["model"] != "sensevoice-int8" || art.Meta["lang"] != "zh" {
		t.Fatalf("artifact Meta 应含 engine/model/lang: %+v", art.Meta)
	}
}

// —— Task 3:index_tts2 家族 / voice_id 克隆 / 情感透传 / 语言按家族 ——

// seedIndexTTS 播种 audiocpp 引擎 + index_tts2 条目(内置 catalog 的 index-tts2_5-q8)的已安装态。
func seedIndexTTS(t *testing.T, dataDir string, m *localmodel.Manager) {
	t.Helper()
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "index-tts2_5-q8", "index-tts2_5-q8_0.gguf")
}

// seedVoice 手工落一个音色目录(绕过 ffmpeg 入库):8 位 hex id + voice.wav,
// 返回成品 wav 绝对路径(与 voicelib.Path 的返回口径一致)。
func seedVoice(t *testing.T, dataDir, id string) string {
	t.Helper()
	dir := filepath.Join(dataDir, "voices", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(dir, "voice.wav")
	if err := os.WriteFile(wav, []byte("RIFFfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wav
}

// fakeOutWav 造一个可作合成产物的假 wav(相对 dataDir 落盘)。
func fakeOutWav(t *testing.T, dataDir string) string {
	t.Helper()
	outWav := filepath.Join(dataDir, "tts", "local_fake.wav")
	if err := os.MkdirAll(filepath.Dir(outWav), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outWav, []byte("RIFF...."), 0o644); err != nil {
		t.Fatal(err)
	}
	return outWav
}

// noSynthStub 校验失败路径专用的合成 seam:意外走到合成即当场报错,
// 避免前置 synthesizeFn==nil 守卫抢先返回「引擎不可用」掩盖真实校验行为。
func noSynthStub(t *testing.T) func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
	return func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		t.Errorf("校验应在此前拒绝请求,不应到达合成: %+v", req)
		return "", fmt.Errorf("不应到达合成")
	}
}

// TestTTSIndexPresetRejected index_tts2 为纯克隆模型:preset 模式直述不支持。
func TestTTSIndexPresetRejected(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedIndexTTS(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "index-tts2_5-q8", "mode": "preset", "speaker": "Vivian", "text": "你好"},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "IndexTTS 为克隆模型") {
		t.Fatalf("index 条目 preset 应直述不支持: %v", err)
	}
}

// TestTTSCloneWithVoiceID voice_id 克隆:RefWav 直接取库内 wav 绝对路径(不经 convertRef,
// ffmpeg 置障验证);情感与 language 按 index_tts2 家族透传;不传 language 回落 auto。
func TestTTSCloneWithVoiceID(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedIndexTTS(t, dataDir, m)
	voices := voicelib.New(dataDir)
	wantWav := seedVoice(t, dataDir, "abcdef12")
	tts := newTTSTool(dataDir, m, nil, voices)
	tts.lookPath = func(string) (string, error) { return "", fmt.Errorf("ffmpeg 不可用") }
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"model": "index-tts2_5-q8", "mode": "clone", "voice_id": "abcdef12",
			"text": "你好", "language": "zh", "emotion_text": "兴奋", "emotion_alpha": 0.7,
		},
		Files: map[string]string{},
	}, func(p int, note string, d map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	if got.RefWav != wantWav {
		t.Fatalf("RefWav 应为库内 wav 绝对路径 %q,实际 %q", wantWav, got.RefWav)
	}
	if got.EmotionText != "兴奋" || got.EmotionAlpha != 0.7 {
		t.Fatalf("情感参数应透传: %+v", got)
	}
	if got.Language != "zh" {
		t.Fatalf("index 家族语言应透传 zh: %+v", got)
	}
	// 不传 language → 回落 auto;不传情感 → 零值
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "index-tts2_5-q8", "mode": "clone", "voice_id": "abcdef12", "text": "你好"},
		Files:  map[string]string{},
	}, func(p int, note string, d map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	if got.Language != "auto" || got.EmotionText != "" || got.EmotionAlpha != 0 {
		t.Fatalf("缺省 language 应回落 auto 且情感为零值: %+v", got)
	}
}

// TestTTSCloneVoiceIDPrecedence voice_id 与临时上传参考音频同传:voice_id 优先。
func TestTTSCloneVoiceIDPrecedence(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedIndexTTS(t, dataDir, m)
	voices := voicelib.New(dataDir)
	wantWav := seedVoice(t, dataDir, "abcdef12")
	tts := newTTSTool(dataDir, m, nil, voices)
	tts.lookPath = func(string) (string, error) { return "", fmt.Errorf("ffmpeg 不可用") }
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	upload := filepath.Join(dataDir, "upload.wav")
	if err := os.WriteFile(upload, []byte("RIFFupload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "index-tts2_5-q8", "mode": "clone", "voice_id": "abcdef12", "text": "你好"},
		Files:  map[string]string{"audio": upload},
	}, func(p int, note string, d map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	if got.RefWav != wantWav || got.RefWav == upload {
		t.Fatalf("voice_id 应优先于临时上传: got=%q want=%q upload=%q", got.RefWav, wantWav, upload)
	}
}

// TestTTSCloneVoiceNotFound voice_id 指向不存在的音色(或音色库未注入)→ 直述错误。
func TestTTSCloneVoiceNotFound(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedIndexTTS(t, dataDir, m)
	params := map[string]any{"model": "index-tts2_5-q8", "mode": "clone", "voice_id": "deadbeef", "text": "你好"}
	run := func(tts *ttsTool) error {
		tts.synthesizeFn = noSynthStub(t)
		_, err := tts.Run(context.Background(), provider.TaskInput{Params: params, Files: map[string]string{}},
			func(p int, note string, d map[string]any) {})
		return err
	}
	if err := run(newTTSTool(dataDir, m, nil, voicelib.New(dataDir))); err == nil || !strings.Contains(err.Error(), "音色不存在或未加载") {
		t.Fatalf("音色库为空应直述不存在: %v", err)
	}
	if err := run(newTTSTool(dataDir, m, nil, nil)); err == nil || !strings.Contains(err.Error(), "音色不存在或未加载") {
		t.Fatalf("音色库未注入应直述不存在: %v", err)
	}
}

// TestTTSLanguageByFamily 语言校验按家族:qwen3 只收全名,index 只收小写码;
// 校验先于参考音频要求(参数错误优先直述)。
func TestTTSLanguageByFamily(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "qwen3-tts-base-q8", "x.gguf")
	seedModelFile(t, dataDir, m, "index-tts2_5-q8", "index-tts2_5-q8_0.gguf")
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	run := func(params map[string]any) error {
		_, err := tts.Run(context.Background(), provider.TaskInput{Params: params, Files: map[string]string{}},
			func(p int, note string, d map[string]any) {})
		return err
	}
	if err := run(map[string]any{"model": "qwen3-tts-base-q8", "mode": "clone", "language": "auto", "text": "你好"}); err == nil || !strings.Contains(err.Error(), "语言") {
		t.Fatalf("qwen3 应拒绝 auto: %v", err)
	}
	if err := run(map[string]any{"model": "index-tts2_5-q8", "mode": "clone", "language": "Korean", "text": "你好"}); err == nil || !strings.Contains(err.Error(), "语言") {
		t.Fatalf("index 应拒绝 Korean: %v", err)
	}
}

// TestTTSQwen3IgnoresEmotion 情感参数仅 index_tts2 透传:qwen3 条目同传情感 → 忽略,
// 合法语言(Korean)照常透传(customvoice/base 匹配校验回归)。
func TestTTSQwen3IgnoresEmotion(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "qwen3-tts-customvoice-q8", "qwen3-tts-12hz-1.7b-customvoice-q8_0.gguf")
	tts := newTTSTool(dataDir, m, nil, nil)
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"model": "qwen3-tts-customvoice-q8", "mode": "preset", "speaker": "Vivian", "text": "你好",
			"language": "Korean", "emotion_text": "兴奋", "emotion_alpha": 0.7,
		},
	}, func(p int, note string, d map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	if got.Language != "Korean" {
		t.Fatalf("qwen3 合法语言应透传: %+v", got)
	}
	if got.EmotionText != "" || got.EmotionAlpha != 0 {
		t.Fatalf("qwen3 应忽略情感参数: %+v", got)
	}
}

// —— kokoro 家族:纯预置模型,sid 归一,sherpa 子进程分流,engine 元数据随条目 ——

// seedKokoroTTS 播种 sherpa 引擎 + kokoro 归档条目的已安装态。
func seedKokoroTTS(t *testing.T, dataDir string, m *localmodel.Manager) {
	t.Helper()
	seedEngine(t, dataDir, m, "sherpa-onnx", "sherpa-onnx-offline")
	seedModelFile(t, dataDir, m, "kokoro-v1.0", "model.onnx")
}

// TestTTSKokoroRejectsClone kokoro 为纯预置模型:clone 模式直述不支持。
func TestTTSKokoroRejectsClone(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedKokoroTTS(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "kokoro-v1.0", "mode": "clone", "text": "你好"},
		Files:  map[string]string{},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "Kokoro 为预置音色模型") {
		t.Fatalf("kokoro 条目 clone 应直述不支持: %v", err)
	}
}

// TestTTSKokoroUnknownVoice 预置音色不在内置表(手拼参数)→ 直述未知音色。
func TestTTSKokoroUnknownVoice(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedKokoroTTS(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "kokoro-v1.0", "mode": "preset", "speaker": "zf_999", "text": "你好"},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "未知 Kokoro 音色") {
		t.Fatalf("未知 kokoro 音色应直述: %v", err)
	}
}

// TestTTSKokoroHappyPath 预置音色全链路:speaker 名归一为 sid,请求分流到
// kokoro seam(audiocpp seam 不得触达),artifact Meta engine=sherpa-onnx;
// 语言缺省回落 auto。
func TestTTSKokoroHappyPath(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedKokoroTTS(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.kokoroSynthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	out, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "kokoro-v1.0", "mode": "preset", "speaker": "zf_xiaobei", "text": "你好"},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelID != "kokoro-v1.0" || got.SpeakerSID != 45 || got.Text != "你好" || got.Language != "auto" {
		t.Fatalf("kokoro 请求不符: %+v", got)
	}
	if got.Speaker != "zf_xiaobei" || got.Instruct != "" {
		t.Fatalf("kokoro 不应透传 instruct: %+v", got)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("应产出 1 个 artifact,实际 %d", len(out.Artifacts))
	}
	if out.Artifacts[0].Meta["engine"] != "sherpa-onnx" || out.Artifacts[0].Meta["mode"] != "preset" {
		t.Fatalf("artifact Meta engine 应随条目为 sherpa-onnx: %+v", out.Artifacts[0].Meta)
	}
}

// TestTTSKokoroLanguageRuling kokoro 语言仅收 auto|zh|en,缺省 auto。
func TestTTSKokoroLanguageRuling(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedKokoroTTS(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "kokoro-v1.0", "mode": "preset", "speaker": "zf_xiaobei", "language": "Korean", "text": "你好"},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "语言") {
		t.Fatalf("kokoro 应拒绝 Korean: %v", err)
	}
}

// TestKokoroVoicesTable 内置音色表:sid 连续 0..52,经典中文音色段与
// generate_voices_bin.py 实测一致(zf_xiaobei=45,zm_yunyang=52)。
func TestKokoroVoicesTable(t *testing.T) {
	voices := KokoroVoices()
	if len(voices) != 53 {
		t.Fatalf("kokoro 内置音色应 53 个,实际 %d", len(voices))
	}
	for i, v := range voices {
		if v.Sid != i {
			t.Fatalf("sid 应连续无空洞: voices[%d].Sid=%d", i, v.Sid)
		}
	}
	if voices[0].ID != "af_alloy" || voices[2].ID != "af_bella" {
		t.Fatalf("英文音色段不符: %v", voices[:3])
	}
	if voices[45].ID != "zf_xiaobei" {
		t.Fatalf("sid 45 应为 zf_xiaobei,实际 %q", voices[45].ID)
	}
	if last := voices[52].ID; last != "zm_yunyang" {
		t.Fatalf("sid 52 应为 zm_yunyang,实际 %q", last)
	}
	if sid, ok := KokoroVoiceSID("zf_xiaobei"); !ok || sid != 45 {
		t.Fatalf("KokoroVoiceSID(zf_xiaobei) 应为 45: %d %v", sid, ok)
	}
	if _, ok := KokoroVoiceSID("nope"); ok {
		t.Fatal("未知音色应返回 false")
	}
}

// —— chatterbox 家族:纯克隆模型,RefText 不透传,语言 19 语无中文 ——

// seedChatterbox 播种 audiocpp 引擎 + chatterbox 条目的已安装态。
func seedChatterbox(t *testing.T, dataDir string, m *localmodel.Manager) {
	t.Helper()
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "chatterbox-q8", "chatterbox-q8_0.gguf")
}

// TestTTSChatterboxRejectsPreset chatterbox 为纯克隆模型:preset 模式直述不支持。
func TestTTSChatterboxRejectsPreset(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedChatterbox(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "chatterbox-q8", "mode": "preset", "speaker": "Vivian", "text": "hello"},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "Chatterbox 为克隆模型") {
		t.Fatalf("chatterbox 条目 preset 应直述不支持: %v", err)
	}
}

// TestTTSChatterboxLanguageRuling chatterbox 语言 19 语无中文:zh 拒绝,缺省 en。
func TestTTSChatterboxLanguageRuling(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedChatterbox(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "chatterbox-q8", "mode": "clone", "language": "zh", "text": "hello"},
		Files:  map[string]string{},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "暂不支持中文") {
		t.Fatalf("chatterbox 应拒绝 zh 并直述: %v", err)
	}
}

// TestTTSChatterboxHappyPath 克隆全链路:Family 透传,RefText 丢弃(零样本无转写语义),
// 语言缺省回落 en,artifact Meta engine 随条目为 audiocpp。
func TestTTSChatterboxHappyPath(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedChatterbox(t, dataDir, m)
	voices := voicelib.New(dataDir)
	wantWav := seedVoice(t, dataDir, "abcdef12")
	tts := newTTSTool(dataDir, m, nil, voices)
	tts.lookPath = func(string) (string, error) { return "", fmt.Errorf("ffmpeg 不可用") }
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	out, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"model": "chatterbox-q8", "mode": "clone", "voice_id": "abcdef12",
			"text": "hello", "ref_text": "some transcript",
		},
		Files: map[string]string{},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if got.Family != "chatterbox" || got.RefWav != wantWav {
		t.Fatalf("Family/RefWav 不符: %+v", got)
	}
	if got.RefText != "" {
		t.Fatalf("chatterbox 应丢弃 RefText: %+v", got)
	}
	if got.Language != "en" {
		t.Fatalf("缺省语言应回落 en: %+v", got)
	}
	if out.Artifacts[0].Meta["engine"] != "audiocpp" {
		t.Fatalf("artifact Meta engine 应为 audiocpp: %+v", out.Artifacts[0].Meta)
	}
}

// —— voxcpm2 家族:克隆+直读(参考音可全空),style 前缀,语言不透传 ——

// seedVoxCPM 播种 audiocpp 引擎 + voxcpm2 条目的已安装态。
func seedVoxCPM(t *testing.T, dataDir string, m *localmodel.Manager) {
	t.Helper()
	seedEngine(t, dataDir, m, "audiocpp", "audiocpp_server")
	seedModelFile(t, dataDir, m, "voxcpm2-q8", "voxcpm2-q8_0.gguf")
}

// TestTTSVoxCPMPresetRejected voxcpm2 无预置音色:preset 模式直述不支持。
func TestTTSVoxCPMPresetRejected(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedVoxCPM(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "voxcpm2-q8", "mode": "preset", "speaker": "Vivian", "text": "你好"},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "VoxCPM2 为克隆模型") {
		t.Fatalf("voxcpm2 条目 preset 应直述不支持: %v", err)
	}
}

// TestTTSVoxCPMLanguageRuling 语言仅收 auto|zh|en。
func TestTTSVoxCPMLanguageRuling(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedVoxCPM(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	tts.synthesizeFn = noSynthStub(t)
	_, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"model": "voxcpm2-q8", "mode": "clone", "language": "Korean", "text": "你好"},
		Files:  map[string]string{},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "语言") {
		t.Fatalf("voxcpm2 应拒绝 Korean: %v", err)
	}
}

// TestTTSVoxCPMDirectRead 直读:clone 模式无 voice_id 无上传不再报错,RefWav 空;
// style 拼括号前缀且在词典处理之后;Language 置空不透传引擎。
func TestTTSVoxCPMDirectRead(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedVoxCPM(t, dataDir, m)
	tts := newTTSTool(dataDir, m, nil, nil)
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	// 发音词典挂钩子会让 Apply 走真实逻辑; pronunciation 是进程单例,默认无词条=原样透传
	out, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"model": "voxcpm2-q8", "mode": "clone", "text": "你好，世界。",
			"style": "温柔的年轻女声",
		},
		Files: map[string]string{},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if got.RefWav != "" || got.RefText != "" {
		t.Fatalf("直读不应携带参考音频: %+v", got)
	}
	if got.Family != "voxcpm2" {
		t.Fatalf("Family 应为 voxcpm2: %+v", got)
	}
	if got.Language != "" {
		t.Fatalf("voxcpm2 不应透传 language: %+v", got)
	}
	if want := "(温柔的年轻女声)你好，世界。"; got.Text != want {
		t.Fatalf("style 应拼括号前缀: want %q got %q", want, got.Text)
	}
	if out.Artifacts[0].Meta["engine"] != "audiocpp" {
		t.Fatalf("artifact Meta engine 应为 audiocpp: %+v", out.Artifacts[0].Meta)
	}
}

// TestTTSVoxCPMClone 克隆:voice_id 参考照常透传,ref_text 保留(终极克隆转写,
// 不像 chatterbox 那样丢弃)。
func TestTTSVoxCPMClone(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedVoxCPM(t, dataDir, m)
	voices := voicelib.New(dataDir)
	wantWav := seedVoice(t, dataDir, "abcdef12")
	tts := newTTSTool(dataDir, m, nil, voices)
	tts.lookPath = func(string) (string, error) { return "", fmt.Errorf("ffmpeg 不可用") }
	outWav := fakeOutWav(t, dataDir)
	var got localruntime.SynthRequest
	tts.synthesizeFn = func(ctx context.Context, req localruntime.SynthRequest, report func(p int, note string)) (string, error) {
		got = req
		return outWav, nil
	}
	if _, err := tts.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{
			"model": "voxcpm2-q8", "mode": "clone", "voice_id": "abcdef12",
			"text": "你好", "ref_text": "参考音频的转写内容",
		},
		Files: map[string]string{},
	}, func(p int, note string, d map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	if got.RefWav != wantWav || got.RefText != "参考音频的转写内容" {
		t.Fatalf("克隆应透传参考音频与转写: %+v", got)
	}
	if got.Language != "" {
		t.Fatalf("voxcpm2 不应透传 language: %+v", got)
	}
}

// TestASRProducesSegments tokens/timestamps 1:1 时按句末标点聚合:subtitle 产物 +
// summary.segments(与云端 ASR 同形状,字幕工坊可导入);转写产物仍为 ITN 文本。
func TestASRProducesSegments(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "sherpa-onnx", "sherpa-onnx-offline")
	seedModelFile(t, dataDir, m, "sensevoice-int8", "model.int8.onnx")
	wav := filepath.Join(dataDir, "in.wav")
	if err := os.WriteFile(wav, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	asr := newASRTool(dataDir, m)
	asr.transcribeFn = func(ctx context.Context, binPath, modelDir, wavPath, language string, itn bool) (localruntime.SherpaResult, error) {
		return localruntime.SherpaResult{
			Text:       "你好，世界！好",
			Lang:       "<|zh|>",
			Tokens:     []string{"你", "好", "，", "世", "界", "！", "好"},
			Timestamps: []float64{0.1, 0.2, 0.3, 0.5, 0.6, 0.7, 0.9},
		}, nil
	}
	out, err := asr.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": wav},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	// 产物：转写 txt + 字幕 srt
	if len(out.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2（transcript+subtitle）", len(out.Artifacts))
	}
	srt := out.Artifacts[1]
	if srt.Kind != "subtitle" || srt.Format != "srt" || !strings.HasSuffix(srt.Path, ".srt") {
		t.Fatalf("subtitle artifact 不符: %+v", srt)
	}
	// summary.segments：句末标点聚句（「你好，世界！」+ 残余尾句「好」）后过断句规范——
	// 尾句 0ms 过短并入前段（合并后 800ms 仍不足最短显示 833ms），整段延长至 933ms
	raw, _ := json.Marshal(out.Summary["segments"])
	var segs []map[string]any
	if err := json.Unmarshal(raw, &segs); err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("过短尾句应并入前段成一句: %s", raw)
	}
	if segs[0]["text"] != "你好，世界！好" || segs[0]["start_ms"] != float64(100) || segs[0]["end_ms"] != float64(933) {
		t.Fatalf("规范后段不符: %+v", segs[0])
	}
	// SRT 内容与规范段时间对齐
	srtBytes, err := os.ReadFile(filepath.Join(dataDir, srt.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(srtBytes), "00:00:00,100 --> 00:00:00,933") ||
		!strings.Contains(string(srtBytes), "你好，世界！好") {
		t.Fatalf("SRT 内容不符: %s", srtBytes)
	}
}

// —— 配音即字幕链路:非 wav 输入自动转码 ——

// TestASRTranscodesNonWav mp3 输入 → ffmpeg 转码(24k mono)→ sherpa 收转码产物;
// 转写产物以原始输入名为基;转码临时文件用后即清。
func TestASRTranscodesNonWav(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "sherpa-onnx", "sherpa-onnx-offline")
	seedModelFile(t, dataDir, m, "sensevoice-int8", "model.int8.onnx")
	mp3 := filepath.Join(dataDir, "in.mp3")
	if err := os.WriteFile(mp3, []byte("FAKE_MP3"), 0o644); err != nil {
		t.Fatal(err)
	}
	asr := newASRTool(dataDir, m)
	var gotWav string
	// lookPath 必须注入:CI runner 无 ffmpeg,不注入会先死在环境探测上
	// (缺 ffmpeg 分支由 TestASRTranscodeNeedsFFmpeg 显式覆盖)
	asr.lookPath = func(string) (string, error) { return "ffmpeg", nil }
	asr.transcodeFn = func(ctx context.Context, src, dst string) error {
		if src != mp3 || filepath.Ext(dst) != ".wav" {
			t.Errorf("转码参数不符: src=%q dst=%q", src, dst)
		}
		return os.WriteFile(dst, []byte("RIFF"), 0o644)
	}
	asr.transcribeFn = func(ctx context.Context, binPath, modelDir, wav, language string, itn bool) (localruntime.SherpaResult, error) {
		gotWav = wav
		if filepath.Ext(wav) != ".wav" {
			t.Errorf("sherpa 应收到 wav: %q", wav)
		}
		if _, err := os.Stat(wav); err != nil {
			t.Errorf("转码产物应在位: %v", err)
		}
		return localruntime.SherpaResult{Text: "你好"}, nil
	}
	out, err := asr.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"language": "zh"},
		Files:  map[string]string{"audio": mp3},
	}, func(p int, note string, d map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if gotWav == "" || gotWav == mp3 {
		t.Fatalf("应收到转码产物路径: %q", gotWav)
	}
	if !strings.HasSuffix(out.Artifacts[0].Path, "in_local.txt") {
		t.Fatalf("转写产物应以原始输入名为基: %q", out.Artifacts[0].Path)
	}
	// 转码临时文件用后即清(Run 返回后)
	if _, err := os.Stat(gotWav); !os.IsNotExist(err) {
		t.Fatalf("转码临时文件应已清理: %v", err)
	}
}

// TestASRWavPassthrough wav 输入原样透传,不经转码。
func TestASRWavPassthrough(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "sherpa-onnx", "sherpa-onnx-offline")
	seedModelFile(t, dataDir, m, "sensevoice-int8", "model.int8.onnx")
	wav := filepath.Join(dataDir, "in.wav")
	if err := os.WriteFile(wav, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	asr := newASRTool(dataDir, m)
	asr.transcodeFn = func(ctx context.Context, src, dst string) error {
		t.Errorf("wav 输入不应触发转码: %q", src)
		return nil
	}
	asr.transcribeFn = func(ctx context.Context, binPath, modelDir, wavPath, language string, itn bool) (localruntime.SherpaResult, error) {
		if wavPath != wav {
			t.Errorf("应原样透传: %q", wavPath)
		}
		return localruntime.SherpaResult{Text: "你好"}, nil
	}
	if _, err := asr.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": wav},
	}, func(p int, note string, d map[string]any) {}); err != nil {
		t.Fatal(err)
	}
}

// TestASRTranscodeNeedsFFmpeg 非 wav 且无 ffmpeg → 直述依赖与安装指引。
func TestASRTranscodeNeedsFFmpeg(t *testing.T) {
	dataDir, m := newTestPkg(t)
	seedEngine(t, dataDir, m, "sherpa-onnx", "sherpa-onnx-offline")
	seedModelFile(t, dataDir, m, "sensevoice-int8", "model.int8.onnx")
	mp3 := filepath.Join(dataDir, "in.mp3")
	if err := os.WriteFile(mp3, []byte("FAKE_MP3"), 0o644); err != nil {
		t.Fatal(err)
	}
	asr := newASRTool(dataDir, m)
	asr.lookPath = func(string) (string, error) { return "", fmt.Errorf("not found") }
	_, err := asr.Run(context.Background(), provider.TaskInput{
		Files: map[string]string{"audio": mp3},
	}, func(p int, note string, d map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("缺 ffmpeg 应直述: %v", err)
	}
}

// —— 播种 helper:绕过下载,直接构造 installed 态(Task 3 的访问器读 manifest)——

func seedEngine(t *testing.T, dataDir string, m *localmodel.Manager, id, binaryName string) {
	t.Helper()
	// 目录布局(NewManager(dataDir)):引擎安装根为 <dataDir>/engines,条目目录
	// engines/<id>,发布包整体解到 engines/<id>/pkg/。播种 = 假二进制 + manifest
	// (id/revision 与目录条目一致——catalog 缺省 revision 被规整为 "master",
	// binary 指向已存在的假二进制)。播完即断言 Installed,布局漂移当场暴露。
	pkgDir := filepath.Join(dataDir, "engines", id, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, binaryName), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mf := map[string]any{
		"id":           id,
		"revision":     "master",
		"binary":       "pkg/" + binaryName,
		"completed_at": "2026-01-01T00:00:00Z",
	}
	raw, err := json.Marshal(mf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "engines", id, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.Installed(id) {
		t.Fatalf("播种引擎 %s 后 Installed 仍为 false,播种布局与访问器判定不符", id)
	}
}

func seedModelFile(t *testing.T, dataDir string, m *localmodel.Manager, id, fileName string) {
	t.Helper()
	// 模型条目安装根为 <dataDir>/models:播种 = 单文件 + manifest(files 数组含该文件)。
	dir := filepath.Join(dataDir, "models", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("fake-model-bytes")
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	mf := map[string]any{
		"id":       id,
		"revision": "master",
		"files":    []map[string]any{{"path": fileName, "size": len(data)}},
	}
	raw, err := json.Marshal(mf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.Installed(id) {
		t.Fatalf("播种模型 %s 后 Installed 仍为 false,播种布局与访问器判定不符", id)
	}
}
