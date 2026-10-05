// 设置页「云端服务/本地环境」两分组的卡片装配：每张卡的声明与凭证行为（当前值读取/
// 快照写入/热重注册/探活）全部自描述于各 provider 包，此处只维护卡清单与通用装配
// （排序、工具计数、字段态渲染），不为任何厂商写 switch。
package service

import (
	"sort"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
	"github.com/yann0917/voxbox/internal/provider/gsgc"
	"github.com/yann0917/voxbox/internal/provider/local"
	"github.com/yann0917/voxbox/internal/provider/minimax"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
	"github.com/yann0917/voxbox/internal/provider/openrouter"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
)

// providerCards 全部卡声明（按 Order 排序）。新增厂商 = 新建包自描述卡 + 此处加一行。
func providerCards() []provider.ProviderInfo {
	cards := []provider.ProviderInfo{
		volcengine.ProviderCard(),
		volcengine.MediaKitCard(),
		mvsep.ProviderCard(),
		qianwen.ProviderCard(),
		xiaomi.ProviderCard(),
		zhipu.ProviderCard(),
		openrouter.ProviderCard(),
		minimax.ProviderCard(),
		audiotool.ProviderCard(),
		gsgc.ProviderCard(),
		local.ProviderCard(),
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Order < cards[j].Order })
	return cards
}

// ProviderCards 卡清单透出（server 的 test-connection 按卡自描述的 Test 逐卡探测）。
func (s *Service) ProviderCards() []provider.ProviderInfo { return providerCards() }

// ProviderSettingRows 云端卡凭证键的配置清单行（CLI `voxbox config list` 消费）：
// 键=字段 ConfigKey、值=配置快照当前值、secret 按字段类型打码。派生自卡自描述，
// 新增厂商无需再登记 config 的键表——config list 行随卡出现。
func ProviderSettingRows(cfg *config.Config) []config.KV {
	rows := []config.KV{}
	for _, c := range providerCards() {
		if c.Kind != provider.KindCloud || c.Values == nil {
			continue
		}
		vals := c.Values(cfg)
		for _, f := range c.Fields {
			v := vals[f.Key]
			if f.Kind == provider.FieldSecret {
				v = config.MaskValue(v)
			}
			rows = append(rows, config.KV{Key: f.ConfigKey, Value: v})
		}
	}
	return rows
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

// ProviderStates 卡自描述 + 配置快照 + 注册表工具计数 → HTTP 形态。
// secret 值只用于 configured/has_value 判定，不出 HTTP 响应。
func (s *Service) ProviderStates(cfg *config.Config) []ProviderState {
	counts := map[string]int{}
	for _, m := range s.reg.List() {
		counts[m.Provider]++
	}
	cards := providerCards()
	out := make([]ProviderState, 0, len(cards))
	for _, c := range cards {
		regName := c.Name
		if alias := cardToolProvider[regName]; alias != "" {
			regName = alias
		}
		st := ProviderState{
			Name: c.Name, Title: c.Title, Description: c.Description,
			Kind: c.Kind, Order: c.Order, ToolsCount: counts[regName],
			Fields: []FieldState{},
		}
		var cv map[string]string
		if c.Values != nil {
			cv = c.Values(cfg)
		}
		st.Configured = c.IsConfigured(cv)
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
