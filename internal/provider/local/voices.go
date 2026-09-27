package local

// LocalVoice 预置音色(CustomVoice GGUF 的 speaker 名单,来源 audio.cpp model_specs)。
type LocalVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
