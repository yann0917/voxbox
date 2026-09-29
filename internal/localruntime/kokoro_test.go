package localruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/localmodel"
)

// writeArgvRecordingKokoro 生成把收到的 argv 写进 $KOKORO_ARGV_OUT 的 mock,并按
// --output-filename= 参数落一个假 wav(模拟 sherpa-onnx-offline-tts 直接写产物文件)。
func writeArgvRecordingKokoro(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock 依赖 shebang,windows 冒烟覆盖")
	}
	bin := filepath.Join(dir, "fake-kokoro-tts")
	script := "#!/bin/sh\n" +
		"[ -n \"$KOKORO_ARGV_OUT\" ] && printf '%s\\n' \"$@\" > \"$KOKORO_ARGV_OUT\"\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in --output-filename=*) : > \"${a#--output-filename=}\" ;; esac\n" +
		"done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// seedKokoroModelFiles 补齐启动前校验要求的模型文件(espeak-ng-data 为目录)。
func seedKokoroModelFiles(t *testing.T, dir string) {
	t.Helper()
	_ = os.MkdirAll(dir, 0o755)
	for _, name := range []string{"model.onnx", "voices.bin", "tokens.txt"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(dir, "espeak-ng-data"), 0o755)
}

// TestSynthesizeKokoroArgvAssembly 校验命令拼装:模型五件套 + sid + 产物路径 + 文本位置参数,
// 与真机实测参数面一致(v1.13.8 无 dict-dir,kokoro-dict-dir 标注 Not used)。
func TestSynthesizeKokoroArgvAssembly(t *testing.T) {
	dir := t.TempDir()
	bin := writeArgvRecordingKokoro(t, dir)
	seedKokoroModelFiles(t, dir)
	out := filepath.Join(dir, "out.wav")

	argvOut := filepath.Join(dir, "argv.txt")
	t.Setenv("KOKORO_ARGV_OUT", argvOut)
	if err := SynthesizeKokoro(context.Background(), bin, dir, out, "你好世界", 3); err != nil {
		t.Fatal(err)
	}
	join := func(names ...string) string {
		paths := make([]string, len(names))
		for i, n := range names {
			paths[i] = filepath.Join(dir, n)
		}
		return strings.Join(paths, ",")
	}
	want := []string{
		"--kokoro-model=" + filepath.Join(dir, "model.onnx"),
		"--kokoro-voices=" + filepath.Join(dir, "voices.bin"),
		"--kokoro-tokens=" + filepath.Join(dir, "tokens.txt"),
		"--kokoro-data-dir=" + filepath.Join(dir, "espeak-ng-data"),
		"--kokoro-lexicon=" + join("lexicon-us-en.txt", "lexicon-zh.txt"),
		"--tts-rule-fsts=" + join("date-zh.fst", "phone-zh.fst", "number-zh.fst"),
		"--sid=3",
		"--num-threads=2",
		"--output-filename=" + out,
		"你好世界",
	}
	if got := readRecordedArgv(t, argvOut); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv 不符:\n got:  %q\n want: %q", got, want)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("mock 已落产物,Stat 不应失败: %v", err)
	}
}

func TestSynthesizeKokoroFailsOnMissingModelFiles(t *testing.T) {
	dir := t.TempDir()
	bin := writeArgvRecordingKokoro(t, dir)
	if err := SynthesizeKokoro(context.Background(), bin, dir, filepath.Join(dir, "o.wav"), "x", 0); err == nil {
		t.Fatal("模型文件缺失应报错(启动前校验)")
	}
}

// TestSynthesizeKokoroFailsWhenNoProduct 子进程退出 0 但产物未落地(磁盘满等)视为失败。
func TestSynthesizeKokoroFailsWhenNoProduct(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock 依赖 shebang,windows 冒烟覆盖")
	}
	bin := filepath.Join(dir, "fake-kokoro-tts")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	seedKokoroModelFiles(t, dir)
	if err := SynthesizeKokoro(context.Background(), bin, dir, filepath.Join(dir, "o.wav"), "x", 0); err == nil ||
		!strings.Contains(err.Error(), "合成产物缺失") {
		t.Fatalf("产物未落地应报错: %v", err)
	}
}

func TestSynthesizeKokoroFailsOnStderr(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock 依赖 shebang,windows 冒烟覆盖")
	}
	bin := filepath.Join(dir, "fake-kokoro-tts")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	seedKokoroModelFiles(t, dir)
	if err := SynthesizeKokoro(context.Background(), bin, dir, filepath.Join(dir, "o.wav"), "x", 0); err == nil ||
		!strings.Contains(err.Error(), "boom") {
		t.Fatalf("失败应取 stderr 末行: %v", err)
	}
}

// TestKokoroTTS happy path:播种 sherpa 引擎(含 TTS CLI)与 kokoro 模型目录 →
// mock 定位二进制、拼参数、产物落 <dataDir>/tts/。
func TestKokoroTTS(t *testing.T) {
	dataDir := t.TempDir()
	models := localmodel.NewManager(dataDir)
	// 播种引擎:engines/sherpa-onnx/pkg/<ver>/bin/sherpa-onnx-offline-tts + manifest
	pkgBin := filepath.Join(dataDir, "engines", "sherpa-onnx", "pkg", "sherpa-onnx-v1.13.8", "bin", "sherpa-onnx-offline-tts")
	if err := os.MkdirAll(filepath.Dir(pkgBin), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := writeArgvRecordingKokoro(t, filepath.Dir(pkgBin))
	if err := os.Rename(bin, pkgBin); err != nil {
		t.Fatal(err)
	}
	mf := `{"id":"sherpa-onnx","revision":"master","binary":"pkg/sherpa-onnx-v1.13.8/bin/sherpa-onnx-offline","completed_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dataDir, "engines", "sherpa-onnx", "manifest.json"), []byte(mf), 0o644); err != nil {
		t.Fatal(err)
	}
	if !models.Installed("sherpa-onnx") {
		t.Fatal("播种引擎后 Installed 仍为 false")
	}
	// 播种 kokoro 模型目录 + manifest
	modelDir := filepath.Join(dataDir, "models", "kokoro-v1.0")
	seedKokoroModelFiles(t, modelDir)
	mmf := `{"id":"kokoro-v1.0","revision":"master","completed_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(mmf), 0o644); err != nil {
		t.Fatal(err)
	}

	argvOut := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("KOKORO_ARGV_OUT", argvOut)
	k := NewKokoroTTS(dataDir, models)
	out, err := k.Synthesize(context.Background(), SynthRequest{
		ModelID: "kokoro-v1.0", Text: "你好", SpeakerSID: 58,
	}, func(p int, note string) {})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != filepath.Join(dataDir, "tts") {
		t.Fatalf("产物应落 tts 目录: %q", out)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("产物应存在: %v", err)
	}
	// argv 断言 sid 与文本透传;完整参数面由 TestSynthesizeKokoroArgvAssembly 落实
	for _, want := range []string{"--sid=58", "你好"} {
		found := false
		for _, a := range readRecordedArgv(t, argvOut) {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("argv 缺少 %q: %v", want, readRecordedArgv(t, argvOut))
		}
	}
	// EngineBinaryNamed 应按名定位 TTS CLI 而非 manifest 记录的 server 二进制
	got, err := models.EngineBinaryNamed("sherpa-onnx", kokoroTTSBinary)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "sherpa-onnx-offline-tts" {
		t.Fatalf("EngineBinaryNamed 应返回 TTS CLI,实际 %q", got)
	}
}
