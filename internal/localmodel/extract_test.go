package localmodel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTar 构造内存 tar(可再包 gzip/bzip2),entries 键为路径值为内容。
func makeTar(t *testing.T, entries map[string][]byte, wrap func(*bytes.Buffer) []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return wrap(&raw)
}

// gzBytes 真实 gzip 包装(穿越用例与归档管线测试共用:裸 tar 传 "tar.gz" 会先死在
// gzip 头解析上,测不到解包逻辑本身)。
func gzBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	if _, err := gw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractTarGzWhitelist(t *testing.T) {
	payload := makeTar(t, map[string][]byte{
		"top/model.int8.onnx": []byte("onnx-data"),
		"top/tokens.txt":      []byte("tokens"),
		"top/junk.bin":        []byte("junk"),
	}, func(b *bytes.Buffer) []byte {
		return gzBytes(t, b.Bytes())
	})
	dir := t.TempDir()
	src := writeTemp(t, dir, "a.tar.gz", payload)
	dest := t.TempDir()
	if err := extractArchive("tar.gz", src, dest, []string{"model.int8.onnx", "tokens.txt"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"model.int8.onnx": "onnx-data", "tokens.txt": "tokens"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s 解包不符: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "junk.bin")); !os.IsNotExist(err) {
		t.Error("白名单外文件不应被解出")
	}
}

// tarBz2Blob 预构建的 tar.bz2 样本(tar --format=ustar | bzip2,含 bin/sherpa-onnx-offline
// 内容 "elf"):标准库 compress/bzip2 只有解压器没有压缩器,测试样本用系统工具离线生成后内嵌。
var tarBz2Blob = []byte{
	0x42, 0x5a, 0x68, 0x32, 0x31, 0x41, 0x59, 0x26, 0x53, 0x59, 0x08, 0xac, 0xbb, 0x8b, 0x00, 0x00,
	0xac, 0xff, 0x90, 0xd1, 0x80, 0x00, 0xc2, 0x40, 0x02, 0xff, 0x80, 0x00, 0x20, 0x00, 0x04, 0x73,
	0x65, 0xde, 0x60, 0x04, 0x00, 0x00, 0x08, 0x30, 0x00, 0xb9, 0xb6, 0x12, 0xa8, 0x19, 0x0d, 0x00,
	0x00, 0x01, 0xa3, 0xd4, 0xd0, 0x69, 0x32, 0x46, 0x9e, 0xa0, 0x00, 0x06, 0x80, 0x00, 0x2a, 0x92,
	0x06, 0xa6, 0x81, 0xa0, 0x68, 0x32, 0x0d, 0x00, 0xe6, 0xec, 0xfd, 0x86, 0xad, 0xf7, 0x6b, 0x57,
	0x10, 0x27, 0x9a, 0x20, 0x45, 0xfb, 0x6b, 0x19, 0x26, 0xad, 0x08, 0x49, 0x12, 0x86, 0x95, 0xa9,
	0x2f, 0x8a, 0x29, 0xa9, 0x75, 0xf4, 0xfc, 0xc7, 0x26, 0x89, 0x8b, 0x52, 0x0b, 0x0e, 0x09, 0x30,
	0xc6, 0x85, 0x44, 0x96, 0x28, 0x46, 0x76, 0x3c, 0x20, 0x3a, 0x60, 0x13, 0x91, 0x8c, 0x2b, 0x78,
	0x98, 0x95, 0x02, 0x98, 0x73, 0x83, 0x1a, 0x70, 0x84, 0x0a, 0xd0, 0x8d, 0x60, 0x42, 0x50, 0x39,
	0xf4, 0x20, 0x70, 0xeb, 0xcb, 0xe0, 0xf3, 0x51, 0x14, 0x75, 0x0f, 0xc8, 0x29, 0xb1, 0x7c, 0xf2,
	0xd8, 0xb1, 0x06, 0xb0, 0x93, 0xd4, 0x1c, 0x50, 0x0e, 0x19, 0x89, 0x89, 0x10, 0x7f, 0x17, 0x72,
	0x45, 0x38, 0x50, 0x90, 0x08, 0xac, 0xbb, 0x8b,
}

func TestExtractTarBz2AndZip(t *testing.T) {
	// tar.bz2(内嵌真实样本:bin/sherpa-onnx-offline = "elf")
	dir := t.TempDir()
	dest := t.TempDir()
	if err := extractArchive("tar.bz2", writeTemp(t, dir, "a.tar.bz2", tarBz2Blob), dest, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "bin", "sherpa-onnx-offline"))
	if err != nil || string(got) != "elf" {
		t.Fatalf("tar.bz2 解包不符: %v %q", err, got)
	}
	// zip
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("audiocpp_server")
	w.Write([]byte("macho"))
	zw.Close()
	dest2 := t.TempDir()
	if err := extractArchive("zip", writeTemp(t, dir, "a.zip", zbuf.Bytes()), dest2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest2, "audiocpp_server")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	// ../ 成员必须被拒:按真实 gzip 包装构造(骨架里的恒等包装是裸 tar,"tar.gz" 会先
	// 死在 gzip 头上),断言错误信息指明「非法归档条目路径」。
	payload := makeTar(t, map[string][]byte{"../evil.txt": []byte("x")}, func(b *bytes.Buffer) []byte {
		return gzBytes(t, b.Bytes())
	})
	dir := t.TempDir()
	dest := t.TempDir()
	err := extractArchive("tar.gz", writeTemp(t, dir, "a.tar.gz", payload), dest, nil)
	if err == nil {
		t.Fatal("上级穿越必须被拒绝")
	}
	if !strings.Contains(err.Error(), "非法归档条目路径") {
		t.Fatalf("错误应指明路径非法: %v", err)
	}
	// 整体失败:目标目录不得留下任何越界文件
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatalf("拒绝穿越后 dest 应为空,实际: %v", entries)
	}
}

// TestFindBinariesSubdirAndExe 落实裁定 6:引擎解包后二进制可能在子目录
// (sherpa 包 bin/ 布局),Windows 还有 .exe 变体——walk + .exe 命中锁死在测试里。
func TestFindBinariesSubdirAndExe(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "sherpa-onnx-offline"), []byte("elf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audiocpp_server.exe"), []byte("pe"), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, err := findBinaries(dir, []string{"sherpa-onnx-offline", "audiocpp_server"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("应命中 2 个二进制,实际: %v", hits)
	}
	if hits[0] != filepath.Join(dir, "bin", "sherpa-onnx-offline") {
		t.Fatalf("子目录中的二进制应被 walk 命中: %v", hits)
	}
	if hits[1] != filepath.Join(dir, "audiocpp_server.exe") {
		t.Fatalf("无后缀名应命中 .exe 变体: %v", hits)
	}
	for _, h := range hits {
		if fi, err := os.Stat(h); err != nil || fi.Mode().Perm() != 0o755 {
			t.Fatalf("命中二进制应 chmod 0755: %s %v %v", h, fi, err)
		}
	}
}

func TestFindBinariesMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findBinaries(dir, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "引擎包不完整") {
		t.Fatalf("缺失二进制应报引擎包不完整: %v", err)
	}
}
