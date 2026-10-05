package provider

import (
	"fmt"

	"github.com/yann0917/voxbox/internal/config"
)

// ProviderKind 设置页的分组维度：云端服务（凭证卡，可配置可探活）与本地能力（只读展示）。
type ProviderKind string

const (
	KindCloud ProviderKind = "cloud"
	KindLocal ProviderKind = "local"
)

// FieldKind 凭证字段的表现形态：text 明文输入、secret 敏感输入（回显 has_value、保存留空=不改）、
// select 下拉（Options 给全量可选项）。
type FieldKind string

const (
	FieldText   FieldKind = "text"
	FieldSecret FieldKind = "secret"
	FieldSelect FieldKind = "select"
)

// CredentialField 一张凭证卡上的单个字段声明。Key 是 API 载荷键，ConfigKey 是 config.yaml
// 落盘键——两者解耦：mediakit 卡的字段 Key=api_key，落盘仍是 volc.mediakit.api_key。
type CredentialField struct {
	Key         string
	Label       string
	Kind        FieldKind
	ConfigKey   string
	Options     []ParamOption
	Required    bool
	Placeholder string
	Hint        string
}

// ProviderInfo 设置页一张卡的完整自描述：字段声明 + 凭证行为（当前值读取/内存快照写入/
// 工具热重注册/配置段同步/连通性探测）。行为闭包由各 provider 包在卡声明处一并给出，
// 装配层（service/server）只遍历卡清单，不为任何厂商写 switch——新增厂商 = 新建包
// 自描述 + 卡清单加一行。本地能力卡（KindLocal）无凭证行为，行为字段留零值。
// 平台跳转不做独立链接声明：描述文案里写明域名，前端统一把域名渲染成可点击链接。
type ProviderInfo struct {
	Name        string
	Title       string
	Description string
	Kind        ProviderKind
	Fields      []CredentialField
	Order       int

	// —— 以下行为仅云端卡声明（Validate 强制 Values/Apply/ReRegister/Sync 非空）——

	// Values 从配置快照取字段当前值，键=Fields[].Key，必须恰好覆盖全部声明字段
	// （cards_test 防呆）。secret 值仅用于 configured/has_value 判定，不出 HTTP 响应。
	Values func(cfg *config.Config) map[string]string
	// Configured 卡级「已配置」判定；nil=任一字段有值即已配置（单凭证卡的通用口径）。
	Configured func(vals map[string]string) bool
	// Apply 把提交字段套进内存快照，语义必须与落盘一致（SaveProviderFields）：
	// secret 留空=不修改；text/select 提交值即生效（空串=清空，如接入线路回落主站）。
	// 提交键合法性（必须 ∈Fields）已由 SaveProviderFields 校验，这里不重复防呆。
	Apply func(nc *config.Config, fields map[string]string)
	// ReRegister 凭证变更后的工具热重注册（注册表 Replace 语义，幂等）。
	ReRegister func(reg *Registry, cfg config.Config, dataDir string)
	// Sync 配置文件热监听（service.ReloadDiskConfig）路径下，把磁盘快照中本卡管辖的
	// 配置段并入内存快照。多卡共用一个配置段时可重复声明（volcengine/mediakit 同属 volc 段）。
	Sync func(dst, src *config.Config)
	// Test 连通性探测（设置页 admin 手动触发，部分探测消耗少量配额）。
	// 省略则该卡不出现在 test-connection 结果里。
	Test func(cfg *config.Config) (string, bool)
}

// Validate 声明合法性：启动/测试路径防呆，非法声明属编程错误。
func (p ProviderInfo) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("卡声明 Name 不能为空（Title=%q）", p.Title)
	}
	if p.Kind == KindLocal {
		if len(p.Fields) > 0 {
			return fmt.Errorf("本地能力卡 %s 不应有凭证字段", p.Name)
		}
		return nil
	}
	if len(p.Fields) == 0 {
		return fmt.Errorf("云端卡 %s 至少需要 1 个凭证字段", p.Name)
	}
	for _, f := range p.Fields {
		if f.Key == "" || f.ConfigKey == "" {
			return fmt.Errorf("卡 %s 字段 %q 的 Key/ConfigKey 不能为空", p.Name, f.Key)
		}
		if f.Kind == FieldSelect && len(f.Options) == 0 {
			return fmt.Errorf("卡 %s select 字段 %q 必须提供 Options", p.Name, f.Key)
		}
	}
	if p.Values == nil || p.Apply == nil || p.ReRegister == nil || p.Sync == nil {
		return fmt.Errorf("云端卡 %s 缺行为声明（Values/Apply/ReRegister/Sync 必须给出）", p.Name)
	}
	return nil
}

// IsConfigured 卡级已配置判定：声明给定了 Configured 则用之，否则按「任一字段有值」兜底。
func (p ProviderInfo) IsConfigured(vals map[string]string) bool {
	if p.Configured != nil {
		return p.Configured(vals)
	}
	for _, v := range vals {
		if v != "" {
			return true
		}
	}
	return false
}
