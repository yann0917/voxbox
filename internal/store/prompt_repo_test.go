package store

import "testing"

func TestPromptCRUDAndIsolation(t *testing.T) {
	db := openTest(t)
	a := &Prompt{UserID: "user-a", Name: "我的提示词", Category: "故事", Content: "写一个故事", Kind: "generate"}
	if err := db.CreatePrompt(a); err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 {
		t.Fatal("自增主键应回填")
	}

	// 本人可读可改可删
	got, err := db.GetPrompt(a.ID, "user-a")
	if err != nil || got.Name != "我的提示词" {
		t.Fatalf("GetPrompt = %+v, %v", got, err)
	}
	got.Description = "说明"
	if err := db.UpdatePrompt(got); err != nil {
		t.Fatal(err)
	}

	// 他人不可见：读/改/删都按 user_id 过滤
	if _, err := db.GetPrompt(a.ID, "user-b"); err != ErrNotFound {
		t.Fatalf("他人读取应 ErrNotFound, got %v", err)
	}
	foreign := &Prompt{ID: a.ID, UserID: "user-b", Name: "篡改", Category: "x", Content: "y", Kind: "generate"}
	if err := db.UpdatePrompt(foreign); err != ErrNotFound {
		t.Fatalf("他人更新应 ErrNotFound, got %v", err)
	}
	if got, _ := db.GetPrompt(a.ID, "user-a"); got.Name != "我的提示词" || got.Description != "说明" {
		t.Fatalf("他人更新不应改动原行: %+v", got)
	}
	if err := db.DeletePrompt(a.ID, "user-b"); err != ErrNotFound {
		t.Fatalf("他人删除应 ErrNotFound, got %v", err)
	}

	items, err := db.ListPrompts("user-a")
	if err != nil || len(items) != 1 || items[0].Name != "我的提示词" {
		t.Fatalf("ListPrompts = %+v, %v", items, err)
	}
	if err := db.DeletePrompt(a.ID, "user-a"); err != nil {
		t.Fatal(err)
	}
	if items, _ = db.ListPrompts("user-a"); len(items) != 0 {
		t.Fatal("删除后列表应为空")
	}
}
