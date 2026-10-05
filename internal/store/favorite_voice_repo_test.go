package store

import (
	"errors"
	"testing"
)

// 收藏音色 repo 测试：幂等收藏、按人隔离、平台筛选、分页与移除。

func TestFavoriteVoiceCRUD(t *testing.T) {
	db := openTest(t)
	add := func(userID, provider, voiceID string) (*FavoriteVoice, bool) {
		row, isNew, err := db.CreateFavoriteVoice(&FavoriteVoice{
			UserID: userID, Provider: provider, VoiceID: voiceID,
			Name: "音色" + voiceID, Label: "音色" + voiceID, Lang: "中文",
		})
		if err != nil {
			t.Fatalf("收藏 %s/%s: %v", provider, voiceID, err)
		}
		return row, isNew
	}

	// 幂等：重复收藏返回既有行
	row1, isNew := add("u1", "minimax", "male-qn-qingse")
	if !isNew {
		t.Fatal("首次收藏 isNew 应为 true")
	}
	row2, isNew := add("u1", "minimax", "male-qn-qingse")
	if isNew || row2.ID != row1.ID {
		t.Fatalf("重复收藏应幂等: isNew=%v row=%+v", isNew, row2)
	}

	// 跨平台同 ID 不冲突；跨用户隔离
	add("u1", "volcengine", "male-qn-qingse")
	add("u2", "minimax", "male-qn-qingse")

	// 全量列表（u1 应有 2 条，时间倒序）
	items, total, err := db.ListFavoriteVoices("u1", "", 20, 0)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("全量列表 = %d/%d err=%v", len(items), total, err)
	}
	// 平台筛选
	items, total, _ = db.ListFavoriteVoices("u1", "minimax", 20, 0)
	if total != 1 || items[0].Provider != "minimax" {
		t.Fatalf("平台筛选 = %+v/%d", items, total)
	}
	// 分页：size=1 第 2 页
	items, total, _ = db.ListFavoriteVoices("u1", "", 1, 1)
	if total != 2 || len(items) != 1 {
		t.Fatalf("分页 = %d/%d", len(items), total)
	}

	// 星标引用对（按平台，弹框取消收藏用行 ID 定位）
	rows, _, err := db.ListFavoriteVoices("u1", "minimax", 500, 0)
	if err != nil || len(rows) != 1 || rows[0].VoiceID != "male-qn-qingse" {
		t.Fatalf("refs = %+v err=%v", rows, err)
	}

	// 移除：本人可删，他人/不存在回 ErrNotFound
	if err := db.DeleteFavoriteVoice(row1.ID, "u2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("他人删除应 ErrNotFound, got %v", err)
	}
	if err := db.DeleteFavoriteVoice(row1.ID, "u1"); err != nil {
		t.Fatalf("本人删除: %v", err)
	}
	if err := db.DeleteFavoriteVoice(row1.ID, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应 ErrNotFound, got %v", err)
	}
	if _, total, _ := db.ListFavoriteVoices("u1", "", 20, 0); total != 1 {
		t.Fatalf("移除后应剩 1 条, got %d", total)
	}
}
