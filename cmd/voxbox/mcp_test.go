package main

import "testing"

// TestSepEngineProvider 回归守护：engine 是使用者侧的引擎别名（mvsep|mediakit），
// provider 才是注册表名（mvsep|volcengine）。
//
// 历史 bug：voxbox_music_separate 把 engine 原样当 provider 交给 service，
// 于是 engine=mediakit 被 service 层「provider 仅支持 mvsep | volcengine」拒绝，
// 火山（计费）链路从 MCP 侧完全不可达；而 voxbox_separate 走了本函数，所以只有它正常。
func TestSepEngineProvider(t *testing.T) {
	cases := []struct {
		engine string
		want   string
		wantOK bool
	}{
		{"", "mvsep", true},
		{"mvsep", "mvsep", true},
		{"MVSEP", "mvsep", true},
		{" mvsep ", "mvsep", true},
		{"mediakit", "volcengine", true},
		{"MediaKit", "volcengine", true},
		{"volcengine", "volcengine", true},
		{"gsgc", "gsgc", true},
		{"cloudsep", "gsgc", true},
		{"GSGC", "gsgc", true},
		{"pcgeshi", "gsgc", true},
		{"geshi", "gsgc", true},
		// zhuanhuanmao 2026-09-19 起是独立线路（转换猫），不再收敛为 gsgc
		{"zhuanhuanmao", "zhuanhuanmao", true},
		{"convertmao", "zhuanhuanmao", true},
		{"demucs", "", false},
	}
	for _, c := range cases {
		got, ok := sepEngineProvider(c.engine)
		if got != c.want || ok != c.wantOK {
			t.Errorf("sepEngineProvider(%q) = (%q, %v), want (%q, %v)", c.engine, got, ok, c.want, c.wantOK)
		}
	}
}
