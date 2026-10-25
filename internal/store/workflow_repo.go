package store

import (
	"errors"

	"gorm.io/gorm"
)

// 工作流存取：用户编排图与运行记录，按人隔离（与提示词/收藏同口径）。
// 内置模板不入库（烤在 internal/workflow），run 的 WorkflowID=0 + WorkflowKey 溯源模板。

// CreateWorkflow 保存新的工作流编排。
func (d *DB) CreateWorkflow(w *Workflow) error { return d.gorm.Create(w).Error }

// GetWorkflow 取单条编排；未命中或非本人返回 ErrNotFound（越权与不存在同语义，不泄露存在性）。
func (d *DB) GetWorkflow(id uint, userID string) (*Workflow, error) {
	var row Workflow
	if err := d.gorm.Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// UpdateWorkflow 覆盖名称/描述/图。仅本人可改。
func (d *DB) UpdateWorkflow(w *Workflow) error {
	res := d.gorm.Model(&Workflow{}).Where("id = ? AND user_id = ?", w.ID, w.UserID).
		Updates(map[string]any{"name": w.Name, "description": w.Description, "graph": w.Graph})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWorkflow 软删编排（运行记录保留，历史不随之消失）。
func (d *DB) DeleteWorkflow(id uint, userID string) error {
	res := d.gorm.Where("id = ? AND user_id = ?", id, userID).Delete(&Workflow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListWorkflows 用户编排列表，最近编辑在前。
func (d *DB) ListWorkflows(userID string) ([]Workflow, error) {
	var rows []Workflow
	err := d.gorm.Where("user_id = ?", userID).Order("updated_at DESC, id DESC").Find(&rows).Error
	return rows, err
}

// CreateWorkflowRun 落一条运行记录（执行前创建，执行器单写者逐步更新）。
func (d *DB) CreateWorkflowRun(r *WorkflowRun) error { return d.gorm.Create(r).Error }

// UpdateWorkflowRun 覆盖运行记录的可变字段（status/inputs/nodes/error）。
// 仅执行器与取消路径写入；Where status=in(running,pending) 供取消竞争兜底，
// 终态后的迟到更新直接 0 行返回 ErrNotFound 由调用方忽略。
func (d *DB) UpdateWorkflowRun(r *WorkflowRun, onlyIfActive bool) error {
	q := d.gorm.Model(&WorkflowRun{}).Where("id = ?", r.ID)
	if onlyIfActive {
		q = q.Where("status IN ?", []string{"pending", "running"})
	}
	res := q.Updates(map[string]any{
		"status": r.Status, "inputs": r.Inputs, "nodes": r.Nodes, "error": r.Error,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetWorkflowRun 取运行详情。
func (d *DB) GetWorkflowRun(id uint, userID string) (*WorkflowRun, error) {
	var row WorkflowRun
	if err := d.gorm.Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// GetWorkflowRunByID 不带归属条件（执行器 goroutine 按 id 推进用；id 为服务端生成不可伪造）。
func (d *DB) GetWorkflowRunByID(id uint) (*WorkflowRun, error) {
	var row WorkflowRun
	if err := d.gorm.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// ListWorkflowRuns 运行列表：workflowID=0 时含全部（含模板直跑），最近优先。
func (d *DB) ListWorkflowRuns(userID string, workflowID uint, size, offset int) ([]WorkflowRun, int64, error) {
	q := d.gorm.Where("user_id = ?", userID)
	if workflowID > 0 {
		q = q.Where("workflow_id = ?", workflowID)
	}
	q = q.Order("created_at DESC, id DESC").Limit(size).Offset(offset)
	var rows []WorkflowRun
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	var total int64
	if err := q.Offset(-1).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
