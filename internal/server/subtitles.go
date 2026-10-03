package server

// 字幕工坊（本地能力：纯 Go 解析/分句/导出，零上游 API 成本；翻译走
// internal/translate 三源引擎，SSE 协议与 refine/apply 同族）。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/subtitle"
	"github.com/yann0917/voxbox/internal/translate"
)

// subtitleTranslate 引擎测试缝：生产即 translate.Run，server 包测试替换为
// 假实现离线跑协议（refineStream 同做法）。
var subtitleTranslate = func(ctx context.Context, cfg *config.Config, segs []subtitle.Segment, o translate.Options, onProgress translate.Progress) (*translate.Result, error) {
	return translate.Run(ctx, cfg, segs, o, onProgress)
}

// listSubtitlePresets 字幕样式预设：单一事实来源（internal/subtitle.Presets），
// 前端预览与导出参数都以此为准，避免两处颜色定义漂移。
func (s *Server) listSubtitlePresets(c *gin.Context) {
	ok(c, subtitle.Presets)
}

type prepareSubtitlesReq struct {
	Mode       string `json:"mode"` // srt：解析 SRT；text：文稿分句草稿
	Content    string `json:"content"`
	DurationMS int64  `json:"duration_ms"` // text 模式的草稿总时长，按字数比例分配
}

func (s *Server) prepareSubtitles(c *gin.Context) {
	var req prepareSubtitlesReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		fail(c, CodeBadRequest, "参数错误：content 必填")
		return
	}
	var segs []subtitle.Segment
	switch req.Mode {
	case "srt":
		var err error
		if segs, err = subtitle.ParseSRT(req.Content); err != nil {
			fail(c, CodeBadRequest, err.Error())
			return
		}
	case "text":
		segs = subtitle.DraftSegments(req.Content, req.DurationMS)
	default:
		fail(c, CodeBadRequest, "mode 仅支持 srt | text")
		return
	}
	if len(segs) == 0 {
		fail(c, CodeBadRequest, "没有解析到可用内容")
		return
	}
	ok(c, gin.H{"segments": segs})
}

type exportSubtitlesReq struct {
	Segments []subtitle.Segment `json:"segments"`
	Format   string             `json:"format"` // srt | ass
	Style    struct {
		Name     string `json:"name"`
		FontSize int    `json:"font_size"`
		MarginV  int    `json:"margin_v"`
		Karaoke  *bool  `json:"karaoke"`
	} `json:"style"`
}

func (s *Server) exportSubtitles(c *gin.Context) {
	var req exportSubtitlesReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Segments) == 0 {
		fail(c, CodeBadRequest, "参数错误：segments 不能为空")
		return
	}
	var data []byte
	ct := "text/plain; charset=utf-8"
	filename := "subtitles"
	switch strings.ToLower(req.Format) {
	case "srt":
		data = subtitle.BuildSRT(req.Segments)
		filename += ".srt"
	case "ass":
		style := subtitle.PresetByName(req.Style.Name)
		if req.Style.FontSize > 0 {
			style.FontSize = req.Style.FontSize
		}
		if req.Style.MarginV > 0 {
			style.MarginV = req.Style.MarginV
		}
		if req.Style.Karaoke != nil {
			style.Karaoke = *req.Style.Karaoke
		}
		data = subtitle.BuildASS(req.Segments, style)
		ct = "text/x-ssa; charset=utf-8"
		filename += ".ass"
	default:
		fail(c, CodeBadRequest, "format 仅支持 srt | ass")
		return
	}
	if len(data) == 0 {
		fail(c, CodeBadRequest, "没有可导出的字幕内容")
		return
	}
	// 二进制流端点惯例：不套 JSON 包络，真实文件语义
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, ct, data)
}

type translateSubtitlesReq struct {
	Segments       []subtitle.Segment `json:"segments"`
	TargetLanguage string             `json:"target_language"`
	SourceLanguage string             `json:"source_language"`
	Source         string             `json:"source"`
	Provider       string             `json:"provider"`
	Model          string             `json:"model"`
}

// translateSubtitles 字幕批量翻译（SSE）：预检错误走 JSON 包络，流内帧
// progress → done(result) / error。协议与 refine/apply 同族。
func (s *Server) translateSubtitles(c *gin.Context) {
	var req translateSubtitlesReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Segments) == 0 {
		fail(c, CodeBadRequest, "参数错误：segments 不能为空")
		return
	}
	if strings.TrimSpace(req.TargetLanguage) == "" {
		fail(c, CodeBadRequest, "参数错误：target_language 必填")
		return
	}
	cfg := s.svc.Config()
	opts := translate.Options{
		Source: req.Source, SourceLang: req.SourceLanguage, TargetLang: req.TargetLanguage,
		Provider: req.Provider, Model: req.Model,
	}
	// 预检源可用性（快速失败，不进流）
	if _, err := translate.ResolveSource(cfg, opts.Source); err != nil {
		failErr(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	w := c.Writer
	writeEvent := func(v any) bool {
		raw, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: " + string(raw) + "\n\n")); err != nil {
			return false
		}
		w.Flush()
		return true
	}
	res, err := subtitleTranslate(c.Request.Context(), cfg, req.Segments, opts, func(done, total int) {
		_ = writeEvent(gin.H{"progress": gin.H{"done": done, "total": total}})
	})
	if err != nil {
		// 客户端主动中止属正常交互：不算错误、不再发事件
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": assistantErrCode(err), "message": err.Error()}})
		}
		return
	}
	// echo_checked 仅 AI 源逐条核对统计；volcengine/free 源无回显可比，恒为 0
	stats := gin.H{
		"total": res.Stats.Total, "translated": res.Stats.Translated,
		"repaired": res.Stats.Repaired, "untranslated": res.Stats.Untranslated,
		"echo_checked": res.Stats.EchoChecked, "source": res.Stats.Source,
	}
	writeEvent(gin.H{"done": true, "result": gin.H{"segments": res.Segments, "stats": stats}})
}

// listSubtitleLangs 翻译语言清单（火山 32 语种 = 三源统一口径）。
func (s *Server) listSubtitleLangs(c *gin.Context) {
	ok(c, volcengine.MTLanguages())
}
