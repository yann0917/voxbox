package workflow

import (
	"testing"

	"github.com/yann0917/voxbox/internal/service"
)

// 防漂移：registry 里每个可链工具都必须有端口表项，端口表里每个键都必须
// 是 registry 真实存在的工具——两头对齐，新工具注册后忘记登记在测试期暴露。
func TestPortsCoverRegistry(t *testing.T) {
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := svc.Registry()
	for _, meta := range reg.List() {
		if meta.Provider == "demo" {
			continue // 演示卡不计
		}
		spec := SpecOf(meta.Provider, meta.Name)
		if spec == nil {
			t.Errorf("工具 %s.%s 缺端口表项（画布无法使用，请在 ports.go 登记）", meta.Provider, meta.Name)
			continue
		}
		if !spec.Chainable {
			continue
		}
		for _, in := range spec.In {
			if in.Key == "" || in.Kind == "" {
				t.Errorf("工具 %s.%s 端口 %s 定义不完整", meta.Provider, meta.Name, in.Key)
			}
		}
		if len(spec.In) == 0 && len(spec.OutKinds) == 0 {
			t.Errorf("工具 %s.%s 声明可链但无任何端口", meta.Provider, meta.Name)
		}
	}
	// 反向：端口表键必须真实存在
	for key, spec := range ports {
		var providerName, toolName string
		for i := 0; i < len(key); i++ {
			if key[i] == '.' {
				providerName, toolName = key[:i], key[i+1:]
				break
			}
		}
		if providerName == "" {
			t.Errorf("端口表键 %s 非法", key)
			continue
		}
		if _, ok := reg.Get(providerName, toolName); !ok {
			t.Errorf("端口表键 %s 在 registry 不存在（工具已下线？清理 ports.go）", key)
		}
		if spec.Chainable && len(spec.OutKinds) == 0 && len(spec.In) == 0 {
			t.Errorf("端口表项 %s 可链但无端口", key)
		}
	}
}
