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

// kokoroZhIDs kokoro v1.1-zh 内置中文音色编号:voices/ 目录实际存在的 .pt 文件,
// 升序即 voices.bin 打包顺序。3 个英文音色在头部(sid 0-2),女声紧随其后。
var kokoroZhIDs = []string{
	"001", "002", "003", "004", "005", "006", "007", "008",
	"017", "018", "019", "021", "022", "023", "024", "026", "027", "028",
	"032", "036", "038", "039", "040", "042", "043", "044", "046", "047", "048", "049", "051",
	"059", "060", "067", "070", "071", "072", "073", "074", "075", "076", "077", "078", "079",
	"083", "084", "085", "086", "087", "088", "090", "092", "093", "094", "099",
	"m009", "m010", "m011", "m012", "m013", "m014", "m015", "m016", "m020",
	"m025", "m029", "m030", "m031", "m033", "m034", "m035", "m037", "m041", "m045", "m050",
	"m052", "m053", "m054", "m055", "m056", "m057", "m058", "m061", "m062", "m063", "m064", "m065", "m066",
	"m068", "m069", "m080", "m081", "m082", "m089", "m091", "m095", "m096", "m097", "m098", "m100",
}

// KokoroVoices kokoro-multi-lang-v1_1(hexgrad/Kokoro-82M-v1.1-zh 的 sherpa-onnx
// 导出)的 103 个内置音色,返回顺序即 voices.bin 的 sid 顺序:0-2 英文女声,
// 3-57 中文女声(zf_),58-102 中文男声(zm_)。
func KokoroVoices() []LocalVoice {
	en := []string{"af_maple", "af_sol", "bf_vale"}
	out := make([]LocalVoice, 0, len(en)+len(kokoroZhIDs))
	for _, id := range en {
		out = append(out, LocalVoice{ID: id, Name: id + " · 英文女声", Sid: len(out)})
	}
	for _, num := range kokoroZhIDs {
		if num[0] == 'm' { // m 前缀为男声段,展开为 zm_ 编号
			id := "zm_" + num[1:]
			out = append(out, LocalVoice{ID: id, Name: id + " · 中文男声", Sid: len(out)})
			continue
		}
		id := "zf_" + num
		out = append(out, LocalVoice{ID: id, Name: id + " · 中文女声", Sid: len(out)})
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
