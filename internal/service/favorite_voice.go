package service

import (
	"errors"
	"strings"

	"github.com/yann0917/voxbox/internal/store"
)

// 收藏音色：弹框式音色选择器的收藏数据源（按平台分类、按人隔离）。
// 校验在此归一，store 只管存取。

// favVoiceProviderMaxLen 平台标识与音色 ID 的长度上限（voice_id 含 moss_audio uuid
// 与复刻音色 ID，128 足够）。
const favVoiceFieldCap = 128

// ListFavoriteVoices 收藏列表（分页 + 可选平台筛选）。
func (s *Service) ListFavoriteVoices(userID, provider string, size, offset int) ([]store.FavoriteVoice, int64, error) {
	return s.db.ListFavoriteVoices(userID, provider, size, offset)
}

// FavoriteVoiceInput 收藏请求载荷。
type FavoriteVoiceInput struct {
	Provider string
	VoiceID  string
	Name     string
	Label    string
	Lang     string
}

// AddFavoriteVoice 收藏音色（幂等：已收藏返回既有行与 isNew=false）。
func (s *Service) AddFavoriteVoice(userID string, in FavoriteVoiceInput) (*store.FavoriteVoice, bool, error) {
	in.Provider = strings.TrimSpace(in.Provider)
	in.VoiceID = strings.TrimSpace(in.VoiceID)
	if in.Provider == "" || in.VoiceID == "" {
		return nil, false, errors.New("缺少必填参数: provider / voice_id")
	}
	row := &store.FavoriteVoice{
		UserID:   userID,
		Provider: truncateRunes(in.Provider, favVoiceFieldCap),
		VoiceID:  truncateRunes(in.VoiceID, favVoiceFieldCap),
		Name:     truncateRunes(strings.TrimSpace(in.Name), favVoiceFieldCap),
		Label:    truncateRunes(strings.TrimSpace(in.Label), 255),
		Lang:     truncateRunes(strings.TrimSpace(in.Lang), 32),
	}
	return s.db.CreateFavoriteVoice(row)
}

// RemoveFavoriteVoice 取消收藏；不存在（已删/他人收藏）按 ErrNotFound 回 404 语义。
func (s *Service) RemoveFavoriteVoice(userID string, id uint) error {
	return s.db.DeleteFavoriteVoice(id, userID)
}

// truncateRunes 按 rune 截断到 n。
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
