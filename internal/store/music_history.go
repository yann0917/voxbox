// 收听历史（音乐播放器）：按用户隔离、按曲聚合。重复收听次数 +1 并置顶，
// 元数据跟随最新（换封面/改名后展示不滞后）。永久保留不做裁剪。
package store

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MusicPlayHistory 收听历史聚合表：(user_id, source, rid) 唯一。
type MusicPlayHistory struct {
	UserID    string    `gorm:"primaryKey;size:64" json:"-"`
	Source    string    `gorm:"primaryKey;size:32" json:"source"`
	Rid       string    `gorm:"primaryKey;size:128" json:"rid"`
	Name      string    `gorm:"size:256" json:"name"`
	Artist    string    `gorm:"size:256" json:"artist"`
	Album     string    `gorm:"size:256" json:"album"`
	Pic       string    `gorm:"type:text" json:"pic,omitempty"`
	Duration  int64     `json:"duration"`   // 毫秒
	PlayCount int       `json:"play_count"` // 累计收听次数
	PlayedAt  time.Time `json:"played_at"`  // 最近一次收听
	CreatedAt time.Time `json:"created_at"` // 首次收听
}

// AddMusicPlayHistory 记一次收听：聚合表 upsert（次数 +1、置顶、元数据跟随最新）。
// 无业务键静默忽略——历史记录失败绝不影响播放主链路。
func (d *DB) AddMusicPlayHistory(userID string, it MusicPlayHistory) error {
	it.Source = strings.TrimSpace(it.Source)
	it.Rid = strings.TrimSpace(it.Rid)
	if it.Source == "" || it.Rid == "" {
		return nil
	}
	now := time.Now()
	it.UserID = userID
	it.PlayedAt = now
	it.PlayCount = 1
	return d.gorm.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "source"}, {Name: "rid"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name":       it.Name,
			"artist":     it.Artist,
			"album":      it.Album,
			"pic":        it.Pic,
			"duration":   it.Duration,
			"played_at":  now,
			"play_count": gorm.Expr("play_count + 1"),
		}),
	}).Create(&it).Error
}

// ListMusicPlayHistory 某用户收听历史，按最近收听倒序分页（page 从 1 起，size 钳 [1,200]）。
func (d *DB) ListMusicPlayHistory(userID string, page, size int) ([]MusicPlayHistory, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int64
	if err := d.gorm.Model(&MusicPlayHistory{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []MusicPlayHistory
	err := d.gorm.Where("user_id = ?", userID).
		Order("played_at DESC").Limit(size).Offset((page - 1) * size).Find(&items).Error
	return items, total, err
}

// DeleteMusicPlayHistory 删除某用户一条收听历史。
func (d *DB) DeleteMusicPlayHistory(userID, source, rid string) error {
	return d.gorm.Where("user_id = ? AND source = ? AND rid = ?", userID, source, rid).
		Delete(&MusicPlayHistory{}).Error
}
