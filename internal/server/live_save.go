// live_save.go 实时字幕存任务收束:会话全量结果组装为 asr 任务直写 store(不经合成
// provider Tool——无输入文件、无重跑语义),产物落 dataDir 常规目录,历史详情纪要区
// (refine)按 Summary 形状直接可用:火山会话=segments(+SRT 字幕),本地会话=text。
package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
)

// liveSave 把一次实时字幕会话落库为已成功的 asr 任务:
//   - 产物:转写文本 asr/<uuid>.txt 恒有;火山会话分句非空再落 SRT 字幕 asr/<uuid>.srt;
//   - Params: {version:"live",engine,duration_ms};Summary: 火山=segments/duration_ms/
//     source/engine(与 asr 工具同形,另带 engine),本地=text/duration_ms/source/engine;
//   - Provider: 火山会话=volcengine、本地会话=local(历史列表按 provider 徽标区分)。
//
// 与引擎任务一致:先落库(任务+产物)再发 hub done 事件,历史列表实时刷新。
func (s *Server) liveSave(userID, engine, prov string, res liveResult) (string, error) {
	if res.Text == "" && len(res.Segments) == 0 {
		return "", fmt.Errorf("会话无识别内容,无可保存结果")
	}
	dataDir := s.svc.Config().DataDir
	id := uuid.NewString()

	txtRel := filepath.Join("asr", id+".txt")
	if err := writeArtifactFile(dataDir, txtRel, res.Text); err != nil {
		return "", err
	}
	arts := []provider.Artifact{{
		Kind: "transcript", Path: txtRel, Format: "txt",
		Size: int64(len(res.Text)), DurationMS: res.DurationMS,
	}}

	summary := map[string]any{
		"duration_ms": res.DurationMS,
		"source":      "live",
		"engine":      engine,
	}
	// 降级交付(会话异常但保留部分文本)在 Summary 留痕,历史详情可解释内容不完整。
	if res.Degraded {
		summary["degraded"] = true
	}
	// 火山会话:segments + SRT 字幕(复用 volcengine BuildSRT);本地会话无时间戳,纯文本。
	segs := make([]map[string]any, 0, len(res.Segments))
	speakers := map[string]int{}
	for _, seg := range res.Segments {
		m := map[string]any{"text": seg.Text, "start_ms": seg.StartMS, "end_ms": seg.EndMS}
		if seg.Speaker != "" {
			m["speaker"] = seg.Speaker
			speakers[seg.Speaker]++
		}
		segs = append(segs, m)
	}
	if len(segs) > 0 {
		summary["segments"] = segs
		if len(speakers) > 0 {
			summary["speakers_count"] = len(speakers)
		}
		volcSegs := make([]volcengine.ASRSegment, 0, len(res.Segments))
		for _, seg := range res.Segments {
			volcSegs = append(volcSegs, volcengine.ASRSegment{
				Text: seg.Text, StartMS: seg.StartMS, EndMS: seg.EndMS, Speaker: seg.Speaker,
			})
		}
		srtRel := filepath.Join("asr", id+".srt")
		srtContent := volcengine.BuildSRT(volcSegs)
		if err := writeArtifactFile(dataDir, srtRel, srtContent); err != nil {
			return "", err
		}
		arts = append(arts, provider.Artifact{Kind: "subtitle", Path: srtRel, Format: "srt", Size: int64(len(srtContent))})
	} else {
		summary["text"] = res.Text
	}

	paramsRaw, _ := json.Marshal(map[string]any{
		"version": "live", "engine": engine, "duration_ms": res.DurationMS,
	})
	summaryRaw, _ := json.Marshal(summary)
	// 标题取识别文本前缀（与 ASR 完成时派生同一实现）；全空文本兜底无时间戳的「实时字幕」。
	title := store.ASRTitleFromSummary(string(summaryRaw))
	if title == "" {
		title = "实时字幕"
	}
	t := &store.Task{
		ID: id, UserID: userID, Provider: prov, Tool: "asr",
		Status: store.StatusSucceeded, Progress: 100,
		Params: string(paramsRaw), Summary: string(summaryRaw),
		Title: title,
	}
	if err := s.svc.DB().CreateTask(t); err != nil {
		return "", fmt.Errorf("任务落库失败: %w", err)
	}
	for _, a := range arts {
		meta, _ := json.Marshal(a.Meta)
		sa := store.Artifact{
			ID: uuid.NewString(), TaskID: t.ID, UserID: userID,
			Kind: a.Kind, Path: a.Path, Filename: filepath.Base(a.Path),
			Format: a.Format, Size: a.Size, DurationMS: a.DurationMS, Meta: string(meta),
		}
		if err := s.svc.DB().CreateArtifact(&sa); err != nil {
			return "", fmt.Errorf("产物落库失败: %w", err)
		}
	}
	// 历史列表实时刷新:与任务引擎 done 事件同形状(先落库后发事件,同引擎口径)。
	s.hub.Notify(task.Event{
		Type: "done", TaskID: t.ID, Provider: prov, Tool: "asr", Progress: 100, Artifacts: arts,
	})
	return t.ID, nil
}

// writeArtifactFile 产物落盘(dataDir 相对路径;.part→rename 防半文件)。
func writeArtifactFile(dataDir, rel, content string) error {
	abs := filepath.Join(dataDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("创建产物目录失败: %w", err)
	}
	part := abs + ".part"
	if err := os.WriteFile(part, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入产物文件失败: %w", err)
	}
	if err := os.Rename(part, abs); err != nil {
		return fmt.Errorf("产物落盘失败: %w", err)
	}
	return nil
}
