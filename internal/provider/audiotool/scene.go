// 场景切点 audio.scene_detect：ffmpeg select=gt(scene,θ) 检测视频镜头切换，
// 产物为切点列表 JSON（scenes kind）。思路借鉴 PySceneDetect（BSD-3），
// 不引 OpenCV/Python——ffmpeg 原生 scene 检测足够覆盖「镜头切分」场景。
package audiotool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// sceneMark 一个场景切点：该帧与前一帧的内容差异分（0-1，越高越像切换）。
type sceneMark struct {
	Time  float64
	Score float64
}

// sceneCut scenes.json 的一个场景段。
type sceneCut struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Duration float64 `json:"duration"`
	Score    float64 `json:"score"`
}

// sceneMetaRe 解析 metadata=print 输出的 pts_time 与 lavfi.scene_score。
var (
	ptsTimeRe    = regexp.MustCompile(`pts_time:\s*([\d.]+)`)
	sceneScoreRe = regexp.MustCompile(`lavfi\.scene_score=([\d.]+)`)
)

// detectScenes 跑一遍 select=gt(scene,θ)+metadata=print 收集切点（不解码音频、
// 不写产物文件，开销为一次顺序解码）。stdout 按帧输出 metadata 块。
func detectScenes(ctx context.Context, src string, threshold float64) ([]sceneMark, error) {
	args := []string{"-hide_banner", "-nostdin", "-i", src, "-an",
		"-vf", fmt.Sprintf("select='gt(scene,%s)',metadata=print:file=-", trimFloat(threshold)),
		"-f", "null", "-"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 ffmpeg 管道失败: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 ffmpeg 失败: %w", err)
	}
	var marks []sceneMark
	var pendingTime = -1.0
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := ptsTimeRe.FindStringSubmatch(line); m != nil {
			pendingTime, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := sceneScoreRe.FindStringSubmatch(line); m != nil && pendingTime >= 0 {
			score, _ := strconv.ParseFloat(m[1], 64)
			marks = append(marks, sceneMark{Time: pendingTime, Score: score})
			pendingTime = -1
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("任务已取消: %w", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("场景检测失败: %s", msg)
	}
	return marks, nil
}

// scenesFromMarks 切点 → 场景段列表：首段从 0 起，末段到 totalSec 收口；
// 相邻切点间隔小于 minScene 的丢弃后者（镜头抖动/同景误检的合并）。
func scenesFromMarks(marks []sceneMark, totalSec, minScene float64) []sceneCut {
	bounds := []float64{0}
	for _, m := range marks {
		if minScene <= 0 || m.Time-bounds[len(bounds)-1] >= minScene {
			bounds = append(bounds, m.Time)
		}
	}
	if len(bounds) == 1 { // 无有效切点：整段一个场景
		if totalSec <= 0 {
			return nil
		}
		return []sceneCut{{Start: 0, End: totalSec, Duration: totalSec}}
	}
	if totalSec > bounds[len(bounds)-1] {
		bounds = append(bounds, totalSec)
	}
	scoreByIDX := map[int]float64{}
	for i, m := range marks {
		scoreByIDX[i+1] = m.Score
	}
	cuts := make([]sceneCut, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		s, e := bounds[i], bounds[i+1]
		if e-s <= 0 {
			continue
		}
		cuts = append(cuts, sceneCut{Start: round2(s), End: round2(e), Duration: round2(e - s), Score: round2(scoreByIDX[i+1])})
	}
	return cuts
}

// ============================================================
// 场景切点 audio.scene_detect
// ============================================================

// sceneDetectTool 场景切点：检测视频镜头切换，产出切点列表 JSON（剪辑参照）。
type sceneDetectTool struct{ baseTool }

func (t *sceneDetectTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "audio", Name: "scene_detect", Title: "场景切点",
		Description: "检测视频的镜头切换点，产出场景切分列表（本地处理）：长视频拆条、素材预筛的剪辑参照",
		Group:       "音频",
	}
}

func (t *sceneDetectTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "threshold", Label: "判定阈值", Type: provider.ParamFloat, Default: 0.3, Group: "检测",
			Placeholder: "0.01 ~ 0.99，帧间差异分超过该值判为切换（默认 0.3）"},
		{Key: "min_scene", Label: "最短场景（秒）", Type: provider.ParamFloat, Default: 1, Group: "检测",
			Placeholder: "0 ~ 60，短于此的场景并入前一镜（过滤抖动误检）"},
	}
}

func (t *sceneDetectTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	src := in.Files["audio"]
	if src == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传视频文件")
	}
	if err := ensureFFmpeg(); err != nil {
		return provider.TaskOutput{}, err
	}
	threshold := clampF(paramFloat(in.Params, "threshold", 0.3), 0.01, 0.99)
	minScene := clampF(paramFloat(in.Params, "min_scene", 1), 0, 60)

	report(10, "正在检测场景切换…", nil)
	marks, err := detectScenes(ctx, src, threshold)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(70, fmt.Sprintf("检出 %d 个切点，正在整理场景列表…", len(marks)), nil)
	totalSec := probeVideoDuration(ctx, src)
	cuts := scenesFromMarks(marks, totalSec, minScene)

	reqID := uuid.NewString()[:8]
	dir := filepath.Join(t.outDir, "editor", reqID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	payload, _ := json.MarshalIndent(cuts, "", "  ")
	final := filepath.Join(dir, fmt.Sprintf("%s_scenes.json", srcBaseName(src)))
	if err := os.WriteFile(final, payload, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入产物失败: %w", err)
	}
	rel, _ := filepath.Rel(t.outDir, final)
	report(100, "场景检测完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "scenes", Path: rel, Format: "json", Size: int64(len(payload)),
		}},
		Summary: map[string]any{
			"scenes":       len(cuts),
			"cuts":         len(marks),
			"threshold":    threshold,
			"duration_sec": round2(totalSec),
			"source":       srcBaseName(src),
		},
	}, nil
}

// probeVideoDuration 容器级时长探测（视频无音频流时 probeAudio 会失败，
// 这里只取 format.duration；探测失败返回 0，末场景以最后切点收口）。
func probeVideoDuration(ctx context.Context, path string) float64 {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet",
		"-print_format", "json", "-show_format", path).Output()
	if err != nil {
		return 0
	}
	var po struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &po) != nil {
		return 0
	}
	return atofSafe(po.Format.Duration)
}
