package provider

import "fmt"

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

// ProviderInfo 设置页一张卡的声明。卡声明属静态描述，当前值/已配置状态由 service 装配。
type ProviderInfo struct {
	Name        string
	Title       string
	Description string
	Kind        ProviderKind
	Fields      []CredentialField
	Order       int
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
	return nil
}
