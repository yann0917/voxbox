package localmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testEntries 三条目测试目录:小文件规模,与 catalog.json 无关(注入构造)。
func testEntries() []Entry {
	e := func(id, kind string, files ...string) Entry {
		return Entry{
			ID: id, Repo: "org/" + id, Revision: "master", Name: id, Kind: kind,
			Summary: "测试模型 " + id, SizeBytes: int64(len(files)) * 100,
			Files:        files,
			Requirements: Requirements{Device: "cpu"},
			License:      "Apache-2.0", LicenseURL: "https://modelscope.cn/models/org/" + id,
		}
	}
	return []Entry{
		e("asr-small", "asr", "a.bin", "b.bin"),
		e("tts-small", "tts", "m/model.bin"),
	}
}

// newTestManager 起一个管理器(entries 注入,不走内嵌 catalog)。
// 可变参 seed 在构造前对模型根目录落地初始盘面——扫盘恢复的前提是「先有盘,再启动」,
// 构造完成后 restore 不再重扫(服务启动期调用一次的契约)。
func newTestManager(t *testing.T, entries []Entry, seed ...func(base string)) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "models")
	for _, s := range seed {
		s(base)
	}
	return newManager(base, "http://fake.modelscope.test", entries), base
}

// waitFor 轮询直到模型进入期望状态(下载是后台 goroutine,测试靠轮询收敛)。
func waitFor(t *testing.T, m *Manager, id string, want Status) ModelView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := m.View(id); ok && v.Status == want {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, _ := m.View(id)
	t.Fatalf("等待 %s 进入 %s 超时,当前: %+v", id, want, v)
	return ModelView{}
}

func viewOrFatal(t *testing.T, m *Manager, id string) ModelView {
	t.Helper()
	v, ok := m.View(id)
	if !ok {
		t.Fatalf("未知模型: %s", id)
	}
	return v
}

func TestRestoreAllIdle(t *testing.T) {
	m, _ := newTestManager(t, testEntries())
	for _, e := range testEntries() {
		v := viewOrFatal(t, m, e.ID)
		if v.Status != StatusIdle || v.HasPartial {
			t.Fatalf("%s 应为未下载,实际 %+v", e.ID, v)
		}
		if v.TotalBytes != e.SizeBytes {
			t.Fatalf("未下载态 TotalBytes 应为目录声明展示值 %d,实际 %d", e.SizeBytes, v.TotalBytes)
		}
	}
	if _, ok := m.View("nope"); ok {
		t.Fatal("未知 id 不应返回视图")
	}
}

func TestRestoreInstalled(t *testing.T) {
	m, _ := newTestManager(t, testEntries(), func(base string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mf := manifest{ID: "asr-small", Repo: "org/asr-small", Revision: "master",
			Files:       []manifestFile{{Path: "a.bin", Size: 100}, {Path: "b.bin", Size: 200}},
			CompletedAt: time.Now().UTC().Format(time.RFC3339)}
		raw, _ := json.Marshal(mf)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := waitFor(t, m, "asr-small", StatusInstalled)
	if v.DownloadedBytes != 300 || v.TotalBytes != 300 {
		t.Fatalf("installed 字节数应为 300/300,实际 %d/%d", v.DownloadedBytes, v.TotalBytes)
	}
}

func TestRestorePartial(t *testing.T) {
	m, _ := newTestManager(t, testEntries(), func(base string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// 半程状态:完整落地一个文件 + 另一个文件的 .part
		if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "b.bin.part"), make([]byte, 40), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle || !v.HasPartial {
		t.Fatalf("应为 idle+可续传,实际 %+v", v)
	}
	if v.DownloadedBytes != 140 {
		t.Fatalf("已收字节应为 140,实际 %d", v.DownloadedBytes)
	}
}

func TestRestoreCorruptManifest(t *testing.T) {
	m, _ := newTestManager(t, testEntries(), func(base string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle || !v.HasPartial {
		t.Fatalf("manifest 损坏应按未安装+可续传处理,实际 %+v", v)
	}
}

func TestRestoreManifestIDMismatch(t *testing.T) {
	m, _ := newTestManager(t, testEntries(), func(base string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mf := manifest{ID: "other", Repo: "org/other", Revision: "master", CompletedAt: "2026-01-01T00:00:00Z"}
		raw, _ := json.Marshal(mf)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle {
		t.Fatalf("manifest id 不匹配应按未安装处理,实际 %+v", v)
	}
}
