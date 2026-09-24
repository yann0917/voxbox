package service

import (
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// 全部卡声明合法（Validate 防呆）+ 云端卡名唯一 + 必要卡在场。
func TestProviderCardsValid(t *testing.T) {
	cards := providerCards()
	seen := map[string]bool{}
	for _, c := range cards {
		if err := c.Validate(); err != nil {
			t.Errorf("卡 %s 声明非法: %v", c.Name, err)
		}
		if seen[c.Name] {
			t.Errorf("卡名重复: %s", c.Name)
		}
		seen[c.Name] = true
	}
	for _, want := range []string{"volcengine", "mediakit", "mvsep", "qianwen", "xiaomi", "audiotool", "gsgc"} {
		if !seen[want] {
			t.Errorf("缺少卡: %s", want)
		}
	}
	// 小米卡描述须含官方控制台域名（前端把文案里的域名渲染成可点击跳转链接）
	for _, c := range cards {
		if c.Name == "xiaomi" && !strings.Contains(c.Description, "platform.xiaomimimo.com") {
			t.Errorf("小米卡描述应含 platform.xiaomimimo.com: %q", c.Description)
		}
	}
	// 卡的字段声明必须与 cardFieldValues 的键完全对齐
	vals := cardFieldValues(&config.Config{})
	for _, c := range cards {
		if c.Kind != provider.KindCloud {
			continue
		}
		m := vals[c.Name]
		for _, f := range c.Fields {
			if m == nil {
				t.Errorf("cardFieldValues 缺卡 %s", c.Name)
				break
			}
			if _, ok := m[f.Key]; !ok {
				t.Errorf("cardFieldValues[%s] 缺字段 %s", c.Name, f.Key)
			}
		}
	}
}

// configured 判定：volcengine 两种凭证组合任一可用；单 secret 卡看 secret。
func TestCardConfigured(t *testing.T) {
	if !cardConfigured("volcengine", map[string]string{"app_id": "a", "access_token": "t"}) {
		t.Error("APP ID+Token 组合应视为已配置")
	}
	if !cardConfigured("volcengine", map[string]string{"api_key": "k"}) {
		t.Error("单 API Key 应视为已配置")
	}
	if cardConfigured("volcengine", map[string]string{"app_id": "a"}) {
		t.Error("只有 APP ID 不应视为已配置")
	}
	if !cardConfigured("qianwen", map[string]string{"api_key": "k"}) {
		t.Error("千问配 key 应视为已配置")
	}
	if cardConfigured("qianwen", map[string]string{}) {
		t.Error("千问空 key 不应视为已配置")
	}
	if !cardConfigured("xiaomi", map[string]string{"api_key": "k"}) {
		t.Error("小米配 key 应视为已配置")
	}
	if cardConfigured("xiaomi", map[string]string{}) {
		t.Error("小米空 key 不应视为已配置")
	}
}

// ProviderStates：secret 只出 has_value、text 出 value、本地卡带 tools_count、configured 正确。
func TestProviderStates(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	states := svc.ProviderStates(svc.Config())
	var volc, qwen, audio *ProviderState
	for i := range states {
		switch states[i].Name {
		case "volcengine":
			volc = &states[i]
		case "qianwen":
			qwen = &states[i]
		case "audiotool":
			audio = &states[i]
		}
	}
	if volc == nil || qwen == nil || audio == nil {
		t.Fatalf("缺少期望的卡: %+v", states)
	}
	if volc.Configured {
		t.Error("空凭证 volcengine 不应 configured")
	}
	for _, f := range volc.Fields {
		if f.Key == "app_id" && f.Value != "" {
			t.Error("app_id 应为 text 且值可回显（空配置下为空串）")
		}
		if f.Key == "access_token" && f.HasValue {
			t.Error("未配置 access_token 时 has_value 应为 false")
		}
	}
	if qwen.Configured {
		t.Error("空凭证 qianwen 不应 configured")
	}
	if audio.Kind != provider.KindLocal || audio.ToolsCount < 10 {
		t.Errorf("audiotool 本地卡 tools_count = %d", audio.ToolsCount)
	}
}
