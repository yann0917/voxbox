package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// CreateUser 新建账号（用户名唯一约束冲突报错原样上抛，由调用方转可读文案）。
func (d *DB) CreateUser(u *User) error {
	return d.gorm.Create(u).Error
}

func (d *DB) CountUsers() (int64, error) {
	var n int64
	err := d.gorm.Model(&User{}).Count(&n).Error
	return n, err
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	var u User
	if err := d.gorm.Where("username = ?", username).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (d *DB) GetUserByID(id string) (*User, error) {
	var u User
	if err := d.gorm.First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByTokenHash API token 鉴权查找（库里只有 sha256，明文比对在调用方做哈希）。
func (d *DB) GetUserByTokenHash(hash string) (*User, error) {
	var u User
	if err := d.gorm.First(&u, "api_token_hash = ?", hash).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// UpdateUserPassword 改密并按 mustChange 决定是否保留强制改密标记。
func (d *DB) UpdateUserPassword(id, hash string, mustChange bool) error {
	return d.gorm.Model(&User{}).Where("id = ?", id).
		Updates(map[string]any{"password_hash": hash, "must_change_password": mustChange}).Error
}

// UpdateUserToken 轮换 API token（hash 为空表示清除为 NULL——空串会撞唯一索引）。
func (d *DB) UpdateUserToken(id, tokenHash string) error {
	val := any(nil)
	issued := time.Time{}
	if tokenHash != "" {
		val = tokenHash
		issued = time.Now()
	}
	return d.gorm.Model(&User{}).Where("id = ?", id).
		Updates(map[string]any{"api_token_hash": val, "token_issued_at": issued}).Error
}

func (d *DB) CreateSession(sess *Session) error {
	return d.gorm.Create(sess).Error
}

// GetSession 命中且未过期才返回；过期行顺带清除。
func (d *DB) GetSession(token string) (*Session, error) {
	var s Session
	if err := d.gorm.First(&s, "token = ?", token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if time.Now().After(s.ExpiresAt) {
		_ = d.gorm.Delete(&Session{}, "token = ?", token).Error
		return nil, ErrNotFound
	}
	return &s, nil
}

func (d *DB) DeleteSession(token string) error {
	return d.gorm.Delete(&Session{}, "token = ?", token).Error
}

// DeleteUserSessions 改密后全端下线（含当前会话，重新登录即可）。
func (d *DB) DeleteUserSessions(userID string) error {
	return d.gorm.Delete(&Session{}, "user_id = ?", userID).Error
}

func (d *DB) CleanExpiredSessions() error {
	return d.gorm.Delete(&Session{}, "expires_at < ?", time.Now()).Error
}
