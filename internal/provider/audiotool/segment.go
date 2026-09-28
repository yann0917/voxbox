package audiotool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 导出包装器：供云厂商 ASR 工具复用本包的 ffmpeg/ffprobe 基建
//（zhipu/xiaomi 的超限音频自动分段），工具注册仍走 RegisterAll 无副作用。

// EnsureFFmpeg 检查 ffmpeg/ffprobe 可用性，缺失时返回带安装指引的错误。
func EnsureFFmpeg() error { return ensureFFmpeg() }

// ProbeAudio 探测音频文件信息（时长/采样率/声道/码率/编码）。
func ProbeAudio(ctx context.Context, path string) (*AudioInfo, error) {
	return probeAudio(ctx, path)
}

// HasExec 探测可执行文件是否在 PATH（测试与能力探测用）。
func HasExec(name string) bool {
	return lookPath(name) == nil
}

// SegmentAudioFile 用 ffmpeg segment muxer 把音频按 segSec 秒一段流拷贝切分
// （不重编码、无质量损失；mp3 等帧级编码按帧边界落刀），分段写入新建临时目录，
// 返回（临时目录, 按序分段路径）。调用方用完负责 os.RemoveAll(dir)。
// onProgress 可空，回报 0-100。
func SegmentAudioFile(ctx context.Context, path string, segSec float64, onProgress func(percent int)) (string, []string, error) {
	if err := ensureFFmpeg(); err != nil {
		return "", nil, err
	}
	info, err := probeAudio(ctx, path)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "voxbox-seg-")
	if err != nil {
		return "", nil, fmt.Errorf("创建分段临时目录失败: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		ext = ".mp3"
	}
	pattern := filepath.Join(dir, "seg-%03d"+ext)
	args := []string{
		"-i", path, "-map", "0:a:0", "-c", "copy",
		"-f", "segment", "-segment_time", strconv.FormatFloat(segSec, 'f', 3, 64),
		"-reset_timestamps", "1", pattern,
	}
	if err := runFFmpeg(ctx, args, info.Duration, onProgress); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	entries, err := filepath.Glob(filepath.Join(dir, "seg-*"+ext))
	if err != nil || len(entries) == 0 {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("ffmpeg 分段无输出（%s）", path)
	}
	sort.Strings(entries) // %03d 零填充，字典序即时序
	return dir, entries, nil
}
