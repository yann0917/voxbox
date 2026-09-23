// 调与 BPM 查询工具：解码 → 纯 Go DSP 分析（dsp.go）→ 结构化结果。
// 无产物落盘，结果全部进任务 Summary（前端/CLI/MCP 直接读）。
package audiotool

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"

	"github.com/yann0917/voxbox/internal/provider"
)

// analyzeMaxSec 分析输入的时长上限：超长曲目截断解码（节拍与调性统计
// 在前几分钟内已收敛，全曲解码纯属白烧 CPU）。
const analyzeMaxSec = 600

type analyzeTool struct{ baseTool }

func (t *analyzeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "analyze", Title: "调与 BPM 查询",
		Description: "分析音乐查找调（key）、音阶（大/小调）、Camelot 编码与 BPM 节奏，内置 DSP 无需外部服务",
		Group:       "音频",
	}
}

func (t *analyzeTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{}
}

func (t *analyzeTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
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
	probeSec := info.Duration
	if probeSec > analyzeMaxSec {
		probeSec = analyzeMaxSec
	}
	report(15, "正在解码音频…", nil)
	pcm, err := decodeMonoPCM(ctx, src)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if len(pcm) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("音频无可解码内容（空文件或全静音）")
	}
	report(45, "正在分析调性与节奏…", nil)
	samples := pcmToFloat(pcm)
	res := analyzeSamples(samples, analyzeSampleRate)
	report(95, "分析完成", nil)

	keyName := fmt.Sprintf("%s %s", res.Key, scaleCN(res.Scale))
	summary := map[string]any{
		"op":           "analyze",
		"bpm":          res.BPM,
		"bpm_alts":     res.BPMAlts,
		"key":          res.Key,
		"scale":        res.Scale,
		"key_name":     keyName,
		"camelot":      res.Camelot,
		"key_score":    round2(res.KeyScore),
		"key_margin":   round2(res.KeyMargin),
		"duration_sec": round2(info.Duration),
		"sample_rate":  info.SampleRate,
		"channels":     info.Channels,
		"bit_rate":     info.BitRate,
		"codec":        info.Codec,
		"source":       filepath.Base(src),
	}
	return provider.TaskOutput{Summary: summary}, nil
}

// pcmToFloat 小端 int16 PCM → [-1,1] 浮点。
func pcmToFloat(pcm []byte) []float64 {
	n := len(pcm) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
	}
	return out
}

func scaleCN(scale string) string {
	if scale == "minor" {
		return "小调"
	}
	return "大调"
}
