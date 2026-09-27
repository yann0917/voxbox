package localmodel

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testEntries 两条目测试目录:小文件规模,与 catalog.json 无关(注入构造)。
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

// newTestManager 起一个管理器(entries 注入,不走内嵌 catalog):
// 模型根目录与引擎根目录分离,返回 (m, modelsDir, enginesDir) 两个目录(控制器裁定)。
// 可变参 seed 在构造前对两个根目录落地初始盘面——扫盘恢复的前提是「先有盘,再启动」,
// 构造完成后 restore 不再重扫(服务启动期调用一次的契约)。
// 引擎相关测试用 enginesDir 播种(引擎安装目录在 engines/<id>)。
func newTestManager(t *testing.T, entries []Entry, seed ...func(modelsDir, enginesDir string)) (*Manager, string, string) {
	t.Helper()
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	enginesDir := filepath.Join(dir, "engines")
	for _, s := range seed {
		s(modelsDir, enginesDir)
	}
	return newManager(modelsDir, enginesDir, "http://fake.modelscope.test", entries), modelsDir, enginesDir
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
	m, _, _ := newTestManager(t, testEntries())
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
	m, _, _ := newTestManager(t, testEntries(), func(base, _ string) {
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
	m, _, _ := newTestManager(t, testEntries(), func(base, _ string) {
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
	m, _, _ := newTestManager(t, testEntries(), func(base, _ string) {
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
	m, _, _ := newTestManager(t, testEntries(), func(base, _ string) {
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

func TestRestoreManifestRevisionMismatch(t *testing.T) {
	// manifest 的 revision 与目录条目不一致(目录换版)→ 按未安装处理,不能静默当作已安装。
	m, _, _ := newTestManager(t, testEntries(), func(base, _ string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mf := manifest{ID: "asr-small", Repo: "org/asr-small", Revision: "old-rev", CompletedAt: "2026-01-01T00:00:00Z"}
		raw, _ := json.Marshal(mf)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle {
		t.Fatalf("manifest revision 不匹配应按未安装处理,实际 %+v", v)
	}
}

// ─── 下载管线测试:fakeScope 假魔搭 + 全链路用例 ─────────────────────────────

// fileMap 可变参构造文件表(pairs 依次为 name, content;值可为 string 或 []byte)。
func fileMap(pairs ...any) map[string][]byte {
	m := map[string][]byte{}
	for i := 0; i+1 < len(pairs); i += 2 {
		switch v := pairs[i+1].(type) {
		case string:
			m[pairs[i].(string)] = []byte(v)
		case []byte:
			m[pairs[i].(string)] = v
		default:
			panic(fmt.Sprintf("fileMap 值必须是 string 或 []byte: %T", pairs[i+1]))
		}
	}
	return m
}

// fakeScope 可编程假魔搭:固定文件表,支持 Range(可关),按文件阻塞以稳定测试暂停/单飞行。
type fakeScope struct {
	t        *testing.T
	files    map[string][]byte
	noRange  bool                     // 恒 200(模拟不支持 Range 的上游)
	quirk200 bool                     // 模拟魔搭非 LFS 怪癖:bytes=0-0 回 200 + 真实大小 Content-Range + 1 字节 body
	badStart bool                     // 模拟损坏代理:start>0 的 Range 回起点错误的 206(声称从 0 开始,body 恰为剩余长度)
	blockMu  sync.Mutex               // 保护 block:handler goroutine 写入与测试轮询读并发
	block    map[string]chan struct{} // 文件 → 关闭后才继续写剩余字节
	blockN   map[string]int64         // 文件 → 先写多少字节后阻塞(仅无 Range 的 200 分支)
	hits     atomic.Int64             // 带 offset>0 的 Range 续传请求数
	srv      *httptest.Server
}

func newFakeScope(t *testing.T, files map[string][]byte) *fakeScope {
	f := &fakeScope{t: t, files: files, block: map[string]chan struct{}{}, blockN: map[string]int64{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// FilePath 取查询串;repo 段不校验(测试条目共用一个 fake 空间)
		file := r.URL.Query().Get("FilePath")
		data, ok := f.files[file]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Range") == "" || f.noRange {
			// 全量分支:阻塞点放这里——fetchOne 全新下载发的是无 Range 请求(offset=0),
			// probe 才带 Range: bytes=0-0(206 分支),阻塞放 206 会先撞上 probe 死锁。
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			w.WriteHeader(http.StatusOK)
			if n := f.blockN[file]; n > 0 {
				ch := make(chan struct{})
				f.blockMu.Lock()
				f.block[file] = ch
				f.blockMu.Unlock()
				_, _ = w.Write(data[:n])
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				<-ch // 测试关闭后才放行剩余字节
				_, _ = w.Write(data[n:])
				return
			}
			_, _ = w.Write(data)
			return
		}
		rng := r.Header.Get("Range") // bytes=S-
		var start int64
		if _, err := fmt.Sscanf(rng, "bytes=%d-", &start); err != nil || start < 0 || start > int64(len(data)) {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if f.quirk200 && start == 0 {
			// 魔搭怪癖:探测请求回 200(非 206)+ 真实大小的 Content-Range + Content-Length: 1
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(data)))
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data[:1])
			return
		}
		if f.badStart && start > 0 {
			// 损坏代理:回起点错误的 206(声称 bytes 0-…、总长不变),body 恰为剩余
			// 长度——字节数校验(offset+=downloaded)挡不住错位内容,必须核对起始偏移。
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", int64(len(data))-start-1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[:int64(len(data))-start])
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, int64(len(data))-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		if start > 0 {
			f.hits.Add(1)
		}
		_, _ = w.Write(data[start:]) // 206 分支不设阻塞:probe(bytes=0-0)会先撞上
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// releaseAfter 等待指定文件的阻塞点出现,返回放行开关(关闭它服务端继续写)。
// 返回的开关幂等:显式放行与 defer 兜底可并存,失败路径不会悬死 httptest.Server.Close。
func (f *fakeScope) releaseAfter(t *testing.T, file string) func() {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.blockMu.Lock()
		ch, ok := f.block[file]
		f.blockMu.Unlock()
		if ok {
			return func() {
				select {
				case <-ch: // 已放行
				default:
					close(ch)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待文件 %s 阻塞点超时", file)
	return nil
}

// waitForPart 轮询直到 path 达到期望大小:服务端 flush ≠ 客户端已落盘,
// 直接 Stop 有取消竞态,先确认盘面再动手。
func waitForPart(t *testing.T, path string, size int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Size() == size {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 %s 达 %d 字节超时", path, size)
}

func TestDownloadHappyPath(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 256)
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("hello world")))
	entries := testEntries()
	m, base, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	if err := m.Start("asr-small"); err != nil {
		t.Fatalf("启动下载失败: %v", err)
	}
	v := waitFor(t, m, "asr-small", StatusInstalled)
	if v.DownloadedBytes != int64(len(content))+11 || v.HasPartial {
		t.Fatalf("installed 进度应为 %d,实际 %+v", len(content)+11, v)
	}
	// 文件落地正确
	for p, want := range map[string]string{"a.bin": string(content), "b.bin": "hello world"} {
		got, err := os.ReadFile(filepath.Join(base, "asr-small", p))
		if err != nil || string(got) != want {
			t.Fatalf("文件 %s 内容不符: %v", p, err)
		}
	}
	// manifest 存在且记录 revision
	mf, err := readManifest(filepath.Join(base, "asr-small"))
	if err != nil || mf.Revision != "master" || mf.ID != "asr-small" {
		t.Fatalf("manifest 不符: %+v err=%v", mf, err)
	}
}

func TestDownloadNestedPath(t *testing.T) {
	// 目录条目允许子目录文件(tts-small: m/model.bin):落盘前 MkdirAll 补父目录,
	// 文件必须落在 models/tts-small/m/model.bin 子目录且安装成功。
	content := []byte("nested-model-bytes")
	f := newFakeScope(t, fileMap("m/model.bin", content))
	entries := testEntries()
	m, base, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	if err := m.Start("tts-small"); err != nil {
		t.Fatalf("启动下载失败: %v", err)
	}
	v := waitFor(t, m, "tts-small", StatusInstalled)
	if v.DownloadedBytes != int64(len(content)) || v.HasPartial {
		t.Fatalf("installed 进度应为 %d,实际 %+v", len(content), v)
	}
	got, err := os.ReadFile(filepath.Join(base, "tts-small", "m", "model.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("子目录文件内容不符: %v", err)
	}
	mf, err := readManifest(filepath.Join(base, "tts-small"))
	if err != nil || len(mf.Files) != 1 || mf.Files[0].Path != "m/model.bin" {
		t.Fatalf("manifest 应记录子目录路径,实际: %+v err=%v", mf, err)
	}
}

func TestDownloadStopAndResume(t *testing.T) {
	content := bytes.Repeat([]byte("y"), 64*1024) // 64KB:阻塞点前只写 1024
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("ok")))
	f.blockN["a.bin"] = 1024 // 全量分支写到 1024 后阻塞
	entries := testEntries()
	m, base, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL

	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	release := f.releaseAfter(t, "a.bin") // 等阻塞点(已写 1024 字节)
	defer release()                       // 失败路径兜底(幂等)
	part := filepath.Join(base, "asr-small", "a.bin.part")
	waitForPart(t, part, 1024) // 先确认客户端已落盘,再 Stop(避免取消竞态)
	if err := m.Stop("asr-small"); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	release() // 放行服务端(客户端已取消,写不写都行)
	v := waitFor(t, m, "asr-small", StatusIdle)
	if !v.HasPartial {
		t.Fatalf("暂停后应可续传: %+v", v)
	}
	if fi, err := os.Stat(part); err != nil || fi.Size() != 1024 {
		t.Fatalf(".part 应保留 1024 字节,实际 %v", err)
	}

	// 续传:必须从 offset=1024 开始(Range 请求)
	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "asr-small", StatusInstalled)
	if f.hits.Load() < 1 {
		t.Fatal("续传应发出 offset>0 的 Range 请求")
	}
	got, err := os.ReadFile(filepath.Join(base, "asr-small", "a.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("续传后内容不符: %v", err)
	}
}

func TestProbe200WithContentRange(t *testing.T) {
	// 魔搭非 LFS 文件怪癖(线上实证):bytes=0-0 探测回 200 + Content-Range: bytes 0-0/N
	// (N 为真实大小)+ Content-Length: 1。probe 采信 Content-Length 会把小文件记成 1 字节:
	// total_bytes 被污染、rangeOK 全局为假 → 续传回零重下,且第二个文件起必然
	// 「下载不完整:已收 N 字节,预期 1」→ 永远装不上。修复后 200+Content-Range 应取 N
	// 并视为支持 Range:盘面预置的 .part 必须走 Range 续传(不回零),total 为真实大小和。
	// (盘面预置而非跑一轮再暂停:旧实现下回零重下会重新进入阻塞分支,测试收尾会悬死。)
	content := bytes.Repeat([]byte("m"), 693) // 复现线上 configuration.json 693 字节的量级
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("ok")))
	f.quirk200 = true
	entries := testEntries()
	m, base, _ := newTestManager(t, entries, func(base, _ string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// 半程盘面:a.bin 已收前 256 字节(.part)
		if err := os.WriteFile(filepath.Join(dir, "a.bin.part"), content[:256], 0o644); err != nil {
			t.Fatal(err)
		}
	})
	m.baseURL = f.srv.URL

	v := viewOrFatal(t, m, "asr-small")
	if !v.HasPartial || v.DownloadedBytes != 256 {
		t.Fatalf("预置盘面应识别为可续传 256 字节,实际 %+v", v)
	}
	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	v = waitFor(t, m, "asr-small", StatusInstalled)
	if v.TotalBytes != int64(len(content))+2 {
		t.Fatalf("total_bytes 应为真实大小和 %d(而非被怪癖污染的 1+1),实际 %d", len(content)+2, v.TotalBytes)
	}
	if v.DownloadedBytes != v.TotalBytes {
		t.Fatalf("installed 进度应为 %d,实际 %+v", v.TotalBytes, v)
	}
	if f.hits.Load() < 1 {
		t.Fatal("续传应发出 offset>0 的 Range 请求(.part 未被废弃)")
	}
	got, err := os.ReadFile(filepath.Join(base, "asr-small", "a.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("续传后内容不符: %v", err)
	}
}

func TestResumeBadStart206(t *testing.T) {
	// 损坏代理回「起点错误的 206」:声称 bytes 0-…(起始偏移与请求 offset 不符),
	// body 恰为剩余长度——仅靠事后字节数校验会把错位内容装进库(目录 sha256 全空,
	// 无第二道校验)。必须核对 Content-Range 起始偏移,不符则降级从零重下。
	// 内容前半/后半必须可区分:旧实现会把前半再拼一遍,前后半同内容时检出无从谈起。
	content := append(bytes.Repeat([]byte("A"), 256), bytes.Repeat([]byte("B"), 256)...)
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("ok")))
	f.badStart = true
	entries := testEntries()
	m, base, _ := newTestManager(t, entries, func(base, _ string) {
		dir := filepath.Join(base, "asr-small")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// 半程盘面:a.bin 已收前 256 字节(.part),启动后走 Range 续传
		if err := os.WriteFile(filepath.Join(dir, "a.bin.part"), content[:256], 0o644); err != nil {
			t.Fatal(err)
		}
	})
	m.baseURL = f.srv.URL

	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "asr-small", StatusInstalled)
	got, err := os.ReadFile(filepath.Join(base, "asr-small", "a.bin"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("起点错误的 206 应降级从零重下,实际内容不符: %v", err)
	}
}

func TestDownloadRangeUnsupported(t *testing.T) {
	content := []byte("no-range-content")
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("ok")))
	f.noRange = true
	entries := testEntries()
	m, base, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	if err := m.Start("asr-small"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "asr-small", StatusInstalled)
	got, _ := os.ReadFile(filepath.Join(base, "asr-small", "a.bin"))
	if string(got) != string(content) {
		t.Fatal("无 Range 支持也应整文件下完")
	}
}

func TestDownloadMissing404(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", []byte("ok"))) // b.bin 缺失 → 404
	entries := testEntries()
	m, _, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "模型不存在或已下架") {
		t.Fatalf("404 错误文案应直述,实际: %s", v.Error)
	}
}

func TestDownloadTruncatedBody(t *testing.T) {
	// 服务端声称 256 字节只写 100 即返回:客户端应检出「下载不完整」。
	// harness 说明:probe(带 Range)按声明给全量,让 probe 拿到 ContentLength=256;
	// 全量请求(无 Range)不声明 Content-Length,写 100 字节后干净收尾(chunked 终止块),
	// 客户端干净 EOF 收满 100 字节 → fetchOne 断言字节数不符 →「下载不完整」。
	// (若按字面声明 CL=256 只写 100,客户端只会得到 unexpected EOF →「传输中断」,
	// 与断言文案物理不相容——实现保持 brief 原样,调整的是这里的服务端。)
	content := bytes.Repeat([]byte("z"), 256)
	entries := testEntries()
	m, _, _ := newTestManager(t, entries)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content) // probe 只取 CL,全量照给
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content[:100]) // 短写即返回,无 CL → chunked 干净收尾
	}))
	defer srv.Close()
	m.baseURL = srv.URL
	_ = m.Start("asr-small")
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "下载不完整") {
		t.Fatalf("应检出截断,实际: %s", v.Error)
	}
}

func TestDownloadDiskPrecheck(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", []byte("ok")))
	entries := testEntries()
	m, _, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	orig := diskFreeBytes
	diskFreeBytes = func(string) (uint64, error) { return 1, nil } // 只剩 1 字节
	t.Cleanup(func() { diskFreeBytes = orig })
	if err := m.Start("asr-small"); err == nil {
		t.Fatal("磁盘不足应拒绝启动")
	}
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "磁盘空间不足") {
		t.Fatalf("错误应直述空间,实际: %s", v.Error)
	}
}

func TestDownloadSHA256Mismatch(t *testing.T) {
	content := []byte("sha-content")
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("ok")))
	entries := testEntries()
	entries[0].SHA256 = map[string]string{"a.bin": strings.Repeat("0", 64)} // 错误哈希
	m, _, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	v := waitFor(t, m, "asr-small", StatusFailed)
	if !strings.Contains(v.Error, "校验失败") {
		t.Fatalf("应报校验失败,实际: %s", v.Error)
	}
}

func TestDownloadSHA256OK(t *testing.T) {
	content := []byte("sha-content")
	f := newFakeScope(t, fileMap("a.bin", content, "b.bin", []byte("ok")))
	entries := testEntries()
	sum := sha256.Sum256(content)
	entries[0].SHA256 = map[string]string{"a.bin": hex.EncodeToString(sum[:])}
	m, _, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	waitFor(t, m, "asr-small", StatusInstalled)
}

func TestSingleFlight(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", bytes.Repeat([]byte("q"), 4096), "b.bin", []byte("ok")))
	f.blockN["a.bin"] = 1024
	entries := testEntries()
	m, _, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	release := f.releaseAfter(t, "a.bin") // 等阻塞点:此刻 A 卡在 a.bin 半程
	defer release()                       // 失败路径兜底(幂等)
	if err := m.Start("tts-small"); err == nil {
		t.Fatal("第二个下载应被拒绝")
	} else if !strings.Contains(err.Error(), "已有模型在下载") {
		t.Fatalf("冲突文案不符: %v", err)
	}
	release() // 放行收尾
	waitFor(t, m, "asr-small", StatusInstalled)
}

func TestStartStopUnknownAndIdle(t *testing.T) {
	m, _, _ := newTestManager(t, testEntries())
	if err := m.Start("nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("未知 id 应 ErrUnknownModel,实际: %v", err)
	}
	if err := m.Stop("asr-small"); err == nil {
		t.Fatal("空闲态 Stop 应报错")
	}
	if err := m.Delete("nope"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("删除未知 id 应 ErrUnknownModel,实际: %v", err)
	}
}

func TestDeleteModel(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", []byte("data"), "b.bin", []byte("ok")))
	entries := testEntries()
	m, base, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	waitFor(t, m, "asr-small", StatusInstalled)
	if err := m.Delete("asr-small"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "asr-small")); !os.IsNotExist(err) {
		t.Fatal("删除后目录应消失")
	}
	v := viewOrFatal(t, m, "asr-small")
	if v.Status != StatusIdle || v.HasPartial || v.DownloadedBytes != 0 {
		t.Fatalf("删除后应回未下载态: %+v", v)
	}
}

func TestDeleteWhileDownloading(t *testing.T) {
	f := newFakeScope(t, fileMap("a.bin", bytes.Repeat([]byte("q"), 4096), "b.bin", []byte("ok")))
	f.blockN["a.bin"] = 1024
	entries := testEntries()
	m, _, _ := newTestManager(t, entries)
	m.baseURL = f.srv.URL
	_ = m.Start("asr-small")
	defer f.releaseAfter(t, "a.bin")() // waitFor 收尾后再放行(Stop 必须赶在完成前)
	if err := m.Delete("asr-small"); err == nil {
		t.Fatal("下载中禁止删除")
	}
	_ = m.Stop("asr-small")
	waitFor(t, m, "asr-small", StatusIdle)
}

// ─── 二期:直链覆盖 / 归档解包 / 引擎安装管线 ─────────────────────────────

// directServer 按 URL 路径精确吐文件(FileURLs 直链与归档资产用,不走魔搭模板):
// 一律 200 + Content-Length 全量回包(probe 的 bytes=0-0 由此拿到真实大小,canRange=false
// 由归档管线忽略;无 .part 时 fetchOne 发的是不带 Range 的全量 GET,路径自洽)。
func directServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// engineTestEntry 构造指向 directServer 的引擎条目:平台资产对准当前 GOOS/GOARCH。
// Revision 与清单缺省一致(master,parseCatalog 会补);archive 字段仅作提示
// (裁定 3:解包格式以资产 URL 扩展名为准)。
func engineTestEntry(url string, size int64, sha string) Entry {
	return Entry{
		ID: "eng-test", Kind: "engine", Name: "测试引擎", Summary: "测试引擎",
		Revision: "master", SizeBytes: size, Requirements: Requirements{Device: "cpu"},
		License: "MIT", LicenseURL: "https://example.com/eng",
		Archive:       "tar.gz",
		ArchiveSHA256: sha,
		Binaries:      []string{"sherpa-onnx-offline"},
		Assets: map[string]Asset{
			runtime.GOOS + "/" + runtime.GOARCH: {URL: url, SizeBytes: size, SHA256: sha},
		},
	}
}

func TestFileURLsDirectLink(t *testing.T) {
	// GGUF 直链条目:Repo 空、FileURLs 给全量直链——fileURLFor 覆盖后直接走
	// 既有逐文件管线(probe/fetchOne/续传),无需归档。
	content := []byte("gguf-bytes")
	srv := directServer(t, map[string][]byte{"/qwen.gguf": content})
	e := Entry{
		ID: "tts-gguf", Kind: "tts", Name: "TTS", Summary: "TTS 直链条目",
		SizeBytes: int64(len(content)), Requirements: Requirements{Device: "metal"},
		License: "Apache-2.0", LicenseURL: "https://example.com/t",
		Files:    []string{"qwen.gguf"},
		FileURLs: map[string]string{"qwen.gguf": srv.URL + "/qwen.gguf"},
	}
	m, modelsDir, _ := newTestManager(t, []Entry{e})
	if err := m.Start("tts-gguf"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "tts-gguf", StatusInstalled)
	got, err := os.ReadFile(filepath.Join(modelsDir, "tts-gguf", "qwen.gguf"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("直链下载内容不符: %v", err)
	}
}

func TestEngineInstall(t *testing.T) {
	// 引擎安装全链路:平台资产直链下载 → sha256 → 解到 engines/<id>/pkg/(裁定 1,
	// rpath 布局不可挪)→ walk 定位 bin/ 子目录二进制 → manifest.Binary。
	payload := makeTar(t, map[string][]byte{
		"bin/sherpa-onnx-offline": []byte("elf"),
		"lib/libonnxruntime.so":   []byte("so"),
		"include/x.h":             []byte("h"),
	}, func(b *bytes.Buffer) []byte { return gzBytes(t, b.Bytes()) })
	srv := directServer(t, map[string][]byte{"/eng-pkg.tar.gz": payload})
	sum := sha256.Sum256(payload)
	entries := []Entry{engineTestEntry(srv.URL+"/eng-pkg.tar.gz", int64(len(payload)), hex.EncodeToString(sum[:]))}
	m, _, enginesDir := newTestManager(t, entries)
	if err := m.Start("eng-test"); err != nil {
		t.Fatalf("启动引擎安装失败: %v", err)
	}
	v := waitFor(t, m, "eng-test", StatusInstalled)
	if v.DownloadedBytes == 0 || v.DownloadedBytes != v.TotalBytes {
		t.Fatalf("installed 进度不符: %+v", v)
	}
	// 引擎解到 engines/<id>/pkg/,包内相对结构保留
	got, err := os.ReadFile(filepath.Join(enginesDir, "eng-test", "pkg", "bin", "sherpa-onnx-offline"))
	if err != nil || string(got) != "elf" {
		t.Fatalf("pkg/bin 二进制不符: %v", err)
	}
	if _, err := os.Stat(filepath.Join(enginesDir, "eng-test", "pkg", "lib", "libonnxruntime.so")); err != nil {
		t.Fatalf("pkg/lib 布局应保留: %v", err)
	}
	// 二进制已 chmod 0755
	fi, err := os.Stat(filepath.Join(enginesDir, "eng-test", "pkg", "bin", "sherpa-onnx-offline"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("二进制应 chmod 0755,实际 %v %v", fi, err)
	}
	// 归档本体安装后删除
	if _, err := os.Stat(filepath.Join(enginesDir, "eng-test", "eng-pkg.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("归档本体应删除")
	}
	// manifest:Binary 为 server 二进制的目录相对路径,Files 记 pkg 相对结构
	mf, err := readManifest(filepath.Join(enginesDir, "eng-test"))
	if err != nil || mf.Binary != "pkg/bin/sherpa-onnx-offline" {
		t.Fatalf("manifest.Binary 不符: %+v err=%v", mf, err)
	}
	if len(mf.Files) != 3 {
		t.Fatalf("manifest.Files 应记 3 个解包产物: %+v", mf.Files)
	}
	for _, f := range mf.Files {
		if !strings.HasPrefix(f.Path, "pkg/") {
			t.Fatalf("引擎 manifest.Files 应带 pkg/ 前缀: %+v", mf.Files)
		}
	}
}

func TestEngineUnsupportedPlatform(t *testing.T) {
	// 平台资产缺当前 GOOS/GOARCH → 前置失败,文案直述平台
	srv := directServer(t, map[string][]byte{"/eng.tar.gz": []byte("x")})
	e := engineTestEntry(srv.URL+"/eng.tar.gz", 1, strings.Repeat("a", 64))
	e.Assets = map[string]Asset{
		"plan9/amd64": {URL: srv.URL + "/eng.tar.gz", SizeBytes: 1, SHA256: strings.Repeat("a", 64)},
	}
	m, _, _ := newTestManager(t, []Entry{e})
	_ = m.Start("eng-test")
	v := waitFor(t, m, "eng-test", StatusFailed)
	if !strings.Contains(v.Error, "暂不提供该引擎") {
		t.Fatalf("应报平台不支持,实际: %s", v.Error)
	}
}

// modelArchiveEntry 构造指向 directServer 的模型归档条目(sensevoice 同构:整包直链 + 白名单)。
func modelArchiveEntry(url string, size int64, sha string) Entry {
	return Entry{
		ID: "sense-test", Kind: "asr", Name: "测试识别", Summary: "模型归档条目",
		SizeBytes: size, Requirements: Requirements{Device: "cpu"},
		License: "Apache-2.0", LicenseURL: "https://example.com/m",
		RequiresEngine: "eng-test",
		Archive:        "tar.gz",
		ArchiveURL:     url,
		ArchiveSize:    size,
		ArchiveSHA256:  sha,
		ExtractFiles:   []string{"model.int8.onnx", "tokens.txt"},
	}
}

func TestModelArchiveInstall(t *testing.T) {
	// 模型归档条目:解包白名单扁平化直接落条目根 models/<id>/(裁定 1),
	// ArchiveSHA256 非空时同样执行 sha256 校验(裁定 4)。
	payload := makeTar(t, map[string][]byte{
		"top/model.int8.onnx": []byte("onnx-data"),
		"top/tokens.txt":      []byte("tokens"),
		"top/junk.bin":        []byte("junk"),
	}, func(b *bytes.Buffer) []byte { return gzBytes(t, b.Bytes()) })
	srv := directServer(t, map[string][]byte{"/model.tar.gz": payload})
	sum := sha256.Sum256(payload)
	entries := []Entry{modelArchiveEntry(srv.URL+"/model.tar.gz", int64(len(payload)), hex.EncodeToString(sum[:]))}
	m, modelsDir, _ := newTestManager(t, entries)
	if err := m.Start("sense-test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, "sense-test", StatusInstalled)
	// 白名单成员落条目根,白名单外不解出
	got, err := os.ReadFile(filepath.Join(modelsDir, "sense-test", "model.int8.onnx"))
	if err != nil || string(got) != "onnx-data" {
		t.Fatalf("模型归档白名单解包不符: %v", err)
	}
	if got, err = os.ReadFile(filepath.Join(modelsDir, "sense-test", "tokens.txt")); err != nil || string(got) != "tokens" {
		t.Fatalf("tokens.txt 解包不符: %v", err)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "sense-test", "junk.bin")); !os.IsNotExist(err) {
		t.Fatal("白名单外文件不应被解出")
	}
	// 归档本体删除 + manifest 记扁平相对路径,Binary 为空
	mf, err := readManifest(filepath.Join(modelsDir, "sense-test"))
	if err != nil || mf.Binary != "" {
		t.Fatalf("模型归档 manifest.Binary 应为空: %+v err=%v", mf, err)
	}
	if len(mf.Files) != 2 || mf.Files[0].Path != "model.int8.onnx" || mf.Files[1].Path != "tokens.txt" {
		t.Fatalf("manifest.Files 应为条目根相对路径: %+v", mf.Files)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "sense-test", "model.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("归档本体应删除")
	}
}

func TestModelArchiveIncompleteWhitelist(t *testing.T) {
	// 白名单成员缺失(归档只含部分成员):下载走完且 sha256 相符,但解包不完整
	// → 终态 failed 且不写 manifest;命中的成员已解出,归档本体保留供重试复用。
	payload := makeTar(t, map[string][]byte{
		"top/model.int8.onnx": []byte("onnx-data"),
		// top/tokens.txt 缺失
	}, func(b *bytes.Buffer) []byte { return gzBytes(t, b.Bytes()) })
	srv := directServer(t, map[string][]byte{"/model.tar.gz": payload})
	sum := sha256.Sum256(payload)
	entries := []Entry{modelArchiveEntry(srv.URL+"/model.tar.gz", int64(len(payload)), hex.EncodeToString(sum[:]))}
	m, modelsDir, _ := newTestManager(t, entries)
	if err := m.Start("sense-test"); err != nil {
		t.Fatal(err)
	}
	v := waitFor(t, m, "sense-test", StatusFailed)
	if !strings.Contains(v.Error, "解包不完整") || !strings.Contains(v.Error, "tokens.txt") {
		t.Fatalf("应报解包不完整并指明缺失成员,实际: %s", v.Error)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "sense-test", "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("解包不完整不应写 manifest")
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "sense-test", "model.int8.onnx")); err != nil {
		t.Fatalf("命中的白名单成员应已解出: %v", err)
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "sense-test", "model.tar.gz")); err != nil {
		t.Fatalf("失败时归档本体应保留供重试复用: %v", err)
	}
}

func TestModelArchiveSHAMismatch(t *testing.T) {
	// 裁定 4:模型归档条目 ArchiveSHA256 非空即校验,不符报失败
	payload := gzBytes(t, func() []byte {
		var raw bytes.Buffer
		tw := tar.NewWriter(&raw)
		tw.WriteHeader(&tar.Header{Name: "model.int8.onnx", Mode: 0o755, Size: 9})
		tw.Write([]byte("onnx-data"))
		tw.Close()
		return raw.Bytes()
	}())
	srv := directServer(t, map[string][]byte{"/model.tar.gz": payload})
	entries := []Entry{modelArchiveEntry(srv.URL+"/model.tar.gz", int64(len(payload)), strings.Repeat("0", 64))}
	m, _, _ := newTestManager(t, entries)
	_ = m.Start("sense-test")
	v := waitFor(t, m, "sense-test", StatusFailed)
	if !strings.Contains(v.Error, "校验失败") {
		t.Fatalf("归档 sha256 不符应报校验失败,实际: %s", v.Error)
	}
}

func TestDirectLinkMissing404(t *testing.T) {
	// 直链条目(Repo 为空)404:文案不再渲染空 repo 段,直述失败的具体文件 URL
	srv := directServer(t, nil) // 空文件表:任何路径都 404
	e := Entry{
		ID: "tts-gguf", Kind: "tts", Name: "TTS", Summary: "TTS 直链条目",
		SizeBytes: 100, Requirements: Requirements{Device: "metal"},
		License: "Apache-2.0", LicenseURL: "https://example.com/t",
		Files:    []string{"qwen.gguf"},
		FileURLs: map[string]string{"qwen.gguf": srv.URL + "/qwen.gguf"},
	}
	m, _, _ := newTestManager(t, []Entry{e})
	_ = m.Start("tts-gguf")
	v := waitFor(t, m, "tts-gguf", StatusFailed)
	if !strings.Contains(v.Error, "模型文件不存在或已下架") || !strings.Contains(v.Error, srv.URL+"/qwen.gguf") {
		t.Fatalf("直链 404 应直述 URL,实际: %s", v.Error)
	}
}

func TestPickAssetURL(t *testing.T) {
	// 引擎:按平台选资产;格式以资产 URL 扩展名为准且忽略查询串——
	// audiocpp 条目级 archive 是 tar.gz 而 windows 资产是 .zip,扩展名必须优先(裁定 3)。
	wantSHA := strings.Repeat("c", 64)
	eng := engineTestEntry("https://example.com/audio-v0.8.2-bin-windows-x64.zip?sig=abc", 25129726, wantSHA)
	url, size, sha, format, err := pickAssetURL(eng)
	if err != nil || url != "https://example.com/audio-v0.8.2-bin-windows-x64.zip?sig=abc" ||
		size != 25129726 || sha != wantSHA {
		t.Fatalf("引擎资产选择不符: %s %d %s %v", url, size, sha, err)
	}
	if format != "zip" {
		t.Fatalf("资产 URL 扩展名应判 zip(忽略查询串),实际 %q", format)
	}
	// 模型归档:条目直属字段,格式即条目级 archive
	mdl := Entry{
		Kind: "asr", Archive: "tar.bz2",
		ArchiveURL: "https://example.com/m.tar.bz2", ArchiveSize: 163002883, ArchiveSHA256: strings.Repeat("d", 64),
	}
	url, size, sha, format, err = pickAssetURL(mdl)
	if err != nil || url != mdl.ArchiveURL || size != mdl.ArchiveSize || sha != mdl.ArchiveSHA256 || format != "tar.bz2" {
		t.Fatalf("模型归档资产选择不符: %s %d %s %s %v", url, size, sha, format, err)
	}
	// 引擎未声明当前平台 → 报错
	e2 := engineTestEntry("https://example.com/e.tar.gz", 1, wantSHA)
	e2.Assets = map[string]Asset{"plan9/amd64": {URL: "https://example.com/e.tar.gz", SizeBytes: 1, SHA256: wantSHA}}
	if _, _, _, _, err := pickAssetURL(e2); err == nil || !strings.Contains(err.Error(), "暂不提供该引擎") {
		t.Fatalf("未声明平台应报错,实际: %v", err)
	}
}

func TestRestoreEngineInstalled(t *testing.T) {
	// 引擎安装态从 enginesDir 恢复(裁定 2:引擎相关测试用 enginesDir 播种)
	m, _, _ := newTestManager(t,
		[]Entry{engineTestEntry("https://example.com/e.tar.gz", 3, strings.Repeat("a", 64))},
		func(modelsDir, engines string) {
			dir := filepath.Join(engines, "eng-test", "pkg", "bin")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sherpa-onnx-offline"), []byte("elf"), 0o755); err != nil {
				t.Fatal(err)
			}
			mf := manifest{ID: "eng-test", Revision: "master",
				Files:       []manifestFile{{Path: "pkg/bin/sherpa-onnx-offline", Size: 3}},
				Binary:      "pkg/bin/sherpa-onnx-offline",
				CompletedAt: time.Now().UTC().Format(time.RFC3339)}
			raw, _ := json.Marshal(mf)
			if err := os.WriteFile(filepath.Join(engines, "eng-test", "manifest.json"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		})
	v := viewOrFatal(t, m, "eng-test")
	if v.Status != StatusInstalled || v.DownloadedBytes != 3 {
		t.Fatalf("引擎安装态应从 enginesDir 恢复: %+v", v)
	}
}

// ─── 消费者访问器:目录条目 / 安装态 / 引擎二进制 / 单文件路径 ─────────────

func TestGetEntryAndInstalled(t *testing.T) {
	m, _, _ := newTestManager(t, testEntries())
	if e, ok := m.GetEntry("asr-small"); !ok || e.ID != "asr-small" {
		t.Fatal("GetEntry 应返回目录条目")
	}
	if m.Installed("asr-small") {
		t.Fatal("未下载不应报告已安装")
	}
}

func TestEngineBinaryLifecycle(t *testing.T) {
	entries := []Entry{engineEntry()} // Task 1 的 fixture;Assets 带 darwin/arm64
	m, _, enginesDir := newTestManager(t, entries)
	if _, err := m.EngineBinary("audiocpp"); err == nil {
		t.Fatal("未安装应报错")
	}
	// 直接播种一个合法引擎 manifest + 假二进制(不走真下载)。
	// 引擎安装根在 enginesDir(裁定:引擎 manifest 播种到 engines/audiocpp)。
	dir := filepath.Join(enginesDir, "audiocpp", "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "audiocpp_server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mf := manifest{ID: "audiocpp", Binary: filepath.Join("pkg", "audiocpp_server"), CompletedAt: "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(enginesDir, "audiocpp", "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.Installed("audiocpp") {
		t.Fatal("播种 manifest 后应报告已安装")
	}
	got, err := m.EngineBinary("audiocpp")
	if err != nil || got != bin {
		t.Fatalf("EngineBinary 应返回 %s,实际 %s err=%v", bin, got, err)
	}
}

func TestInstalledModelFile(t *testing.T) {
	// 裸单文件条目:直链 fixture
	e := validEntry()
	e.ID, e.Kind = "gguf-model", "tts"
	e.Repo = ""
	e.FileURLs = map[string]string{"model.bin": "https://example.com/model.bin"}
	m, base, _ := newTestManager(t, []Entry{e})
	if _, err := m.InstalledModelFile("gguf-model"); err == nil {
		t.Fatal("未安装应报错")
	}
	dir := filepath.Join(base, "gguf-model")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := manifest{ID: "gguf-model", Files: []manifestFile{{Path: "model.bin", Size: 4}}, CompletedAt: "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(mf)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := m.InstalledModelFile("gguf-model")
	if err != nil || got != filepath.Join(dir, "model.bin") {
		t.Fatalf("应返回唯一文件绝对路径,实际 %s err=%v", got, err)
	}
}
