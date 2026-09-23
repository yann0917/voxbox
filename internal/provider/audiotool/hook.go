// 副歌/hook 候选检测：纯函数 DSP + 工具壳（零音频产物，候选承载于 Summary）。
package audiotool

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"

	"github.com/yann0917/voxbox/internal/provider"
)

// hookCand 一个候选选区：时间（秒）与该窗的均值评分 [0,1]（越大越像副歌）。
type hookCand struct {
	Start float64
	End   float64
	Score float64
}

// hookCandidates 副歌候选检测：RMS 包络（0.5s 窗 0.25s 步）→ 帧评分
// 0.7·能量+0.3·rise_rate（各自 0-1 归一）→ 滑动 durS 秒窗逐帧起点求均值
// （帧分 0-1，均值即窗分，域 [0,1]）→ 贪心取 top count（不重叠、间隔 ≥5s），
// 按时间升序返回、至多 count 个；输入过短（< durS）返回 nil。归一化分母为 0
// （全静音 eMax=0 / 恒定幅度 rMax=0）时该项计 0 而非 NaN。纯函数：不动
// ffmpeg，合成信号单测锁行为。
func hookCandidates(samples []float64, sr float64, durS float64, count int) []hookCand {
	if len(samples) == 0 || sr <= 0 || durS <= 0 || count <= 0 {
		return nil
	}
	if float64(len(samples))/sr < durS {
		return nil // 音频比目标选区还短，无从选起
	}

	// 1) RMS 包络：0.5s 整窗、0.25s 步进，不足整窗的尾帧丢弃
	frameN := int(0.5 * sr)
	stepN := int(0.25 * sr)
	if frameN <= 0 || stepN <= 0 {
		return nil
	}
	var frames []float64
	for start := 0; start+frameN <= len(samples); start += stepN {
		var sum float64
		for i := start; i < start+frameN; i++ {
			sum += samples[i] * samples[i]
		}
		frames = append(frames, math.Sqrt(sum/float64(frameN)))
	}

	// 2) rise_rate：帧间增量的半波整流（每秒 4 帧），首帧无前帧计 0
	rise := make([]float64, len(frames))
	for i := 1; i < len(frames); i++ {
		if d := frames[i] - frames[i-1]; d > 0 {
			rise[i] = d
		}
	}

	// 3) 帧评分：能量与 rise 各按全曲峰值归一到 0-1 后加权；分母为 0
	//    （全静音 / 恒定幅度）时对应分量整体计 0
	eMax, rMax := 0.0, 0.0
	for i := range frames {
		if frames[i] > eMax {
			eMax = frames[i]
		}
		if rise[i] > rMax {
			rMax = rise[i]
		}
	}
	score := make([]float64, len(frames))
	for i := range score {
		if eMax > 0 {
			score[i] += 0.7 * frames[i] / eMax
		}
		if rMax > 0 {
			score[i] += 0.3 * rise[i] / rMax
		}
	}

	// 4) 窗评分：前缀和枚举每个窗起点帧 k（时间 k*0.25s），窗越过帧序列
	//    末尾的丢弃；winFrames 最少 1 帧，防 durS<0.25s 出现空窗。窗和除以
	//    winFrames 取均值 → Score 域 [0,1]，对齐 docs/mcp.md 与 MCP Description
	//    的「score 评分 0-1」契约；除以同一正常数不改排序与贪心选择。
	const stepSec = 0.25
	winFrames := int(durS / stepSec)
	if winFrames < 1 {
		winFrames = 1
	}
	winSec := float64(winFrames) * stepSec
	pre := make([]float64, len(score)+1)
	for i, v := range score {
		pre[i+1] = pre[i] + v
	}
	type winCand struct {
		k     int
		score float64
	}
	var cands []winCand
	for k := 0; k+winFrames <= len(score); k++ {
		cands = append(cands, winCand{k: k, score: (pre[k+winFrames] - pre[k]) / float64(winFrames)})
	}
	// 同分按起点帧先后，保证全静音/恒定幅度等无差别输入的输出确定
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].k < cands[j].k
	})

	// 5) 贪心：按积分降序取窗，与已选段重叠或间隔不足 5s 的跳过，取满为止
	const gapSec = 5.0
	var picked []hookCand
	for _, c := range cands {
		if len(picked) >= count {
			break
		}
		start := float64(c.k) * stepSec
		end := start + winSec
		conflict := false
		for _, p := range picked {
			// 候选两端各外扩 5s 缓冲后与已选段相交即视为冲突
			if start < p.End+gapSec && end+gapSec > p.Start {
				conflict = true
				break
			}
		}
		if !conflict {
			picked = append(picked, hookCand{Start: start, End: end, Score: c.score})
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Start < picked[j].Start })
	return picked
}

// ============================================================
// 副歌候选检测 audio.hook
// ============================================================

// hookTool 副歌候选检测：零音频产物，Summary 携带 top-N 候选（spec §1.1）。
// 解码复用 analyze 的 mono 22050 通道（decodeMonoPCM+pcmToFloat）。
// 参数：duration 目标切片时长秒 [5,60] 默认 30；count 候选数 [1,5] 默认 3。
type hookTool struct{ baseTool }

func (t *hookTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "hook", Title: "副歌候选检测",
		Description: "按能量与起伏定位最像副歌的 top-N 选区（本地 DSP 零额度），零产物：候选区间直接进结果 Summary 供前端展示",
		Group:       "音频",
	}
}

func (t *hookTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "duration", Label: "选区时长（秒）", Type: provider.ParamFloat, Default: "30", Group: "检测",
			Placeholder: "5 ~ 60，副歌片段目标长度"},
		{Key: "count", Label: "候选数量", Type: provider.ParamFloat, Default: "3", Group: "检测",
			Placeholder: "1 ~ 5，按得分取 top-N"},
	}
}

func (t *hookTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	report(5, "正在探测音频信息…", nil)
	info, err := probeAudio(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(15, "正在解码音频…", nil)
	pcm, err := decodeMonoPCM(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if len(pcm) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("音频无可解码内容（空文件或全静音）")
	}
	durS := clampF(paramFloat(in.Params, "duration", 30), 5, 60)
	count := int(clampF(paramFloat(in.Params, "count", 3), 1, 5))
	report(45, "正在分析副歌候选…", nil)
	cands := hookCandidates(pcmToFloat(pcm), analyzeSampleRate, durS, count)
	if len(cands) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("音频过短或无可检测内容")
	}
	// 全静音等无差别输入同样走到这里：候选分数为 0 照实返回，前端展示分数
	report(100, "检测完成", nil)
	out := make([]map[string]any, 0, len(cands))
	for _, c := range cands {
		out = append(out, map[string]any{
			"start": round2(c.Start), "end": round2(c.End), "score": round2(c.Score),
		})
	}
	return provider.TaskOutput{
		Summary: map[string]any{
			"candidates":   out,
			"duration_sec": round2(info.Duration),
			"source":       filepath.Base(src),
		},
	}, nil
}
