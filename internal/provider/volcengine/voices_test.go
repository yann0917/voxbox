package volcengine

import (
	"strings"
	"testing"
)

// 筛选词表锁定：Web 端筛选器按这些值渲染选项，voices.json 出现新值即构建期暴露。
var wantVoiceScenes = map[string]bool{
	"通用场景": true, "角色扮演": true, "视频配音": true, "教育场景": true,
	"客服场景": true, "有声阅读": true, "外语音色": true, "多情感": true, "趣味口音": true,
}

var wantVoiceLangs = map[string]bool{
	"中文": true, "美式英语": true, "英式英语": true, "澳洲英语": true,
	"日语": true, "韩语": true, "印尼语": true, "马来语": true, "泰语": true,
	"菲律宾语": true, "越南语": true, "阿拉伯语": true, "西班牙语": true, "墨西哥西语": true,
	"巴西葡萄牙语": true, "法语": true, "德语": true, "意大利语": true, "俄语": true,
}

func voiceListHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestVoicesData(t *testing.T) {
	vs := Voices()
	if len(vs) < 500 {
		t.Fatalf("音色数 = %d, 期望 ≥500（官方 2.0+1.0 全量）", len(vs))
	}
	seen := map[string]bool{}
	for _, v := range vs {
		if v.ID == "" || v.Name == "" {
			t.Fatalf("存在空 id/name: %+v", v)
		}
		if seen[v.ID] {
			t.Fatalf("voice_type 重复: %s", v.ID)
		}
		seen[v.ID] = true
		if v.Gender != "男" && v.Gender != "女" {
			t.Errorf("%s gender = %q", v.ID, v.Gender)
		}
		if len(v.Scenes) == 0 || len(v.Languages) == 0 {
			t.Errorf("%s scenes/languages 为空", v.ID)
		}
		for _, s := range v.Scenes {
			if !wantVoiceScenes[s] {
				t.Errorf("%s 出现未锁定场景值: %q", v.ID, s)
			}
		}
		for _, l := range v.Languages {
			if !wantVoiceLangs[l] {
				t.Errorf("%s 出现未锁定语种值: %q", v.ID, l)
			}
		}
		if v.Generation != "2.0" && v.Generation != "1.0" {
			t.Errorf("%s generation = %q", v.ID, v.Generation)
		}
		// S2S/SC 端到端实时模型专用音色（jupiter/saturn 前缀）不属于 TTS 可用列表
		if strings.Contains(v.ID, "saturn_") || strings.Contains(v.ID, "jupiter_") {
			t.Errorf("S2S 专用音色混入: %s", v.ID)
		}
	}
}

func TestVoicesSpotChecks(t *testing.T) {
	byID := map[string]Voice{}
	for _, v := range Voices() {
		byID[v.ID] = v
	}
	cases := []struct {
		id       string
		name     string
		gen      string
		lang     string
		dialect  string
		noteFrag string
	}{
		{"zh_female_vv_uranus_bigtts", "Vivi 2.0", "2.0", "中文", "粤语", ""},
		{"en_male_tim_uranus_bigtts", "Tim", "2.0", "美式英语", "", ""},
		{"ar_female_dina_uranus_bigtts", "Dina", "2.0", "阿拉伯语", "", ""},
		{"de_male_sven_uranus_bigtts", "Sven", "2.0", "德语", "", "单向流"},
		{"zh_female_cancan_mars_bigtts", "灿灿/Shiny", "1.0", "中文", "", ""},
		{"zh_male_lengkugege_emo_v2_mars_bigtts", "冷酷哥哥（多情感）", "1.0", "中文", "", ""},
	}
	for _, tc := range cases {
		v, ok := byID[tc.id]
		if !ok {
			t.Fatalf("缺少音色 %s", tc.id)
		}
		if v.Name != tc.name {
			t.Errorf("%s name = %q, 期望 %q", tc.id, v.Name, tc.name)
		}
		if v.Generation != tc.gen {
			t.Errorf("%s generation = %q, 期望 %q", tc.id, v.Generation, tc.gen)
		}
		if !voiceListHas(v.Languages, tc.lang) {
			t.Errorf("%s languages 缺 %q: %v", tc.id, tc.lang, v.Languages)
		}
		if tc.dialect != "" && !voiceListHas(v.Dialects, tc.dialect) {
			t.Errorf("%s dialects 缺 %q: %v", tc.id, tc.dialect, v.Dialects)
		}
		if tc.noteFrag != "" && !strings.Contains(v.Note, tc.noteFrag) {
			t.Errorf("%s note 缺 %q: %q", tc.id, tc.noteFrag, v.Note)
		}
	}
	// 多情感音色应携带情感参数表
	if len(byID["zh_male_lengkugege_emo_v2_mars_bigtts"].Emotions) == 0 {
		t.Errorf("多情感音色应携带 emotions")
	}
	// 开朗学长（en_male_jason）官方确认支持中英混，文档语种列漏标英文，人工修正后锁定
	jason := byID["en_male_jason_conversation_wvae_bigtts"]
	if !voiceListHas(jason.Languages, "中文") || !voiceListHas(jason.Languages, "美式英语") {
		t.Errorf("开朗学长应支持中英混: %v", jason.Languages)
	}
}
