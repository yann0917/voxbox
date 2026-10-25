package workflow

import (
	"strings"
	"testing"
)

func testGraph() *Graph {
	return &Graph{
		Inputs: []InputSlot{
			{Key: "url", Label: "链接", Type: "string", Required: true},
			{Key: "text", Label: "文案", Type: "text"},
		},
		Nodes: []Node{
			{Key: "dl", Provider: "downloader", Tool: "download", Params: map[string]any{"url": "{{inputs.url}}", "mode": "audio"}},
			{Key: "asr", Provider: "local", Tool: "asr", Params: map[string]any{"language": "auto"}, InputsFrom: map[string]string{"audio": "dl"}},
		},
	}
}

func TestGraphValidateOK(t *testing.T) {
	if err := testGraph().Validate(); err != nil {
		t.Fatalf("合法图不应报错: %v", err)
	}
}

func TestGraphValidateErrors(t *testing.T) {
	cases := map[string]func(*Graph){
		"重复key": func(g *Graph) { g.Nodes[1].Key = "dl" },
		"空key":  func(g *Graph) { g.Nodes[0].Key = "" },
		"上游不存在": func(g *Graph) { g.Nodes[1].InputsFrom["audio"] = "ghost" },
		"直接环":   func(g *Graph) { g.Nodes[0].InputsFrom = map[string]string{"audio": "asr"} },
		"槽类型非法": func(g *Graph) { g.Inputs[0].Type = "magic" },
		"版本过新":  func(g *Graph) { g.Version = 99 },
	}
	for name, mutate := range cases {
		g := testGraph()
		mutate(g)
		if err := g.Validate(); err == nil {
			t.Errorf("%s: 应报错", name)
		}
	}
}

func TestGraphExpand(t *testing.T) {
	g := testGraph()
	out, files, err := g.Expand(map[string]any{"url": "https://example.com/v.mp3", "text": ""})
	if err != nil {
		t.Fatalf("展开失败: %v", err)
	}
	if got := out["dl"]["url"]; got != "https://example.com/v.mp3" {
		t.Errorf("url 未展开: %v", got)
	}
	if len(files) != 0 {
		t.Errorf("无 file 槽时不应有 file 值: %v", files)
	}
	// 必填缺失
	if _, _, err := g.Expand(map[string]any{}); err == nil || !strings.Contains(err.Error(), "缺少必填输入") {
		t.Errorf("必填缺失应报错，得到: %v", err)
	}
	// 未声明槽引用
	g2 := testGraph()
	g2.Nodes[0].Params["x"] = "{{inputs.nosuch}}"
	if _, _, err := g2.Expand(map[string]any{"url": "u"}); err == nil || !strings.Contains(err.Error(), "未提供") {
		t.Errorf("未声明槽应报错，得到: %v", err)
	}
}

func TestGraphExpandFileSlot(t *testing.T) {
	g := &Graph{
		Inputs: []InputSlot{{Key: "voice", Label: "人声", Type: "file", Required: true}},
		Nodes:  []Node{{Key: "asr", Provider: "local", Tool: "asr", InputsFrom: map[string]string{"audio": "inputs.voice"}}},
	}
	_, files, err := g.Expand(map[string]any{"voice": "018c2d3e-1111-2222-3333-444455556666"})
	if err != nil {
		t.Fatalf("file 槽展开失败: %v", err)
	}
	if files["voice"] != "018c2d3e-1111-2222-3333-444455556666" {
		t.Errorf("file 槽值未收集: %v", files)
	}
	if _, _, err := g.Expand(map[string]any{}); err == nil {
		t.Error("file 必填槽缺失应报错")
	}
}

func TestGraphExpandTrackSlot(t *testing.T) {
	g := &Graph{
		Inputs: []InputSlot{{Key: "track", Label: "选歌", Type: "track", Required: true}},
		Nodes: []Node{
			{Key: "dl", Provider: "music", Tool: "music_download", Params: map[string]any{
				"source": "{{inputs.track.source}}", "rid": "{{inputs.track.rid}}",
				"duration_ms": "{{inputs.track.duration}}",
			}},
		},
	}
	out, _, err := g.Expand(map[string]any{
		"track": map[string]any{"source": "kw", "rid": "42", "duration": 210000},
	})
	if err != nil {
		t.Fatalf("track 槽展开失败: %v", err)
	}
	if out["dl"]["source"] != "kw" || out["dl"]["rid"] != "42" {
		t.Errorf("字段引用未展开: %v", out["dl"])
	}
	if out["dl"]["duration_ms"] != "210000" {
		t.Errorf("duration 字段未展开: %v", out["dl"]["duration_ms"])
	}
	// 整槽引用（不带字段）禁止——选歌槽必须按字段取
	g2 := &Graph{
		Inputs: []InputSlot{{Key: "track", Label: "选歌", Type: "track"}},
		Nodes:  []Node{{Key: "n", Params: map[string]any{"x": "{{inputs.track}}"}}},
	}
	if _, _, err := g2.Expand(map[string]any{"track": map[string]any{"source": "kw"}}); err == nil || !strings.Contains(err.Error(), "字段") {
		t.Errorf("整槽引用应报错，得到: %v", err)
	}
	// 非对象值（选歌控件漏传）拒绝
	if _, _, err := g.Expand(map[string]any{"track": "kw-42"}); err == nil {
		t.Error("track 槽非对象值应报错")
	}
	// 必填缺失
	if _, _, err := g.Expand(map[string]any{}); err == nil {
		t.Error("track 必填槽缺失应报错")
	}
}

func TestParseUpstreamRef(t *testing.T) {
	cases := map[string]UpstreamRef{
		"dl":              {Node: "dl"},
		"sep:track=voice": {Node: "sep", Select: "track=voice"},
		"sep:2":           {Node: "sep", Select: "2"},
	}
	for in, want := range cases {
		got, err := ParseUpstreamRef(in)
		if err != nil || got != want {
			t.Errorf("%q → %+v (want %+v), err=%v", in, got, want, err)
		}
	}
	if _, err := ParseUpstreamRef(":2"); err == nil {
		t.Error("空节点名应报错")
	}
}

func TestFindCycleTwoHops(t *testing.T) {
	g := &Graph{Nodes: []Node{
		{Key: "a", InputsFrom: map[string]string{"audio": "b"}},
		{Key: "b", InputsFrom: map[string]string{"audio": "c"}},
		{Key: "c"},
	}}
	if err := g.Validate(); err != nil {
		t.Fatalf("长链不应报环: %v", err)
	}
	g.Nodes[2].InputsFrom = map[string]string{"audio": "a"}
	if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "循环") {
		t.Errorf("三节点环应报错，得到: %v", err)
	}
}
