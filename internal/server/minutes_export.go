package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/docx"
	"github.com/yann0917/voxbox/internal/store"
)

// 妙记导出：模板定义与 Markdown/Docx 组装都在服务端（单一事实来源，
// 教训来自字幕预设的颜色两处定义漂移），前端只负责选模板与下载；
// CLI/agent 可直接调这两个端点。

type minutesTemplate struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Sections []string `json:"sections"` // meta|summary|todos|chapters|translation|transcript
}

var minutesTemplates = []minutesTemplate{
	{ID: "standard", Name: "通用会议纪要", Sections: []string{"meta", "summary", "todos", "chapters", "translation", "transcript"}},
	{ID: "brief", Name: "简洁速记", Sections: []string{"meta", "summary", "todos"}},
	{ID: "actions", Name: "待办行动清单", Sections: []string{"meta", "todos"}},
}

func minutesTemplateByID(id string) minutesTemplate {
	for _, t := range minutesTemplates {
		if t.ID == id {
			return t
		}
	}
	return minutesTemplates[0]
}

// minutesDoc 妙记 summary 的视图（键位与 lark_tool.go 写入的一致）。
type minutesDoc struct {
	MinutesTitle    string   `json:"minutes_title"`
	SummaryText     string   `json:"summary_text"`
	TranslationText string   `json:"translation_text"`
	Features        []string `json:"features"`
	Sentences       int      `json:"sentences"`
	SpeakersCount   int      `json:"speakers_count"`
	DurationMS      int64    `json:"duration_ms"`
	Segments        []struct {
		Text    string `json:"text"`
		StartMS int64  `json:"start_ms"`
	} `json:"segments"`
	Todos []struct {
		Content   string   `json:"content"`
		Executor  []string `json:"executor"`
		StartTime int64    `json:"start_time"`
	} `json:"todos"`
	Chapters []struct {
		Title     string `json:"title"`
		Summary   string `json:"summary"`
		StartTime int64  `json:"start_time"`
		EndTime   int64  `json:"end_time"`
	} `json:"chapters"`
}

func parseMinutesSummary(raw string) minutesDoc {
	var m minutesDoc
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

// fmtClockMS 毫秒 → mm:ss / h:mm:ss（导出文档内的时间轴格式）。
func fmtClockMS(ms int64) string {
	total := int64(0)
	if ms > 0 {
		total = ms / 1000
	}
	h := total / 3600
	m := (total % 3600) / 60
	sec := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func minutesMetaLines(m minutesDoc, createdAt string) []string {
	meta := []string{}
	if createdAt != "" {
		meta = append(meta, "- 时间："+createdAt)
	}
	if m.DurationMS > 0 {
		meta = append(meta, "- 时长："+fmtClockMS(m.DurationMS))
	}
	if m.SpeakersCount > 0 {
		meta = append(meta, fmt.Sprintf("- 说话人：%d 人", m.SpeakersCount))
	}
	if m.Sentences > 0 {
		meta = append(meta, fmt.Sprintf("- 句数：%d", m.Sentences))
	}
	if len(m.Features) > 0 {
		meta = append(meta, "- 功能："+strings.Join(m.Features, "、"))
	}
	return meta
}

func minutesTitleOf(m minutesDoc, t store.Task) string {
	if m.MinutesTitle != "" {
		return m.MinutesTitle
	}
	return "会议纪要"
}

// buildMinutesMarkdown 服务端 Markdown 组装（章节取舍由模板决定）。
func buildMinutesMarkdown(t store.Task, m minutesDoc, tpl minutesTemplate) string {
	title := minutesTitleOf(m, t)
	var out []string
	out = append(out, "# "+title, "")

	has := func(k string) bool {
		for _, s := range tpl.Sections {
			if s == k {
				return true
			}
		}
		return false
	}

	if has("meta") {
		if meta := minutesMetaLines(m, t.CreatedAt.Format("2006-01-02 15:04:05")); len(meta) > 0 {
			out = append(out, "## 会议信息")
			out = append(out, meta...)
			out = append(out, "")
		}
	}
	if has("summary") && strings.TrimSpace(m.SummaryText) != "" {
		out = append(out, "## 全文总结", "", strings.TrimSpace(m.SummaryText), "")
	}
	if has("todos") && len(m.Todos) > 0 {
		out = append(out, "## 待办事项", "", "| # | 待办 | 执行人 | 时间 |", "| --- | --- | --- | --- |")
		for i, td := range m.Todos {
			exec := "—"
			var valid []string
			for _, e := range td.Executor {
				if e != "" && e != "无" {
					valid = append(valid, e)
				}
			}
			if len(valid) > 0 {
				exec = strings.Join(valid, "、")
			}
			at := "—"
			if td.StartTime > 0 {
				at = fmtClockMS(td.StartTime)
			}
			out = append(out, fmt.Sprintf("| %d | %s | %s | %s |", i+1,
				strings.ReplaceAll(td.Content, "|", "\\|"), exec, at))
		}
		out = append(out, "")
	}
	if has("chapters") && len(m.Chapters) > 0 {
		out = append(out, "## 章节总结", "", "| 时间 | 章节 | 摘要 |", "| --- | --- | --- |")
		for _, ch := range m.Chapters {
			sum := strings.ReplaceAll(strings.ReplaceAll(ch.Summary, "|", "\\|"), "\n", " ")
			out = append(out, fmt.Sprintf("| %s – %s | %s | %s |",
				fmtClockMS(ch.StartTime), fmtClockMS(ch.EndTime),
				strings.ReplaceAll(ch.Title, "|", "\\|"), sum))
		}
		out = append(out, "")
	}
	if has("translation") && strings.TrimSpace(m.TranslationText) != "" {
		out = append(out, "## 翻译文本", "", strings.TrimSpace(m.TranslationText), "")
	}
	if has("transcript") && len(m.Segments) > 0 {
		out = append(out, "## 转写全文", "")
		for _, seg := range m.Segments {
			out = append(out, fmt.Sprintf("- `%s` %s", fmtClockMS(seg.StartMS), seg.Text))
		}
		out = append(out, "")
	}
	out = append(out, "---", "", fmt.Sprintf("由 voxbox 语音妙记生成 · 任务 %s", t.ID))
	return strings.Join(out, "\n")
}

// buildMinutesDocx 服务端 Word 组装（结构与 Markdown 模板一致）。
func buildMinutesDocx(t store.Task, m minutesDoc, tpl minutesTemplate) (*docx.Doc, error) {
	d := docx.New()
	title := minutesTitleOf(m, t)
	// Title 样式（styles.xml 中定义）
	d.Heading(0, title)

	has := func(k string) bool {
		for _, s := range tpl.Sections {
			if s == k {
				return true
			}
		}
		return false
	}

	if has("meta") {
		if meta := minutesMetaLines(m, t.CreatedAt.Format("2006-01-02 15:04:05")); len(meta) > 0 {
			d.Heading(1, "会议信息")
			for _, line := range meta {
				d.Paragraph(strings.TrimPrefix(line, "- "))
			}
		}
	}
	if has("summary") && strings.TrimSpace(m.SummaryText) != "" {
		d.Heading(1, "全文总结")
		d.Paragraph(strings.TrimSpace(m.SummaryText))
	}
	if has("todos") && len(m.Todos) > 0 {
		d.Heading(1, "待办事项")
		rows := make([][]string, 0, len(m.Todos))
		for i, td := range m.Todos {
			var valid []string
			for _, e := range td.Executor {
				if e != "" && e != "无" {
					valid = append(valid, e)
				}
			}
			exec := strings.Join(valid, "、")
			if exec == "" {
				exec = "—"
			}
			at := "—"
			if td.StartTime > 0 {
				at = fmtClockMS(td.StartTime)
			}
			rows = append(rows, []string{fmt.Sprint(i + 1), td.Content, exec, at})
		}
		d.Table([]string{"#", "待办", "执行人", "时间"}, rows)
	}
	if has("chapters") && len(m.Chapters) > 0 {
		d.Heading(1, "章节总结")
		rows := make([][]string, 0, len(m.Chapters))
		for _, ch := range m.Chapters {
			rows = append(rows, []string{
				fmtClockMS(ch.StartTime) + " – " + fmtClockMS(ch.EndTime),
				ch.Title, ch.Summary,
			})
		}
		d.Table([]string{"时间", "章节", "摘要"}, rows)
	}
	if has("translation") && strings.TrimSpace(m.TranslationText) != "" {
		d.Heading(1, "翻译文本")
		d.Paragraph(strings.TrimSpace(m.TranslationText))
	}
	if has("transcript") && len(m.Segments) > 0 {
		d.Heading(1, "转写全文")
		for _, seg := range m.Segments {
			d.Paragraph(fmt.Sprintf("[%s] %s", fmtClockMS(seg.StartMS), seg.Text))
		}
	}
	d.Paragraph("由 voxbox 语音妙记生成 · 任务 " + t.ID)
	return d, nil
}

func (s *Server) listMinutesTemplates(c *gin.Context) {
	ok(c, minutesTemplates)
}

// exportMinutes 妙记纪要导出：GET /api/minutes/:id/export?format=markdown|docx&template=standard
// 二进制/文本文件端点，不套 JSON 包络；业务错误走包络。
func (s *Server) exportMinutes(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	if t.Tool != "minutes" {
		fail(c, CodeBadRequest, "仅妙记任务支持纪要导出")
		return
	}
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if t.Summary == "" {
		fail(c, CodeBadRequest, "该任务没有纪要结果，请先完成任务")
		return
	}
	tpl := minutesTemplateByID(c.Query("template"))
	m := parseMinutesSummary(t.Summary)

	title := minutesTitleOf(m, *t)
	for _, ch := range []string{"\\", "/", ":", "*", "?", "\"", "<", ">", "|"} {
		title = strings.ReplaceAll(title, ch, "")
	}
	if strings.TrimSpace(title) == "" {
		title = "妙记纪要"
	}
	shortID := t.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	switch strings.ToLower(c.Query("format")) {
	case "markdown", "md":
		name := fmt.Sprintf("%s_%s.md", title, shortID)
		c.Header("Content-Disposition", contentDispositionName(name))
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(buildMinutesMarkdown(*t, m, tpl)))
	case "docx":
		d, err := buildMinutesDocx(*t, m, tpl)
		if err != nil {
			failErr(c, err)
			return
		}
		data, err := d.Bytes()
		if err != nil {
			failErr(c, err)
			return
		}
		name := fmt.Sprintf("%s_%s.docx", title, shortID)
		c.Header("Content-Disposition", contentDispositionName(name))
		c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
	default:
		fail(c, CodeBadRequest, "format 仅支持 markdown | docx")
	}
}

// contentDispositionName 中文文件名走 RFC 5987 filename*，ASCII 回退防旧客户端不识别。
func contentDispositionName(name string) string {
	return `attachment; filename="export"; filename*=UTF-8''` + url.PathEscape(name)
}
