package mvsep

import (
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// ProviderCard MVSep 凭证卡。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "mvsep",
		Title:       "MVSep · 音频源分离",
		Description: "MVSep（mvsep.com）音乐源分离：人声/伴奏、鼓、贝斯等 120+ 算法，注册账号每天 50 次免费。",
		Kind:        provider.KindCloud,
		Order:       30,
		Fields: []provider.CredentialField{
			{Key: "api_token", Label: "API Token", Kind: provider.FieldSecret, Required: true,
				ConfigKey: "mvsep.api_token", Placeholder: "mvsep.com 全页 API 页获取"},
			{Key: "base_url", Label: "接入线路", Kind: provider.FieldSelect, ConfigKey: "mvsep.base_url",
				Options: []provider.ParamOption{
					{Value: "", Label: "主站（自动就近）"},
					{Value: "https://hk.mvsep.com", Label: "香港 hk.mvsep.com"},
					{Value: "https://de.mvsep.com", Label: "德国 de.mvsep.com"},
					{Value: "https://de2.mvsep.com", Label: "德国 2 de2.mvsep.com"},
				},
				Hint: "同一任务只能由接单节点出结果，须全程固定线路"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{"api_token": cfg.MVSep.APIToken, "base_url": cfg.MVSep.BaseURL}
		},
		// api_token 是 secret：留空=不修改；base_url 是 select 字段：空串=主站（合法取值），不设守卫。
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["api_token"]; ok && v != "" {
				nc.MVSep.APIToken = v
			}
			if v, ok := fields["base_url"]; ok {
				nc.MVSep.BaseURL = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.MVSep = src.MVSep },
		Test:       ProbeMVSep,
	}
}
