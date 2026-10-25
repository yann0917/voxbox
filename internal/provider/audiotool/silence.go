// 剪静音 audio.silence_cut：silencedetect 探段（元数据）+ silenceremove 剪除。
// 思路借鉴 auto-editor（按响度自动剪静音），全本地 ffmpeg 实现：
// silencedetect 只为 Summary 提供剪了什么；实际剪除断 silenceremove 一遍完成
// （stop_duration=最短静音判定，stop_silence=每段保留缓冲）。
package audiotool

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// silenceSpan silencedetect 输出的一段静音（秒）。
type silenceSpan struct {
	Start float64
	End   float64
}

// silenceRe 解析 ffmpeg silencedetect 的 stderr 行：
// [silencedetect @ ...] silence_start: 1.234 与 silence_end: 2.5 | silence_duration: 1.266
var (
	silenceStartRe = regexp.MustCompile(`silence_start:\s*(-?[\d.]+)`)
	silenceEndRe   = regexp.MustCompile(`silence_end:\s*([\d.]+)`)
)

// detectSilence 跑一遍 silencedetect 收集静音段（-vn 只看音频，开销小）。
// 尾部未闭合的静音（到文件结束仍静音，无 silence_end 行）以 totalSec 收口。
func detectSilence(ctx context.Context, src string, thresholdDB float64, minSilence float64, totalSec float64) []silenceSpan {
	args := []string{"-hide_banner", "-nostdin", "-i", src, "-vn",
		"-af", fmt.Sprintf("silencedetect=noise=%sdB:d=%s", trimFloat(thresholdDB), trimFloat(minSilence)),
		"-f", "null", "-"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	var spans []silenceSpan
	var openStart = -1.0
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := silenceStartRe.FindStringSubmatch(line); m != nil {
			openStart, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := silenceEndRe.FindStringSubmatch(line); m != nil {
			end, _ := strconv.ParseFloat(m[1], 64)
			if openStart >= 0 {
				spans = append(spans, silenceSpan{Start: openStart, End: end})
				openStart = -1
			}
		}
	}
	_ = cmd.Wait()
	if openStart >= 0 && totalSec > openStart {
		spans = append(spans, silenceSpan{Start: openStart, End: totalSec})
	}
	return spans
}

// buildSilenceCutArgs 编译剪静音的 ffmpeg 参数（不含输入，-i 由执行层统一注入）。
// 两遍法：detectSilence 已按 min_silence 门槛检出长静音段，这里 aselect 精确
// 拼回保留段——每段静音在其起点后/终点前各保留 padding（防切字头字尾），
// 短于 min_silence 的停顿（换气）天然不在段列表里、整段保留。
// （silenceremove 的 stop_duration 语义是「每段保留量」而非判定门槛，
// 与「短停顿保留、长静音剪除」的门槛语义不符，故不走单遍方案。）
func buildSilenceCutArgs(spans []silenceSpan, total, padding float64, out outputSpec, srcPath string) ([]string, error) {
	ext, codec, bitrate := out.resolve(srcPath)
	// 保留区间：[0, s1+p] ∪ [e1-p, s2+p] ∪ … ∪ [eN-p, total]
	var keep []string
	prev := 0.0
	for _, s := range spans {
		p := clampF(padding, 0, (s.End-s.Start)/2) // 缓冲不超过半段静音
		start := prev
		end := clampF(s.Start+p, 0, total)
		if end > start {
			keep = append(keep, fmt.Sprintf("between(t,%s,%s)", trimFloat(start), trimFloat(end)))
		}
		prev = clampF(s.End-p, prev, total)
	}
	if total > prev {
		keep = append(keep, fmt.Sprintf("between(t,%s,%s)", trimFloat(prev), trimFloat(total)))
	}
	if len(keep) == 0 {
		return nil, fmt.Errorf("没有可保留的音频段（整段均为静音）")
	}
	filter := fmt.Sprintf("aselect='%s',asetpts=N/SR/TB", strings.Join(keep, "+"))
	args := []string{"-af", filter}
	return append(args, encodeTail(ext, codec, bitrate)...), nil
}

// ============================================================
// 剪静音 audio.silence_cut
// ============================================================

// silenceCutTool 剪静音：按响度阈值自动剪除长静音段（播客/口播/转写前置）。
type silenceCutTool struct{ baseTool }

func (t *silenceCutTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "silence_cut", Title: "剪静音",
		Description: "按响度自动剪除长静音段（本地处理）：适合口播、播客修剪与转写前裁剪提速，可保留换气停顿",
		Group:       "音频",
	}
}

func (t *silenceCutTool) ParamSpecs() []provider.ParamSpec {
	return append([]provider.ParamSpec{
		{Key: "threshold", Label: "静音阈值（dB）", Type: provider.ParamFloat, Default: -35, Group: "检测",
			Placeholder: "-60 ~ -10，低于该响度视为静音（默认 -35）"},
		{Key: "min_silence", Label: "最短静音（秒）", Type: provider.ParamFloat, Default: 0.5, Group: "检测",
			Placeholder: "0.1 ~ 10，短于此的停顿保留（换气/自然停顿）"},
		{Key: "padding", Label: "切口缓冲（秒）", Type: provider.ParamFloat, Default: 0.1, Group: "检测",
			Placeholder: "0 ~ 0.5，每段静音保留的缓冲，防切字头字尾"},
	}, outputSpecs()...)
}

func (t *silenceCutTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	threshold := clampF(paramFloat(in.Params, "threshold", -35), -60, -10)
	minSilence := clampF(paramFloat(in.Params, "min_silence", 0.5), 0.1, 10)
	padding := clampF(paramFloat(in.Params, "padding", 0.1), 0, 0.5)

	report(5, "正在探测音频信息…", nil)
	info, err := probeAudio(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(15, "正在检测静音段…", nil)
	spans := detectSilence(ctx, src, threshold, minSilence, info.Duration)
	removed := 0.0
	for _, s := range spans {
		removed += s.End - s.Start
	}

	var out outputSpec
	out.Format = paramString(in.Params, "format")
	out.Bitrate = paramString(in.Params, "bitrate")
	reqID := uuid.NewString()[:8]
	ext, _, _ := out.resolve(src)
	artPath := filepath.Join("editor", reqID, fmt.Sprintf("%s_cut_silence.%s", srcBaseName(src), ext))
	args, err := buildSilenceCutArgs(spans, info.Duration, padding, out, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(20, fmt.Sprintf("检测到 %d 段静音（共 %.1f 秒），正在剪除…", len(spans), removed), nil)
	final, size, durMS, err := t.runToArtifact(ctx, []string{src}, args, artPath, paramString(in.Params, "_out"), info.Duration, report, "剪除静音")
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(100, "剪静音完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: final, Format: ext, Size: size, DurationMS: durMS,
		}},
		Summary: map[string]any{
			"duration_sec":     round2(info.Duration),
			"out_duration_sec": round2(float64(durMS) / 1000),
			"removed_sec":      round2(removed),
			"removed_segments": len(spans),
			"threshold_db":     threshold,
			"source":           srcBaseName(src),
		},
	}, nil
}
