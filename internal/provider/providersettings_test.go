package provider

import (
	"strings"
	"testing"
)

// ValidateCard 卡声明约束：云端卡至少一个字段、每个字段 ConfigKey 非空、secret 字段必为 Required 语义基线、
// select 字段必须给 Options、本地卡不允许带字段。
func TestValidateCard(t *testing.T) {
	cases := []struct {
		name string
		card ProviderInfo
		want string // 期望的错误子串，空=合法
	}{
		{"合法云端卡", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindCloud, Order: 10,
			Fields: []CredentialField{
				{Key: "api_key", Label: "API Key", Kind: FieldSecret, ConfigKey: "demo.api_key"},
			},
		}, ""},
		{"云端卡无字段", ProviderInfo{Name: "demo", Title: "演示", Kind: KindCloud}, "至少需要 1 个凭证字段"},
		{"字段缺 ConfigKey", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindCloud,
			Fields: []CredentialField{{Key: "k", Label: "K", Kind: FieldText}},
		}, "ConfigKey 不能为空"},
		{"select 无选项", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindCloud,
			Fields: []CredentialField{{Key: "line", Label: "线路", Kind: FieldSelect, ConfigKey: "demo.line"}},
		}, "必须提供 Options"},
		{"本地卡带字段", ProviderInfo{
			Name: "demo", Title: "演示", Kind: KindLocal,
			Fields: []CredentialField{{Key: "k", Label: "K", Kind: FieldText, ConfigKey: "demo.k"}},
		}, "不应有凭证字段"},
		{"卡名空", ProviderInfo{Title: "演示", Kind: KindCloud}, "Name 不能为空"},
	}
	for _, tc := range cases {
		err := tc.card.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: 期望合法，报错 %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: 期望错误含 %q，得到 %v", tc.name, tc.want, err)
		}
	}
}

func TestBuildSRT(t *testing.T) {
	got := BuildSRT([]SRTSegment{
		{StartMS: 0, EndMS: 1500, Text: "你好"},
		{StartMS: 1500, EndMS: 62000, Text: "世界"},
	})
	want := "1\n00:00:00,000 --> 00:00:01,500\n你好\n\n2\n00:00:01,500 --> 00:01:02,000\n世界\n"
	if got != want {
		t.Errorf("BuildSRT 输出不符:\n得到:\n%s\n期望:\n%s", got, want)
	}
}
