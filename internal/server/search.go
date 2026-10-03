package server

// 转写全文搜索：跨任务检索识别/妙记文本，命中带时间戳供前端回放跳转。

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
)

// searchTasks 转写全文搜索：LIKE 粗筛候选任务（summary/title/tags），解析后按标题/
// 标签/分句/总结文本精确命中提取片段（含时间戳，前端可跳到对应同步回放位置）。
func (s *Server) searchTasks(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		fail(c, CodeBadRequest, "参数错误：q 必填")
		return
	}
	tasks, err := s.svc.DB().SearchSucceededSummaries(q, 50, searchScopeUserID(principalFrom(c)))
	if err != nil {
		failErr(c, err)
		return
	}
	lower := strings.ToLower(q)
	type matchSeg struct {
		Text    string `json:"text"`
		StartMS int64  `json:"start_ms"`
		EndMS   int64  `json:"end_ms"`
	}
	type summaryJSON struct {
		Segments     []matchSeg `json:"segments"`
		SummaryText  string     `json:"summary_text"`
		MinutesTitle string     `json:"minutes_title"`
	}
	items := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		// 标题命中（如分离任务的「歌名 - 歌手」）排首位：无时间戳的整行匹配
		var matches []matchSeg
		if strings.Contains(strings.ToLower(t.Title), lower) {
			matches = append(matches, matchSeg{Text: t.Title})
		}
		// 标签命中（LIKE 粗筛含 tags 列）：整标签作为匹配片段展示
		var tagList []string
		_ = json.Unmarshal([]byte(t.Tags), &tagList)
		for _, tag := range tagList {
			if strings.Contains(strings.ToLower(tag), lower) {
				matches = append(matches, matchSeg{Text: "标签：" + tag})
				break
			}
		}
		var sv summaryJSON
		if json.Unmarshal([]byte(t.Summary), &sv) == nil {
			for _, seg := range sv.Segments {
				if strings.Contains(strings.ToLower(seg.Text), lower) {
					matches = append(matches, seg)
					if len(matches) == 5 {
						break
					}
				}
			}
			if len(matches) == 0 {
				// LIKE 粗筛命中但分句未命中：查总结文本/标题（如妙记 summary_text），
				// 都不中则本次为键名误命中，丢弃
				hay := sv.SummaryText
				if hay == "" {
					hay = sv.MinutesTitle
				}
				if bi := strings.Index(strings.ToLower(hay), lower); bi >= 0 {
					// 按 rune 提取上下文窗口（本项目语种下 ToLower 不改变 rune 数）
					runes := []rune(hay)
					rOff := len([]rune(strings.ToLower(hay)[:bi]))
					start, end := rOff-30, rOff+len([]rune(q))+60
					if start < 0 {
						start = 0
					}
					if end > len(runes) {
						end = len(runes)
					}
					matches = append(matches, matchSeg{Text: string(runes[start:end])})
				}
			}
		}
		if len(matches) == 0 {
			continue
		}
		items = append(items, gin.H{
			"task_id":    t.ID,
			"tool":       t.Tool,
			"title":      t.Title,
			"created_at": t.CreatedAt.Format("2006-01-02 15:04:05"),
			"matches":    matches,
		})
	}
	ok(c, gin.H{"items": items, "total": len(items)})
}
