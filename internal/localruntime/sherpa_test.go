package localruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// fakeSherpa 生成一个可执行 mock(打印一行固定 JSON 到 stdout),用于命令拼装与解析测试。
// Windows 无 shebang 语义,mock 用例在 windows 跳过(执行器本体由冒烟覆盖)。
func writeFakeSherpa(t *testing.T, dir, stdout string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock 依赖 shebang,windows 冒烟覆盖")
	}
	bin := filepath.Join(dir, "fake-sherpa")
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// writeArgvRecordingSherpa 生成把收到的 argv 逐个写进 $SHERPA_ARGV_OUT 指定文件的 mock
// (每行一个参数),同时向 stdout 打一行合法结果 JSON 供 Transcribe 解析。
// argv 经临时文件而非 stdout 断言:stdout 必须保持「单行结果 JSON」的解析语义不被侵入。
func writeArgvRecordingSherpa(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock 依赖 shebang,windows 冒烟覆盖")
	}
	bin := filepath.Join(dir, "fake-sherpa")
	script := "#!/bin/sh\n" +
		"[ -n \"$SHERPA_ARGV_OUT\" ] && printf '%s\\n' \"$@\" > \"$SHERPA_ARGV_OUT\"\n" +
		"echo '{\"text\": \"argv-ok\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// readRecordedArgv 读回 mock 记录的 argv(每行一个参数;路径含空格也安全)。
func readRecordedArgv(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// TestTranscribeArgvAssembly 校验命令拼装:模型/tokens/itn/language 传参规则与 wav 位置参数。
// zh 显式传 --sense-voice-language=zh;auto 不传 language;一个用例内断言全部。
func TestTranscribeArgvAssembly(t *testing.T) {
	dir := t.TempDir()
	bin := writeArgvRecordingSherpa(t, dir)
	// 启动前校验要求模型文件与 wav 在位——补齐空文件
	for _, name := range []string{"model.int8.onnx", "tokens.txt", "in.wav"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	wav := filepath.Join(dir, "in.wav")

	argvOut := filepath.Join(dir, "argv-zh.txt")
	t.Setenv("SHERPA_ARGV_OUT", argvOut)
	res, err := Transcribe(context.Background(), bin, dir, wav, "zh", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "argv-ok" {
		t.Fatalf("mock 结果解析不符: %+v", res)
	}
	wantZh := []string{
		"--sense-voice-model=" + filepath.Join(dir, "model.int8.onnx"),
		"--tokens=" + filepath.Join(dir, "tokens.txt"),
		"--sense-voice-use-itn=true",
		"--sense-voice-language=zh",
		wav,
	}
	if got := readRecordedArgv(t, argvOut); !reflect.DeepEqual(got, wantZh) {
		t.Fatalf("argv(zh) 不符:\n got:  %q\n want: %q", got, wantZh)
	}

	// auto:不传 language,其余不变
	argvAutoOut := filepath.Join(dir, "argv-auto.txt")
	t.Setenv("SHERPA_ARGV_OUT", argvAutoOut)
	if _, err := Transcribe(context.Background(), bin, dir, wav, "auto", true); err != nil {
		t.Fatal(err)
	}
	wantAuto := []string{
		"--sense-voice-model=" + filepath.Join(dir, "model.int8.onnx"),
		"--tokens=" + filepath.Join(dir, "tokens.txt"),
		"--sense-voice-use-itn=true",
		wav,
	}
	if got := readRecordedArgv(t, argvAutoOut); !reflect.DeepEqual(got, wantAuto) {
		t.Fatalf("argv(auto) 不符:\n got:  %q\n want: %q", got, wantAuto)
	}
}

func TestTranscribeParsesStdoutJSON(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeSherpa(t, dir, `{"lang": "<|zh|>", "emotion": "<|NEUTRAL|>", "event": "<|Speech|>", "text": "你好世界", "timestamps": [0.1, 0.2]}`)
	// 启动前校验要求模型文件与 wav 在位——补齐空文件再测命令拼装与解析
	for _, name := range []string{"model.int8.onnx", "tokens.txt", "in.wav"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	res, err := Transcribe(context.Background(), bin, dir, filepath.Join(dir, "in.wav"), "zh", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "你好世界" || res.Lang != "<|zh|>" || len(res.Timestamps) != 2 {
		t.Fatalf("解析结果不符: %+v", res)
	}
}

func TestTranscribeFailsOnMissingModelFiles(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeSherpa(t, dir, "{}")
	if _, err := Transcribe(context.Background(), bin, dir, "in.wav", "auto", true); err == nil {
		t.Fatal("模型文件缺失应报错(启动前校验)")
	}
}

func TestTranscribeRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeSherpa(t, dir, "not-json at all")
	// mock 缺输入文件会先报错(启动前校验)——补齐模型文件与 wav 再测解析失败
	_ = os.WriteFile(filepath.Join(dir, "model.int8.onnx"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "in.wav"), []byte("x"), 0o644)
	if _, err := Transcribe(context.Background(), bin, dir, filepath.Join(dir, "in.wav"), "auto", true); err == nil {
		t.Fatal("非 JSON stdout 应报错")
	}
}
