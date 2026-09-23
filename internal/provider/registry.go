package provider

import (
	"fmt"
	"sync"
)

// Registry 并发安全：凭证热加载在 HTTP handler 中替换工具，与任务引擎
// 取工具、工具列表查询并发读写，故所有访问都过 RWMutex。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func key(provider, name string) string { return provider + "." + name }

// Register 注册新工具，同 key 重复注册报错（启动路径防呆：同一工具被注册两次属编程错误）。
func (r *Registry) Register(t Tool) error {
	m := t.Meta()
	k := key(m.Provider, m.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[k]; exists {
		return fmt.Errorf("tool %s already registered", k)
	}
	r.tools[k] = t
	return nil
}

// Replace 覆盖注册：同 key 已存在则替换，不存在则新增。
// 供凭证热加载重注册使用——工具集不变，实例随最新配置重建，无需重启服务。
func (r *Registry) Replace(t Tool) {
	m := t.Meta()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[key(m.Provider, m.Name)] = t
}

func (r *Registry) Get(provider, name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[key(provider, name)]
	return t, ok
}

func (r *Registry) List() []ToolMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolMeta, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Meta())
	}
	return out
}
