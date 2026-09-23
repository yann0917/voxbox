// Package store 提供 SQLite（gorm）持久化：任务与产物。
package store

import (
	"errors"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var ErrNotFound = errors.New("record not found")

type DB struct{ gorm *gorm.DB }

func Open(path string) (*DB, error) {
	g, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := g.AutoMigrate(&Task{}, &Artifact{}, &User{}, &Session{}); err != nil {
		return nil, err
	}
	d := &DB{gorm: g}
	if err := d.markRunningAsInterrupted(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) markRunningAsInterrupted() error {
	return d.gorm.Model(&Task{}).
		Where("status = ?", StatusRunning).
		Update("status", StatusInterrupted).Error
}
