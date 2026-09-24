// 设置页「云端服务/本地环境」两分组的卡片装配：声明来自各 provider 包，
// 当前值与已配置状态来自配置快照，工具数来自注册表。
package service

import (
	"sort"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
	"github.com/yann0917/voxbox/internal/provider/gsgc"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
)

// providerCards 全部卡声明（按 Order 排序）。新增厂商 = 包内声明 + 此处加一行。
func providerCards() []provider.ProviderInfo {
	cards := []provider.ProviderInfo{
		volcengine.ProviderCard(),
		volcengine.MediaKitCard(),
		mvsep.ProviderCard(),
		qianwen.ProviderCard(),
		xiaomi.ProviderCard(),
		zhipu.ProviderCard(),
		audiotool.ProviderCard(),
		gsgc.ProviderCard(),
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Order < cards[j].Order })
	return cards
}

// cardFieldValues 各卡字段的当前值（来自配置快照）。键必须与卡声明 Fields 完全对齐
// （cards_test 防呆）；secret 值只用于 configured/has_value 判定，不出 HTTP 响应。
func cardFieldValues(cfg *config.Config) map[string]map[string]string {
	return map[string]map[string]string{
		"volcengine": {
			"app_id":       cfg.Volc.Speech.AppID,
			"access_token": cfg.Volc.Speech.AccessToken,
			"api_key":      cfg.Volc.Speech.APIKey,
		},
		"mediakit": {"api_key": cfg.Volc.MediaKit.APIKey},
		"mvsep":    {"api_token": cfg.MVSep.APIToken, "base_url": cfg.MVSep.BaseURL},
		"qianwen":  {"api_key": cfg.Qianwen.APIKey},
		"xiaomi":   {"api_key": cfg.Xiaomi.APIKey},
		"zhipu":    {"api_key": cfg.Zhipu.APIKey},
	}
}

// cardConfigured 卡级「已配置」判定，沿用各卡现口径。
func cardConfigured(name string, vals map[string]string) bool {
	switch name {
	case "volcengine":
		return (vals["app_id"] != "" && vals["access_token"] != "") || vals["api_key"] != ""
	case "mediakit", "qianwen", "xiaomi", "zhipu":
		return vals["api_key"] != ""
	case "mvsep":
		return vals["api_token"] != ""
	}
	return false
}

// cardToolProvider 卡名 → 注册表 provider 名。个别卡与工具注册名历史不一致：
// 本地音频剪辑卡叫 audiotool，其 12 个工具的 ToolMeta.Provider 却是 "audio"，
// 按注册名取数才能让本地卡展示真实工具数；其余卡同名无需登记。
var cardToolProvider = map[string]string{
	"audiotool": "audio",
}

// FieldState 卡字段的 HTTP 形态：text/select 回 value，secret 只回 has_value。
type FieldState struct {
	Key         string                 `json:"key"`
	Label       string                 `json:"label"`
	Kind        provider.FieldKind     `json:"kind"`
	Value       string                 `json:"value,omitempty"`
	HasValue    bool                   `json:"has_value,omitempty"`
	Options     []provider.ParamOption `json:"options,omitempty"`
	Placeholder string                 `json:"placeholder,omitempty"`
	Hint        string                 `json:"hint,omitempty"`
	Required    bool                   `json:"required,omitempty"`
}

// ProviderState 卡的 HTTP 形态（GET /api/settings 的 providers 数组元素）。
type ProviderState struct {
	Name        string                `json:"name"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Kind        provider.ProviderKind `json:"kind"`
	Order       int                   `json:"order"`
	Configured  bool                  `json:"configured"`
	ToolsCount  int                   `json:"tools_count"`
	Fields      []FieldState          `json:"fields"`
}

// ProviderStates 卡声明 + 配置快照 + 注册表工具计数 → HTTP 形态。
func (s *Service) ProviderStates(cfg *config.Config) []ProviderState {
	counts := map[string]int{}
	for _, m := range s.reg.List() {
		counts[m.Provider]++
	}
	vals := cardFieldValues(cfg)
	out := make([]ProviderState, 0, len(providerCards()))
	for _, c := range providerCards() {
		regName := c.Name
		if alias := cardToolProvider[regName]; alias != "" {
			regName = alias
		}
		st := ProviderState{
			Name: c.Name, Title: c.Title, Description: c.Description,
			Kind: c.Kind, Order: c.Order, ToolsCount: counts[regName],
			Fields: []FieldState{},
		}
		cv := vals[c.Name]
		st.Configured = cardConfigured(c.Name, cv)
		for _, f := range c.Fields {
			fs := FieldState{
				Key: f.Key, Label: f.Label, Kind: f.Kind,
				Options: f.Options, Placeholder: f.Placeholder, Hint: f.Hint, Required: f.Required,
			}
			if f.Kind == provider.FieldSecret {
				fs.HasValue = cv[f.Key] != ""
			} else {
				fs.Value = cv[f.Key]
			}
			st.Fields = append(st.Fields, fs)
		}
		out = append(out, st)
	}
	return out
}
