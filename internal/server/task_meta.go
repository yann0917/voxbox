// task_meta.go 任务标题/标签编辑端点：标题手改后置 title_edited（ASR 完成时的自动
// 派生让位，见 task/engine.go）；标签以 JSON 数组字符串存 Task.Tags，历史搜索 LIKE
// 覆盖。属主把关与 speakers.go 同款：GetTask + canAccessTask（admin 可代改、本地
// 无主存量可改），行内条件更新二次防竞态。
package server

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/store"
)

const (
	metaTitleMaxRunes = 60 // 与 task 包 deriveTitle 的标题截断同量级
	metaTagMaxRunes   = 20
	metaTagMaxCount   = 10
)

func (s *Server) editTaskMeta(c *gin.Context) {
	var body struct {
		Title *string   `json:"title"`
		Tags  *[]string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if body.Title == nil && body.Tags == nil {
		fail(c, CodeBadRequest, "title 与 tags 至少提供一个")
		return
	}

	var titlePtr, tagsPtr *string
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			fail(c, CodeBadRequest, "标题不能为空")
			return
		}
		if utf8.RuneCountInString(title) > metaTitleMaxRunes {
			fail(c, CodeBadRequest, "标题过长（最多 60 字）")
			return
		}
		titlePtr = &title
	}
	if body.Tags != nil {
		tags := normalizeTags(*body.Tags)
		b, err := json.Marshal(tags)
		if err != nil {
			failErr(c, err)
			return
		}
		tagsPtr = new(string)
		*tagsPtr = string(b)
	}

	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	// 越权同报不存在（不泄露他人任务存在性），与 getTask/deleteTask 同款
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if err := s.svc.DB().UpdateTaskMeta(t.ID, t.UserID, titlePtr, tagsPtr); err != nil {
		failErr(c, err)
		return
	}

	// 回读返回最新 DTO：前端直接 patch 缓存，不用再发一次 GET
	updated, err := s.svc.DB().GetTask(t.ID)
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, toTaskDTO(*updated))
}

// normalizeTags 标签清洗：去空白、丢空项、按首现顺序去重；超长/超量的丢弃（个人
// 工具的容错口径——多打的标签默默忽略，不做整单报错打断）。
func normalizeTags(in []string) []string {
	seen := map[string]bool{}
	tags := make([]string, 0, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" || seen[tag] {
			continue
		}
		if utf8.RuneCountInString(tag) > metaTagMaxRunes || len(tags) >= metaTagMaxCount {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	return tags
}
