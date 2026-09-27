package localmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func validEntry() Entry {
	return Entry{
		ID: "m1", Repo: "org/m1", Name: "M1", Kind: "asr", Summary: "测试模型",
		SizeBytes: 100, Files: []string{"model.bin"},
		Requirements: Requirements{Device: "cpu"},
		License:      "Apache-2.0", LicenseURL: "https://modelscope.cn/models/org/m1",
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseCatalogAcceptsValid(t *testing.T) {
	entries, err := parseCatalog(mustJSON(t, []Entry{validEntry()}))
	if err != nil {
		t.Fatalf("合法条目被拒绝: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "m1" {
		t.Fatalf("解析结果不符: %+v", entries)
	}
}

func TestParseCatalogRejects(t *testing.T) {
	cases := map[string][]Entry{
		"id重复":        {validEntry(), func() Entry { e := validEntry(); e.Repo = "org/m2"; return e }()},
		"缺字段":         {func() Entry { e := validEntry(); e.Name = ""; return e }()},
		"非法设备":        {func() Entry { e := validEntry(); e.Requirements.Device = "tpu"; return e }()},
		"无文件":         {func() Entry { e := validEntry(); e.Files = nil; return e }()},
		"大小非法":        {func() Entry { e := validEntry(); e.SizeBytes = 0; return e }()},
		"上级穿越":        {func() Entry { e := validEntry(); e.Files = []string{"../model.bin"}; return e }()},
		"绝对路径":        {func() Entry { e := validEntry(); e.Files = []string{"/etc/passwd"}; return e }()},
		"反斜杠":         {func() Entry { e := validEntry(); e.Files = []string{`dir\model.bin`}; return e }()},
		"未规范化":        {func() Entry { e := validEntry(); e.Files = []string{"./model.bin"}; return e }()},
		"保留名manifest": {func() Entry { e := validEntry(); e.Files = []string{"manifest.json"}; return e }()},
	}
	for name, entries := range cases {
		_, err := parseCatalog(mustJSON(t, entries))
		if err == nil {
			t.Errorf("%s: 期望被拒绝,实际通过", name)
		}
	}
}

func TestParseCatalogBadJSON(t *testing.T) {
	if _, err := parseCatalog([]byte("not json")); err == nil {
		t.Fatal("非法 JSON 期望报错")
	}
}

// 内嵌目录自检:随二进制发布的静态资产,损坏必须在首次加载时暴露。
func TestEmbeddedCatalog(t *testing.T) {
	if len(catalog) < 3 {
		t.Fatalf("内嵌目录至少 3 个条目,实际 %d", len(catalog))
	}
	ids := map[string]bool{}
	for _, e := range catalog {
		if ids[e.ID] {
			t.Errorf("条目 id 重复: %s", e.ID)
		}
		ids[e.ID] = true
		if !strings.HasPrefix(e.LicenseURL, "https://modelscope.cn/models/") {
			t.Errorf("条目 %s 的 license_url 应指向魔搭模型页: %s", e.ID, e.LicenseURL)
		}
	}
}

func TestParseCatalogDefaultsRevision(t *testing.T) {
	e := validEntry()
	e.Revision = ""
	entries, err := parseCatalog(mustJSON(t, []Entry{e}))
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Revision != "master" {
		t.Fatalf("revision 缺省应回落 master,实际 %q", entries[0].Revision)
	}
}
