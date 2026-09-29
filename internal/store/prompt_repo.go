package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// 提示词库用户条目的存取。所有查询都带 user_id 条件：提示词始终按人隔离，
// admin 不例外（与任务的 admin 全量可见口径不同——提示词是个人创作素材）。

func (d *DB) CreatePrompt(p *Prompt) error {
	return d.gorm.Create(p).Error
}

// UpdatePrompt 显式带 user_id 条件按列更新：不用 Save（按主键整行覆盖），
// 防止调用方传入被篡改 owner 的行时越权改写。
func (d *DB) UpdatePrompt(p *Prompt) error {
	res := d.gorm.Model(&Prompt{}).
		Where("id = ? AND user_id = ?", p.ID, p.UserID).
		Updates(map[string]any{
			"name": p.Name, "category": p.Category, "description": p.Description,
			"content": p.Content, "kind": p.Kind, "updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) GetPrompt(id uint, userID string) (*Prompt, error) {
	var p Prompt
	if err := d.gorm.First(&p, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (d *DB) DeletePrompt(id uint, userID string) error {
	res := d.gorm.Where("id = ? AND user_id = ?", id, userID).Delete(&Prompt{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) ListPrompts(userID string) ([]Prompt, error) {
	var items []Prompt
	err := d.gorm.Where("user_id = ?", userID).Order("updated_at DESC").Find(&items).Error
	return items, err
}
