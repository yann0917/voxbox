package workflow

import (
	"testing"

	"github.com/yann0917/voxbox/internal/service"
)

// 内置模板必须通过真实 registry 的完整校验（工具存在/端口 kind/必填参数齐备），
// 模板引用的工具或参数漂移在此暴露。
func TestTemplatesValidate(t *testing.T) {
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range Templates {
		if err := tpl.Graph.ValidateWithTools(svc.Registry()); err != nil {
			t.Errorf("模板 %s 校验失败: %v", tpl.Key, err)
		}
	}
}
