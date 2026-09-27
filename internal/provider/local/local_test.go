package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
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
	tts := newTTSTool(t.TempDir(), m, nil) // runtime nil:synthesizeFn seam 会替换
	if _, err := tts.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"model": "qwen3-tts-base-q8", "mode": "clone",
	}}, func(p int, note string, d map[string]any) {}); err == nil {
		t.Fatal("引擎未安装应报错")
	}
}

func TestTTSCloneRequiresRefAudio(t *testing.T) {
	dataDir, m := newTestPkg(t)
	tts := newTTSTool(t.TempDir(), m, nil)
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
	tts := newTTSTool(t.TempDir(), m, nil)
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
	tts := newTTSTool(dataDir, m, nil) // runtime nil:synthesizeFn 走 seam 注入
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
	seedModelFile(t, dataDir, m, "sensevoice-int8", "model.int8.onnx")
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
