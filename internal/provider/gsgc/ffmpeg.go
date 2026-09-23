// gsgc 分离产物的后处理：站点服务端固定输出 WAV（create_task 无格式参数，
// 见 site_tools.go 说明），本文件在产物落盘前做格式检查与转码——
// ffprobe 检测编码/码率，非 MP3 一律转成标准 MP3（128k，与项目分离默认档一致）。
// ffmpeg/ffprobe 缺失或转码失败时保留 WAV 降级继续（best-effort：不让已成功的
// 上游任务作废），降级原因写入 summary.warnings 供前端/调用方感知。
package gsgc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// mp3TargetBitrate 标准 MP3 码率：与项目「standard 128k」默认档一致
// （音乐下载/试听/分离四条路径统一 standard，见 README 与历史决策）。
const mp3TargetBitrate = "128k"

// 后处理依赖的可注入句柄（测试 stub 点）。
var (
	ffmpegLookPath = func() error {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return err
		}
		if _, err := exec.LookPath("ffprobe"); err != nil {
			return err
		}
		return nil
	}
	probeAudio = func(ctx context.Context, path string) (codec string, bitrate int64, err error) {
		out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error",
			"-select_streams", "a:0",
			"-show_entries", "stream=codec_name,bit_rate",
			"-of", "json", path).Output()
		if err != nil {
			return "", 0, err
		}
		var parsed struct {
			Streams []struct {
				CodecName string      `json:"codec_name"`
				BitRate   json.Number `json:"bit_rate,omitempty"`
			} `json:"streams"`
			Format struct {
				BitRate json.Number `json:"bit_rate,omitempty"`
			} `json:"format"`
		}
		if err := json.Unmarshal(out, &parsed); err != nil {
			return "", 0, err
		}
		if len(parsed.Streams) == 0 {
			return "", 0, fmt.Errorf("无音频流")
		}
		// 码率优先取流级；WAV/PCM 常缺省，回落容器级
		bitrate, _ = parsed.Streams[0].BitRate.Int64()
		if bitrate == 0 {
			bitrate, _ = parsed.Format.BitRate.Int64()
		}
		return parsed.Streams[0].CodecName, bitrate, nil
	}
	transcodeMP3 = func(ctx context.Context, src, dst string) error {
		cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-v", "error",
			"-i", src, "-vn",
			"-codec:a", "libmp3lame", "-b:a", mp3TargetBitrate, dst)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
)

// ensureStemMP3 分离音轨的格式检查与转码：
//   - ffmpeg/ffprobe 缺失 → 保留原文件（format 返回原扩展名），带降级说明；
//   - ffprobe 探测失败（文件损坏/无法解析）→ 保留原文件，不做盲转；
//   - 已是 MP3 → 原样保留；
//   - 其余（WAV/PCM 等）→ 转码标准 MP3 128k 并删除源 WAV。
//
// 返回最终文件路径、格式、是否发生转码、源码率（bps，探测失败为 0）与降级警告。
func ensureStemMP3(ctx context.Context, src string) (finalPath, format string, transcoded bool, sourceBitrate int64, warn string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(src), "."))
	if ext == "" {
		ext = "wav"
	}
	finalPath, format = src, ext

	if err := ffmpegLookPath(); err != nil {
		return finalPath, format, false, 0, "服务器未安装 ffmpeg/ffprobe，保留 WAV 产物"
	}
	codec, bitrate, err := probeAudio(ctx, src)
	if err != nil {
		return finalPath, format, false, 0, "无法探测音频编码，保留原格式"
	}
	if codec == "mp3" {
		return finalPath, format, false, bitrate, ""
	}

	dst := strings.TrimSuffix(src, filepath.Ext(src)) + ".mp3"
	if err := transcodeMP3(ctx, src, dst); err != nil {
		return finalPath, format, false, bitrate, fmt.Sprintf("转码 MP3 失败，保留 WAV: %v", err)
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst) // 删不掉旧文件就放弃转码结果，避免双份歧义
		return finalPath, format, false, bitrate, fmt.Sprintf("清理 WAV 失败，保留 WAV: %v", err)
	}
	return dst, "mp3", true, bitrate, ""
}
