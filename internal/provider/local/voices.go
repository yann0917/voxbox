package local

// LocalVoice 预置音色(qwen3 CustomVoice 为 speaker 名单,kokoro 为内置音色库)。
type LocalVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Sid  int    `json:"sid,omitempty"` // 仅 kokoro:voices.bin 的 speaker id(qwen3 恒 0 省略)
}

// CustomVoices 九个预置音色(audio.cpp 官方 speaker 集合)。
func CustomVoices() []LocalVoice {
	return []LocalVoice{
		{ID: "Vivian", Name: "Vivian · 女声"},
		{ID: "Serena", Name: "Serena · 女声"},
		{ID: "Uncle_Fu", Name: "Uncle Fu · 男声"},
		{ID: "Dylan", Name: "Dylan · 男声"},
		{ID: "Eric", Name: "Eric · 男声"},
		{ID: "Ryan", Name: "Ryan · 男声"},
		{ID: "Aiden", Name: "Aiden · 男声"},
		{ID: "Ono_Anna", Name: "Ono Anna · 女声(日语向)"},
		{ID: "Sohee", Name: "Sohee · 女声(韩语向)"},
	}
}

// kokoroSpeakers kokoro-multi-lang-v1_0(原版 Kokoro-82M,hexgrad/Kokoro-82M)的
// 53 个内置音色,顺序即 voices.bin 的 sid(scripts/kokoro/v1.0/generate_voices_bin.py):
// 45-52 为 8 个经典中文音色(xiaobei/xiaoni/xiaoxiao/xiaoyi/yunjian/yunxi/yunxia/yunyang)。
var kokoroSpeakers = []string{
	"af_alloy", "af_aoede", "af_bella", "af_heart", "af_jessica", "af_kore", "af_nicole",
	"af_nova", "af_river", "af_sarah", "af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir",
	"am_liam", "am_michael", "am_onyx", "am_puck", "am_santa", "bf_alice", "bf_emma",
	"bf_isabella", "bf_lily", "bm_daniel", "bm_fable", "bm_george", "bm_lewis", "ef_dora",
	"em_alex", "ff_siwis", "hf_alpha", "hf_beta", "hm_omega", "hm_psi", "if_sara", "im_nicola",
	"jf_alpha", "jf_gongitsune", "jf_nezumi", "jf_tebukuro", "jm_kumo", "pf_dora", "pm_alex",
	"pm_santa", "zf_xiaobei", "zf_xiaoni", "zf_xiaoxiao", "zf_xiaoyi", "zm_yunjian",
	"zm_yunxi", "zm_yunxia", "zm_yunyang",
}

// kokoroLangPrefix 音色名首字母 → 语言标注(第二位 f/m 为女/男声)。
var kokoroLangPrefix = map[string]string{
	"a": "美式英文", "b": "英式英文", "e": "西语", "f": "法语",
	"h": "印地语", "i": "意语", "j": "日语", "p": "葡语", "z": "中文",
}

// KokoroVoices kokoro-multi-lang-v1_0 的 53 个内置音色,返回顺序即 voices.bin 的
// sid 顺序(45-52 为经典中文音色)。
func KokoroVoices() []LocalVoice {
	out := make([]LocalVoice, 0, len(kokoroSpeakers))
	for _, id := range kokoroSpeakers {
		lang := kokoroLangPrefix[id[:1]]
		gender := "男声"
		if id[1] == 'f' {
			gender = "女声"
		}
		out = append(out, LocalVoice{ID: id, Name: id + " · " + lang + gender, Sid: len(out)})
	}
	return out
}

// KokoroVoiceSID 音色 id → voices.bin sid;未知音色返回 false。
func KokoroVoiceSID(id string) (int, bool) {
	for _, v := range KokoroVoices() {
		if v.ID == id {
			return v.Sid, true
		}
	}
	return 0, false
}
