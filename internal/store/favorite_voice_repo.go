package store

import (
	"errors"

	"gorm.io/gorm"
)

// 收藏音色的存取：弹框式音色选择器的收藏数据源，按人隔离（同提示词口径）。
// 音色元数据收藏时快照入库，列表不回源各平台；同 (user_id, provider, voice_id)
// 唯一，重复收藏幂等返回既有行。

// CreateFavoriteVoice 收藏音色。已存在时幂等返回既有行（isNew=false），不报错——
// 星标双击/并发收藏都不该变成用户可见的错误。
func (d *DB) CreateFavoriteVoice(f *FavoriteVoice) (existing *FavoriteVoice, isNew bool, err error) {
	q := d.gorm.Where("user_id = ? AND provider = ? AND voice_id = ?",
		f.UserID, f.Provider, f.VoiceID)
	var row FavoriteVoice
	if err := q.First(&row).Error; err == nil {
		return &row, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if err := d.gorm.Create(f).Error; err != nil {
		return nil, false, err
	}
	return f, true, nil
}

// DeleteFavoriteVoice 取消收藏：显式带 user_id 条件，防越权删他人收藏。
func (d *DB) DeleteFavoriteVoice(id uint, userID string) error {
	res := d.gorm.Where("id = ? AND user_id = ?", id, userID).Delete(&FavoriteVoice{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListFavoriteVoices 收藏列表：provider 空=全部平台，按收藏时间倒序，分页返回与总数。
// 同链复用：Find 后 Offset(-1) 取消分页偏移再 Count（与 glossary 同款口径）。
func (d *DB) ListFavoriteVoices(userID, provider string, size, offset int) ([]FavoriteVoice, int64, error) {
	q := d.gorm.Where("user_id = ?", userID)
	if provider != "" {
		q = q.Where("provider = ?", provider)
	}
	q = q.Order("created_at DESC, id DESC").Limit(size).Offset(offset)
	var rows []FavoriteVoice
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	var total int64
	if err := q.Offset(-1).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
