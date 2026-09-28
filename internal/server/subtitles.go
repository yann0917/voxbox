package server

// 字幕工坊（本地能力：纯 Go 解析/分句/导出，零上游 API 成本）。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/subtitle"
)

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
