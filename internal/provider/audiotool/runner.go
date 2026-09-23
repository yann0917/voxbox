// Package audiotool 本地音频剪辑工具集（ffmpeg 执行 + 纯 Go DSP 分析）。
//
// 与 gsgc（站点云端直连）互补：剪辑类操作（切割/合并/变调/均衡/响度/倒放）
// 走本地 ffmpeg——离线可用；调与 BPM 查询用内置 DSP 实现，
// 不依赖任何外部服务。ffmpeg/ffprobe 缺失时工具在 Run 期报出可定位的错误
// （剪辑不是降级场景：没有 ffmpeg 就没有产物）。
//
// 参数构建与进程执行分离：工具先把请求编译成 ffmpeg 参数（纯函数，可单测），
// 再交 runner 执行。进度经 -progress pipe:1 解析为任务进度。
package audiotool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ---------- 可执行文件探测 ----------

var lookPath = func(name string) error {
	_, err := exec.LookPath(name)
	return err
}

// ensureFFmpeg 检查 ffmpeg/ffprobe 可用性，缺失时给出安装指引。
func ensureFFmpeg() error {
	if err := lookPath("ffmpeg"); err != nil {
		return fmt.Errorf("服务器未安装 ffmpeg，音频剪辑功能不可用（请先安装：macOS brew install ffmpeg / Debian apt install ffmpeg）")
	}
	if err := lookPath("ffprobe"); err != nil {
		return fmt.Errorf("服务器未安装 ffprobe（随 ffmpeg 附带），无法探测音频信息")
	}
	return nil
}

// ---------- ffmpeg 执行 ----------

// runFFmpeg 执行一条 ffmpeg 命令。-progress pipe:1 输出经 onProgress 回报
// （percent 为 0-100，总时长未知时为 -1）。失败时错误携带 stderr 尾部便于定位。
func runFFmpeg(ctx context.Context, args []string, totalSec float64, onProgress func(percent int)) error {
	full := append([]string{"-hide_banner", "-nostdin", "-y", "-nostats", "-progress", "pipe:1", "-v", "error"}, args...)
	cmd := exec.CommandContext(ctx, "ffmpeg", full...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 ffmpeg 管道失败: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 ffmpeg 失败: %w", err)
	}
	consumeProgress(stdout, totalSec, onProgress)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("任务已取消: %w", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// 只保留最后一行（真正的报错原因），前几行是参数回显
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return fmt.Errorf("ffmpeg 处理失败: %s", msg)
	}
	return nil
}

// consumeProgress 逐行解析 -progress 输出（key=value 行，progress=end 为结束标记）。
func consumeProgress(rd io.Reader, totalSec float64, onProgress func(percent int)) {
	if onProgress == nil {
		_, _ = io.Copy(io.Discard, rd) // 必须排空管道，否则 ffmpeg 写满即阻塞
		return
	}
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var outTime float64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			// ffmpeg 历史命名不一致：out_time_us/out_time_ms 语义都是微秒
			outTime = atofSafe(val) / 1e6
		case "out_time":
			outTime = parseHMS(val)
		case "progress":
			p := -1
			if totalSec > 0 && outTime > 0 {
				p = int(outTime / totalSec * 100)
				if p < 0 {
					p = 0
				}
				if p > 100 {
					p = 100
				}
			}
			onProgress(p)
		}
	}
}

// ---------- ffprobe 探测 ----------

// AudioInfo 是剪辑关心的最小探测结果。
type AudioInfo struct {
	Duration   float64 `json:"duration"`    // 秒
	SampleRate int     `json:"sample_rate"` // Hz
	Channels   int     `json:"channels"`
	BitRate    int64   `json:"bit_rate"` // bps，无则为 0（WAV/PCM 常缺省）
	Codec      string  `json:"codec"`    // 如 mp3 / aac / pcm_s16le
}

type probeOutput struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Channels   int    `json:"channels"`
		SampleRate string `json:"sample_rate"`
		BitRate    string `json:"bit_rate"`
		Duration   string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

// probeAudio 探测首个音频流信息；duration 缺失时回落容器级，再回落流级。
func probeAudio(ctx context.Context, path string) (*AudioInfo, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet",
		"-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		return nil, fmt.Errorf("探测音频信息失败（ffprobe）: %w", err)
	}
	var po probeOutput
	if err := json.Unmarshal(out, &po); err != nil {
		return nil, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}
	info := &AudioInfo{Duration: atofSafe(po.Format.Duration), BitRate: int64(atofSafe(po.Format.BitRate))}
	for i := range po.Streams {
		s := &po.Streams[i]
		if s.CodecType != "audio" {
			continue
		}
		info.Codec = s.CodecName
		info.SampleRate = int(atofSafe(s.SampleRate))
		info.Channels = s.Channels
		if info.BitRate == 0 {
			info.BitRate = int64(atofSafe(s.BitRate))
		}
		if info.Duration == 0 {
			info.Duration = atofSafe(s.Duration)
		}
		break
	}
	if info.Duration <= 0 {
		return nil, fmt.Errorf("探测音频时长失败：文件可能不是有效的音频（%s）", path)
	}
	return info, nil
}

// ---------- PCM 解码（分析用）----------

// decodeMonoPCM 把音频解码为单声道 22050Hz 的 s16le PCM。
// 分析器只关心相对能量与时频结构，22.05kHz 单声道已足够（色度最高 ~5.5kHz
// 基频，BPM 通量以节拍粒度为主），文件越长解码开销越可控。
const (
	analyzeSampleRate = 22050
	decodeTimeout     = 10 * time.Minute
)

func decodeMonoPCM(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, decodeTimeout)
	defer cancel()
	args := []string{"-v", "error", "-i", path,
		"-vn", "-ac", "1", "-ar", strconv.Itoa(analyzeSampleRate),
		"-f", "s16le", "-"}
	out, err := exec.CommandContext(ctx, "ffmpeg", args...).Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("解码超时或已取消: %w", ctx.Err())
		}
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("解码音频失败: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("解码音频失败: %w", err)
	}
	return out, nil
}

// ---------- 小工具 ----------

func atofSafe(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

// parseHMS 解析 ffmpeg -progress 的 "00:01:23.456000" 时间。
func parseHMS(s string) float64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0
	}
	h, m, sec := atofSafe(parts[0]), atofSafe(parts[1]), atofSafe(parts[2])
	return h*3600 + m*60 + sec
}

// atempoChain 把任意倍率拆成 ffmpeg atempo 的合法区间（单级 [0.5, 2.0]）串。
// 如 4x → atempo=2,atempo=2；0.3x → atempo=0.5,atempo=0.6。
func atempoChain(ratio float64) (string, error) {
	if ratio <= 0 {
		return "", fmt.Errorf("变速倍率须为正数，收到 %g", ratio)
	}
	var stages []string
	for ratio > 2.0+1e-9 {
		stages = append(stages, "atempo=2")
		ratio /= 2.0
	}
	for ratio < 0.5-1e-9 {
		stages = append(stages, "atempo=0.5")
		ratio *= 2.0
	}
	stages = append(stages, fmt.Sprintf("atempo=%s", trimFloat(ratio)))
	return strings.Join(stages, ","), nil
}

// trimFloat 输出 filter 参数用的浮点串（去多余零，最多 6 位小数）。
func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
