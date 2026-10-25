package workflow

// 工具端口表：画布连线校验与节点端口渲染的静态事实源。产物 kind 与
// provider.Artifact 的 Kind 词汇一致（audio/video/image/transcript/subtitle/
// dialog/translation/minutes/archive），text 是参数型输入（进 Params，不走端口）。
//
// 防漂移：ports_test 保证 registry 里每个可链工具都有表项——新工具注册后
// 忘记登记会在测试期暴露，而不是用户搭图时连不上线。

// InPort 节点的具名输入端口：Key 与引擎 Files 键一致（audio/audio2…），
// Kind 为接受的产物 kind（"file" 通配任意产物）。
type InPort struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// ToolSpec 一个工具在画布里的端口形态。Chainable=false 表示不进工具面板
// （管理类/检测类工具：无产物输入输出，串进流程没有意义）。
type ToolSpec struct {
	In        []InPort `json:"in,omitempty"`
	OutKinds  []string `json:"out_kinds,omitempty"` // 可能的输出产物 kind（连线按交集校验）
	Chainable bool     `json:"chainable"`
}

var audioIn = InPort{Key: "audio", Kind: "audio", Label: "音频"}

// ports 工具端口表，键 = "provider.tool"。
var ports = map[string]ToolSpec{
	// ---- 下载类：输入是 URL/参数，无产物输入端口 ----
	"downloader.download":     {OutKinds: []string{"audio", "video", "subtitle", "image", "archive"}, Chainable: true},
	"music.music_download":    {OutKinds: []string{"audio"}, Chainable: true},
	"music.music_mv_download": {OutKinds: []string{"video"}, Chainable: true},

	// ---- 识别/妙记：吃音频出文本 ----
	"volcengine.asr":     {In: []InPort{audioIn}, OutKinds: []string{"transcript", "subtitle"}, Chainable: true},
	"volcengine.minutes": {In: []InPort{{Key: "audio", Kind: "audio", Label: "音视频"}}, OutKinds: []string{"minutes", "transcript"}, Chainable: true},
	"qianwen.asr":        {In: []InPort{audioIn}, OutKinds: []string{"transcript", "subtitle"}, Chainable: true},
	"xiaomi.asr":         {In: []InPort{audioIn}, OutKinds: []string{"transcript"}, Chainable: true},
	"zhipu.asr":          {In: []InPort{audioIn}, OutKinds: []string{"transcript"}, Chainable: true},
	"minimax.asr":        {In: []InPort{audioIn}, OutKinds: []string{"transcript", "subtitle"}, Chainable: true},
	"local.asr":          {In: []InPort{audioIn}, OutKinds: []string{"transcript", "subtitle"}, Chainable: true},

	// ---- 合成/翻译/播客：吃文本参数，出音频/译文 ----
	"volcengine.tts":        {OutKinds: []string{"audio"}, Chainable: true},
	"volcengine.tts_stream": {OutKinds: []string{"audio"}, Chainable: true},
	"volcengine.tts_long":   {OutKinds: []string{"audio"}, Chainable: true},
	"volcengine.podcast":    {OutKinds: []string{"audio"}, Chainable: true},
	"volcengine.translate":  {OutKinds: []string{"translation"}, Chainable: true},
	"qianwen.tts":           {OutKinds: []string{"audio"}, Chainable: true},
	"xiaomi.tts":            {OutKinds: []string{"audio"}, Chainable: true},
	"zhipu.tts":             {OutKinds: []string{"audio"}, Chainable: true},
	"openrouter.tts":        {OutKinds: []string{"audio"}, Chainable: true},
	"minimax.tts":           {OutKinds: []string{"audio"}, Chainable: true},
	"minimax.tts_long":      {OutKinds: []string{"audio"}, Chainable: true},
	"local.tts":             {OutKinds: []string{"audio"}, Chainable: true},

	// ---- 分离 ----
	"mvsep.separate":        {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"volcengine.separate":   {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"zhuanhuanmao.separate": {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"gsgc.separate":         {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},

	// ---- 音频剪辑（mix 约定 files["audio"]=伴奏、files["audio2"]=人声；
	//      duck 同序：第一路=人声、第二路=BGM）----
	"audio.trim":      {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.clip":      {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.merge":     {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.pitch":     {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.equalizer": {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.volume":    {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.fade":      {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.reverse":   {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.mix": {In: []InPort{
		{Key: "audio", Kind: "audio", Label: "伴奏"},
		{Key: "audio2", Kind: "audio", Label: "人声"},
	}, OutKinds: []string{"audio"}, Chainable: true},
	"audio.duck": {In: []InPort{
		{Key: "audio", Kind: "audio", Label: "人声"},
		{Key: "audio2", Kind: "audio", Label: "BGM"},
	}, OutKinds: []string{"audio"}, Chainable: true},
	// 剪静音：ASR/播客前置裁剪（auto-editor 思路本地化）
	"audio.silence_cut": {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	// 场景切点：视频镜头切换列表（PySceneDetect 思路，ffmpeg 原生 scene 检测）；
	// 联络表随场景切点附带产出（image）
	"audio.scene_detect": {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"scenes", "image"}, Chainable: true},
	// 字幕烧录：字幕全文走参数，视频走文件端口
	"audio.subtitle_burn": {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"video"}, Chainable: true},

	// ---- 格式工厂（gsgc 站点工具，图片类吃 image，其余按媒体类型）----
	"gsgc.video-format-convert": {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"video"}, Chainable: true},
	"gsgc.video-compression":    {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"video"}, Chainable: true},
	"gsgc.video-extract-audio":  {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"audio"}, Chainable: true},
	"gsgc.video-volume-adjust":  {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"video"}, Chainable: true},
	"gsgc.video-speed":          {In: []InPort{{Key: "audio", Kind: "video", Label: "视频"}}, OutKinds: []string{"video"}, Chainable: true},
	"gsgc.audio-format-convert": {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"gsgc.audio-compression":    {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"gsgc.audio-denoise":        {In: []InPort{audioIn}, OutKinds: []string{"audio"}, Chainable: true},
	"gsgc.image-format-convert": {In: []InPort{{Key: "audio", Kind: "image", Label: "图片"}}, OutKinds: []string{"image"}, Chainable: true},
	"gsgc.image-compression":    {In: []InPort{{Key: "audio", Kind: "image", Label: "图片"}}, OutKinds: []string{"image"}, Chainable: true},

	// ---- 管理类/检测类：不进画布 ----
	"zhipu.voice_clone":  {Chainable: false},
	"zhipu.voice_delete": {Chainable: false},
	"audio.analyze":      {Chainable: false},
	"audio.hook":         {Chainable: false},
}

// SpecOf 查工具端口形态；无表项返回 nil（校验期报「工具不支持编排」）。
func SpecOf(provider, tool string) *ToolSpec {
	if s, ok := ports[provider+"."+tool]; ok {
		return &s
	}
	return nil
}

// acceptsKind 端口是否接受某产物 kind：file 通配，其余精确匹配。
func acceptsKind(portKind, artifactKind string) bool {
	return portKind == "file" || portKind == artifactKind
}

// Compatible 连线校验：上游可能产出与端口可接受存在交集才允许连接。
func (p InPort) Compatible(upstreamOutKinds []string) bool {
	for _, k := range upstreamOutKinds {
		if acceptsKind(p.Kind, k) {
			return true
		}
	}
	return false
}

// PortOf 按 Files 键找输入端口定义（校验 InputsFrom 的端口名合法性）。
func (s ToolSpec) PortOf(key string) (InPort, bool) {
	for _, p := range s.In {
		if p.Key == key {
			return p, true
		}
	}
	return InPort{}, false
}
