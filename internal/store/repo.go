package store

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

func (d *DB) CreateTask(t *Task) error {
	return d.gorm.Create(t).Error
}

func (d *DB) UpdateTask(t *Task) error {
	return d.gorm.Save(t).Error
}

// UpdateTaskSummary 只改 summary 列（id+user_id 双条件：handler 已过属主校验，
// 行内条件是竞态窗口的二次把关——被删/易主则 0 行受影响报 ErrNotFound）。
// 说话人改名端点与后续加工层共用；不走 Save 全列覆写，避免踩掉并发的进展更新。
func (d *DB) UpdateTaskSummary(taskID, userID, summaryJSON string) error {
	res := d.gorm.Model(&Task{}).
		Where("id = ? AND user_id = ?", taskID, userID).
		Updates(map[string]any{"summary": summaryJSON})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateTaskMeta 条件更新标题/标签（id+user_id 双条件同 UpdateTaskSummary：竞态窗口
// 的属主二次把关）。title 提供时同时置 title_edited——ASR 完成时的自动派生标题据此让位；
// tags 是序列化好的 JSON 数组字符串，两端只想改其一就只传其一。
func (d *DB) UpdateTaskMeta(taskID, userID string, title, tags *string) error {
	set := map[string]any{}
	if title != nil {
		set["title"] = *title
		set["title_edited"] = true
	}
	if tags != nil {
		set["tags"] = *tags
	}
	if len(set) == 0 {
		return ErrNotFound
	}
	res := d.gorm.Model(&Task{}).
		Where("id = ? AND user_id = ?", taskID, userID).
		Updates(set)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) GetTask(id string) (*Task, error) {
	var t Task
	if err := d.gorm.First(&t, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// ListTasks 按 provider/tool/status 过滤分页；userID 为空=全部（admin/本地模式），
// 非 admin 传自己的 id 只看自己的任务（含本地历史的空 owner 行不可见）。
func (d *DB) ListTasks(provider, tool string, statuses []TaskStatus, limit, offset int, userID string) ([]Task, int64, error) {
	q := d.gorm.Model(&Task{})
	if provider != "" {
		q = q.Where("provider = ?", provider)
	}
	if tool != "" {
		q = q.Where("tool = ?", tool)
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	var items []Task
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&items).Error
	return items, total, err
}

// SearchSucceededSummaries 全文搜索粗筛：summary（转写/总结内容）、title（任务标题，
// 如分离任务的「歌名 - 歌手」）或 tags（用户标签）LIKE 命中的成功任务，按创建时间倒序。
// LIKE 只做候选集粗筛（转义 %/_/\\），精确命中与片段提取由上层解析后判定；
// 个人工具量级（千级任务）全表 LIKE 足够，量大再上 FTS5。
func (d *DB) SearchSucceededSummaries(keyword string, limit int, userID string) ([]Task, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + escapeLike(keyword) + "%"
	q := d.gorm.Model(&Task{}).
		Where("status = ? AND (summary LIKE ? ESCAPE '\\' OR title LIKE ? ESCAPE '\\' OR tags LIKE ? ESCAPE '\\')",
			StatusSucceeded, pattern, pattern, pattern)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	var items []Task
	err := q.Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	return strings.ReplaceAll(s, "_", "\\_")
}

// DeleteTask 软删除任务（gorm DeletedAt 约定，所有查询自动过滤）：
// 产物行与磁盘文件保留——误删可整体恢复，将来「彻底清除」走 Unscoped + 文件 GC。
func (d *DB) DeleteTask(id string) error {
	res := d.gorm.Delete(&Task{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) CreateArtifact(a *Artifact) error {
	return d.gorm.Create(a).Error
}

func (d *DB) ListArtifacts(taskID string) ([]Artifact, error) {
	var items []Artifact
	err := d.gorm.Order("created_at ASC").Find(&items, "task_id = ?", taskID).Error
	return items, err
}

func (d *DB) GetArtifact(id string) (*Artifact, error) {
	var a Artifact
	if err := d.gorm.First(&a, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}
