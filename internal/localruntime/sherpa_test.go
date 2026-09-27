package localruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
	// mock 缺模型文件也会先报错——补两个空模型文件再测解析失败
	_ = os.WriteFile(filepath.Join(dir, "model.int8.onnx"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte("x"), 0o644)
	if _, err := Transcribe(context.Background(), bin, dir, filepath.Join(dir, "in.wav"), "auto", true); err == nil {
		t.Fatal("非 JSON stdout 应报错")
	}
}
