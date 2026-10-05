package zhipu

import (
	"testing"

	"github.com/yann0917/voxbox/internal/config"
)

// 未配置直接报未配置，不发请求。
func TestProbeZhipuUnconfigured(t *testing.T) {
	msg, ok := ProbeZhipu(&config.Config{})
	if ok || msg == "" {
		t.Errorf("未配置应 (false, 提示), got (%v, %q)", ok, msg)
	}
}
