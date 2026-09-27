package voicelib

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireFFmpeg 真依赖 ffmpeg/ffprobe 的用例守卫：探测失败即跳过（CI 无 ffmpeg
// 时测试仍可移植），与「ffmpeg 缺失报错」用例共用同一探测结论。
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if err := lookPath("ffmpeg"); err != nil {
		t.Skip("本机无 ffmpeg，跳过")
	}
	if err := lookPath("ffprobe"); err != nil {
		t.Skip("本机无 ffprobe，跳过")
	}
}

// genSine 用 ffmpeg lavfi 生成指定秒数的 440Hz 正弦 wav 作入库输入样本。
func genSine(t *testing.T, dir string, seconds float64) string {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("sine_%v.wav", seconds))
	if out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", fmt.Sprintf("sine=frequency=440:duration=%v", seconds), p).CombinedOutput(); err != nil {
		t.Fatalf("生成正弦失败: %v: %s", err, out)
	}
	return p
}

// ① Add 成功：转码产物落盘（voice.wav + meta.json）、时长≈2000±500ms、List 可见。
func TestAddSuccess(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := genSine(t, dir, 2)
	l := New(filepath.Join(dir, "data"))
	v, err := l.Add(context.Background(), "测试音色", src)
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if v.Name != "测试音色" {
		t.Errorf("Name = %q, want 测试音色", v.Name)
	}
	if v.ID == "" {
		t.Error("ID 不应为空")
	}
	if v.DurationMS < 1500 || v.DurationMS > 2500 {
		t.Errorf("DurationMS = %d, want 2000±500", v.DurationMS)
	}
	if _, err := os.Stat(filepath.Join(l.root, v.ID, "voice.wav")); err != nil {
		t.Errorf("voice.wav 应落盘: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.root, v.ID, "meta.json")); err != nil {
		t.Errorf("meta.json 应落盘: %v", err)
	}
	list := l.List()
	if len(list) != 1 || list[0].ID != v.ID {
		t.Errorf("List() = %v, want 1 条且含新音色", list)
	}
}

// ② 成品 <1 秒拒收：报「太短」且半成品目录被清理（磁盘不留残骸）。
func TestAddTooShortRejected(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := genSine(t, dir, 0.3)
	l := New(filepath.Join(dir, "data"))
	_, err := l.Add(context.Background(), "太短", src)
	if err == nil || !strings.Contains(err.Error(), "太短") {
		t.Fatalf("err = %v, want 含「参考音频太短（需 1-60 秒）」", err)
	}
	entries, rderr := os.ReadDir(l.root)
	if rderr == nil && len(entries) != 0 {
		t.Errorf("拒收后应清理半成品目录, voices 下残留 %d 项", len(entries))
	}
}

// ③ Add 后 Rename 生效：meta.json 更新、List 读到新名。
func TestRenameUpdatesMeta(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	l := New(filepath.Join(dir, "data"))
	v, err := l.Add(context.Background(), "旧名", genSine(t, dir, 2))
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if err := l.Rename(v.ID, "新名字"); err != nil {
		t.Fatalf("Rename 失败: %v", err)
	}
	list := l.List()
	if len(list) != 1 || list[0].Name != "新名字" {
		t.Errorf("List() = %v, want 名称已改为「新名字」", list)
	}
}

// ④ Delete 后 List 不再包含、Path 报 ErrNotFound。
func TestDeleteRemovesVoice(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	l := New(filepath.Join(dir, "data"))
	v, err := l.Add(context.Background(), "待删", genSine(t, dir, 2))
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if err := l.Delete(v.ID); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if got := l.List(); len(got) != 0 {
		t.Errorf("Delete 后 List() = %v, want 空", got)
	}
	if p, err := l.Path(v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete 后 Path err = %v, want ErrNotFound（path=%q）", err, p)
	}
}

// ⑤ Path 未知 id → ErrNotFound；非法 id（路径注入形态）同样按不存在处理。
func TestPathUnknownID(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "data"))
	if p, err := l.Path("00000000"); !errors.Is(err, ErrNotFound) || p != "" {
		t.Errorf("未知 id: path=%q err=%v, want \"\"/ErrNotFound", p, err)
	}
	if _, err := l.Path("../evil"); !errors.Is(err, ErrNotFound) {
		t.Errorf("路径注入 id err = %v, want ErrNotFound", err)
	}
}

// ⑥ name 空 → 参数错误（校验先于 ffmpeg 探测，无 ffmpeg 环境亦可跑）。
func TestAddEmptyName(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "data"))
	if _, err := l.Add(context.Background(), "  ", "whatever.wav"); err == nil ||
		!strings.Contains(err.Error(), "参数错误") {
		t.Fatalf("err = %v, want 参数错误", err)
	}
}

// ⑦ lookPath 缺失 → 明确的 ffmpeg 错误（替换探测函数模拟未安装环境）。
func TestAddFFmpegMissing(t *testing.T) {
	orig := lookPath
	lookPath = func(string) error { return exec.ErrNotFound }
	t.Cleanup(func() { lookPath = orig })
	l := New(filepath.Join(t.TempDir(), "data"))
	_, err := l.Add(context.Background(), "x", "/tmp/x.wav")
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("err = %v, want 明确的 ffmpeg 缺失错误", err)
	}
}
