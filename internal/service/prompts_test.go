package service

import (
	"context"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/prompts"
)

func TestPromptCRUDAndListMerge(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 校验：必填与上限
	if _, err := svc.CreatePrompt("u1", PromptInput{Category: "故事", Content: "正文"}); err == nil {
		t.Fatal("缺名称应报错")
	}
	if _, err := svc.CreatePrompt("u1", PromptInput{Name: "名称", Category: "故事", Content: " "}); err == nil {
		t.Fatal("缺正文应报错")
	}
	if _, err := svc.CreatePrompt("u1", PromptInput{Name: "名称", Category: "故事", Content: "正文", Kind: "other"}); err == nil {
		t.Fatal("非法用途应报错")
	}

	p, err := svc.CreatePrompt("u1", PromptInput{Name: "产品介绍", Category: "文案", Content: "介绍这款产品", Description: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	// Kind 缺省回落 generate
	if p.Kind != string(prompts.KindGenerate) {
		t.Fatalf("Kind = %s, want generate", p.Kind)
	}

	items, err := svc.ListPrompts("u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(prompts.Builtin())+1 {
		t.Fatalf("列表应合并内置+自定义 = %d, got %d", len(prompts.Builtin())+1, len(items))
	}
	if items[0].Source != "builtin" || items[len(items)-1].Source != "user" {
		t.Fatalf("内置应排前自定义排后: first=%+v last=%+v", items[0], items[len(items)-1])
	}

	// 更新与删除（本人）
	if _, err := svc.UpdatePrompt("u1", p.ID, PromptInput{Name: "改名", Category: "故事", Content: "新正文", Kind: "polish"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.db.GetPrompt(p.ID, "u1")
	if err != nil || got.Name != "改名" || got.Kind != "polish" {
		t.Fatalf("更新未生效: %+v, %v", got, err)
	}
	if err := svc.DeletePrompt("u1", p.ID); err != nil {
		t.Fatal(err)
	}
	// 他人看不到也删不掉
	if _, err := svc.db.GetPrompt(p.ID, "u2"); err == nil {
		t.Fatal("他人不应读到")
	}
	if err := svc.DeletePrompt("u2", p.ID); err == nil {
		t.Fatal("他人删除应报错")
	}
}

func TestResolveApply(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// 未知内置 key
	if _, err := svc.ResolveApply("u1", ApplyReq{Builtin: "no-such"}); err == nil {
		t.Fatal("未知内置 key 应报错")
	}
	// 未指定条目
	if _, err := svc.ResolveApply("u1", ApplyReq{}); err == nil {
		t.Fatal("未指定条目应报错")
	}
	// 生成类：空输入回落自拟主题，篇幅指令按档位追加
	r, err := svc.ResolveApply("u1", ApplyReq{Builtin: "idiom-story", Length: prompts.LengthShort})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.System, "朗读约束") || !strings.Contains(r.System, "150 字") {
		t.Fatalf("生成类系统提示应含朗读约束+篇幅: %s", r.System)
	}
	if r.Input != "请自拟一个合适的主题完成创作。" {
		t.Fatalf("空输入应回落自拟: %q", r.Input)
	}
	// 润色类：原文必填
	if _, err := svc.ResolveApply("u1", ApplyReq{Builtin: "polish-general"}); err == nil {
		t.Fatal("润色缺原文应报错")
	}
	r, err = svc.ResolveApply("u1", ApplyReq{Builtin: "polish-general", Input: "原文"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.System, "【篇幅】") {
		t.Fatalf("润色不应有篇幅指令: %s", r.System)
	}
	// 自定义条目走 id，且按用户隔离
	p, err := svc.CreatePrompt("u1", PromptInput{Name: "n", Category: "c", Content: "正文"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveApply("u2", ApplyReq{ID: p.ID}); err == nil {
		t.Fatal("他人条目不可用")
	}
	if r, err = svc.ResolveApply("u1", ApplyReq{ID: p.ID, Input: "主题"}); err != nil || !strings.Contains(r.System, "正文") {
		t.Fatalf("自定义条目应生效: %+v, %v", r, err)
	}
}

func TestStreamApplyNoCredential(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	r, err := svc.ResolveApply("u1", ApplyReq{Builtin: "idiom-story"})
	if err != nil {
		t.Fatal(err)
	}
	// 全未配置凭证：报哨兵错误（HTTP 层映射业务码 4）
	err = svc.StreamApply(context.Background(), r, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "API Key 未配置") {
		t.Fatalf("err = %v, want 凭证哨兵", err)
	}
}

func TestSaveAssistantDefault(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if err := svc.SaveAssistantDefault("qianwen:qwen3.8-max"); err != nil {
		t.Fatal(err)
	}
	if got := svc.AssistantDefault(); got != "qianwen:qwen3.8-max" {
		t.Fatalf("AssistantDefault = %q", got)
	}
	// 目录外模型拒绝
	if err := svc.SaveAssistantDefault("zhipu:gpt-4o"); err == nil {
		t.Fatal("目录外模型应拒绝")
	}
	if err := svc.SaveAssistantDefault("nonsense"); err == nil {
		t.Fatal("非法格式应拒绝")
	}
	// 空串恢复自动
	if err := svc.SaveAssistantDefault(""); err != nil {
		t.Fatal(err)
	}
	if got := svc.AssistantDefault(); got != "" {
		t.Fatalf("空串应恢复自动, got %q", got)
	}
}
