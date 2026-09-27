// Package voicelib 音色库：参考音频的入库/列表/预览/改名/删除。
//
// 磁盘即真相（与 localmodel 同 doctrine，不落 DB）：每个音色一个目录
// <dataDir>/voices/<uuid8>/，内含 voice.wav（ffmpeg 转码产物：24kHz 单声道
// pcm_s16le，超长按 -t 60 裁剪）与 meta.json（名称/时长/创建时间）。
// 入库失败（转码报错/成品太短/写元数据失败）一律清理半成品目录，磁盘不留残骸。
package voicelib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound 音色不存在（未知 id / 目录或成品已被清理 / id 形态非法）。
var ErrNotFound = errors.New("音色不存在")

// lookPath 可执行文件探测（测试可替换，与 audiotool 同款）。
var lookPath = func(name string) error {
	_, err := exec.LookPath(name)
	return err
}

const (
	minDurationMS = 1000 // 成品时长下限：低于 1 秒的参考音频拒收
	maxSeconds    = 60   // 入库裁剪上限：-t 60
)

// Voice 单条音色元数据（meta.json 的内容即此结构的 JSON）。
type Voice struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// Library 音色库：以 <dataDir>/voices 为根的目录型存储。
type Library struct {
	root string
}

// New 构造音色库（目录懒创建：首次 Add 时落盘）。
func New(dataDir string) *Library {
	return &Library{root: filepath.Join(dataDir, "voices")}
}

// Add 入库：ffmpeg 转码为 24kHz 单声道 pcm_s16le（超 60 秒裁剪）→ ffprobe 校验
// 成品时长 ≥1 秒 → 写 meta.json。任一步失败清理半成品目录并返回错误。
func (l *Library) Add(ctx context.Context, name, srcPath string) (Voice, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Voice{}, errors.New("参数错误：name 必填")
	}
	if err := lookPath("ffmpeg"); err != nil {
		return Voice{}, fmt.Errorf("服务器未安装 ffmpeg，音色入库不可用（请先安装：macOS brew install ffmpeg / Debian apt install ffmpeg）")
	}
	if err := lookPath("ffprobe"); err != nil {
		return Voice{}, fmt.Errorf("服务器未安装 ffprobe（随 ffmpeg 附带），无法探测音频时长")
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	dir := filepath.Join(l.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Voice{}, fmt.Errorf("创建音色目录失败: %w", err)
	}
	// 失败清理：转码/探测/写元数据任一步出错，半成品目录整体删除。
	cleanup := func() { _ = os.RemoveAll(dir) }
	wav := filepath.Join(dir, "voice.wav")
	args := []string{"-hide_banner", "-nostdin", "-y", "-v", "error",
		"-i", srcPath, "-t", strconv.Itoa(maxSeconds),
		"-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", wav}
	if err := runFFmpeg(ctx, args); err != nil {
		cleanup()
		return Voice{}, err
	}
	durMS, err := probeDurationMS(ctx, wav)
	if err != nil {
		cleanup()
		return Voice{}, err
	}
	if durMS < minDurationMS {
		cleanup()
		return Voice{}, fmt.Errorf("参考音频太短（需 1-60 秒）")
	}
	v := Voice{ID: id, Name: name, DurationMS: durMS, CreatedAt: time.Now()}
	if err := writeMeta(dir, v); err != nil {
		cleanup()
		return Voice{}, err
	}
	return v, nil
}

// List 全量音色（读目录聚合 meta.json，按创建时间倒序；损坏/缺失元数据的目录跳过）。
func (l *Library) List() []Voice {
	voices := []Voice{}
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return voices
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, err := readMeta(filepath.Join(l.root, e.Name()))
		if err != nil || v.ID == "" {
			continue
		}
		voices = append(voices, v)
	}
	slices.SortFunc(voices, func(a, b Voice) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return voices
}

// Path 音色成品 wav 的绝对路径；不存在报 ErrNotFound。
func (l *Library) Path(id string) (string, error) {
	dir, err := l.voiceDir(id)
	if err != nil {
		return "", err
	}
	wav := filepath.Join(dir, "voice.wav")
	if _, err := os.Stat(wav); err != nil {
		return "", ErrNotFound
	}
	return wav, nil
}

// Rename 改名（读改写 meta.json；音色不存在报 ErrNotFound）。
func (l *Library) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("参数错误：name 必填")
	}
	dir, err := l.voiceDir(id)
	if err != nil {
		return err
	}
	v, err := readMeta(dir)
	if err != nil {
		return ErrNotFound
	}
	v.Name = name
	return writeMeta(dir, v)
}

// Delete 删除音色（先定位后整目录删除；不存在报 ErrNotFound）。
func (l *Library) Delete(id string) error {
	dir, err := l.voiceDir(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除音色失败: %w", err)
	}
	return nil
}

// voiceDir 校验 id 形态并返回存在的音色目录。id 会拼进文件路径且来自 HTTP 参数，
// 非 8 位十六进制（uuid8 形态）一律按不存在处理，杜绝路径穿越。
func (l *Library) voiceDir(id string) (string, error) {
	if len(id) != 8 {
		return "", ErrNotFound
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return "", ErrNotFound
		}
	}
	dir := filepath.Join(l.root, id)
	if _, err := os.Stat(dir); err != nil {
		return "", ErrNotFound
	}
	return dir, nil
}

// ---------- ffmpeg 执行与探测（照 audiotool runner 模式）----------

// runFFmpeg 执行一条 ffmpeg 命令（CommandContext 绑 ctx），失败时错误携带
// stderr 末行——前几行是参数回显，末行才是真正的报错原因。
func runFFmpeg(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("任务已取消: %w", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return fmt.Errorf("音频转码失败: %s", msg)
	}
	return nil
}

// probeOutput ffprobe -print_format json 的最小解析结构。
type probeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Duration  string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// probeDurationMS 探测成品时长（毫秒）：容器级优先，回落首个音频流级，
// 都缺失视为无效音频（与 audiotool probeAudio 同一回落次序）。
func probeDurationMS(ctx context.Context, path string) (int64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet",
		"-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		return 0, fmt.Errorf("探测音频信息失败（ffprobe）: %w", err)
	}
	var po probeOutput
	if err := json.Unmarshal(out, &po); err != nil {
		return 0, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}
	sec := atofSafe(po.Format.Duration)
	if sec <= 0 {
		for _, s := range po.Streams {
			if s.CodecType == "audio" {
				sec = atofSafe(s.Duration)
				if sec > 0 {
					break
				}
			}
		}
	}
	if sec <= 0 {
		return 0, fmt.Errorf("探测音频时长失败：文件可能不是有效的音频")
	}
	return int64(sec*1000 + 0.5), nil
}

// ---------- 元数据读写 ----------

func readMeta(dir string) (Voice, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Voice{}, err
	}
	var v Voice
	if err := json.Unmarshal(data, &v); err != nil {
		return Voice{}, err
	}
	return v, nil
}

func writeMeta(dir string, v Voice) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644)
}

// atofSafe 宽松浮点解析（空串/非法值归 0，与 audiotool 同款）。
func atofSafe(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}
