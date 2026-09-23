package provider

import (
	"context"
	"testing"
)

type fakeTool struct{}

func (fakeTool) Meta() ToolMeta { return ToolMeta{Provider: "p", Name: "t", Title: "T"} }
func (fakeTool) ParamSpecs() []ParamSpec {
	return []ParamSpec{{Key: "text", Label: "文本", Type: ParamText, Required: true}}
}
func (fakeTool) Run(context.Context, TaskInput, ProgressReporter) (TaskOutput, error) {
	return TaskOutput{}, nil
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeTool{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeTool{}); err == nil {
		t.Error("duplicate register should fail")
	}
	got, ok := r.Get("p", "t")
	if !ok || got.Meta().Name != "t" {
		t.Fatalf("Get = %v %v", got, ok)
	}
	if len(r.List()) != 1 {
		t.Errorf("List len = %d", len(r.List()))
	}
}

// TestRegistryReplaceHotSwap 验证 Replace 覆盖注册：热加载后 Get 返回新实例。
func TestRegistryReplaceHotSwap(t *testing.T) {
	r := NewRegistry()
	first := &fakeTool{}
	r.Replace(first)
	if _, ok := r.Get("p", "t"); !ok {
		t.Fatal("Replace should add missing tool")
	}
	second := &fakeTool{}
	r.Replace(second)
	got, _ := r.Get("p", "t")
	if got != Tool(second) {
		t.Error("Get should return the replaced instance")
	}
	if len(r.List()) != 1 {
		t.Errorf("List len = %d, want 1 (no duplicate)", len(r.List()))
	}
}
