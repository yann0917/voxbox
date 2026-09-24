package qianwen

import "github.com/yann0917/voxbox/internal/provider"

// qwen3-tts 非实时音色（官方音色列表，值即 voice 名）。
var voices = []provider.ParamOption{
	{Value: "Cherry", Label: "Cherry · 女声 · 阳光积极、亲切自然"},
	{Value: "Serena", Label: "Serena · 女声 · 温柔小姐姐"},
	{Value: "Ethan", Label: "Ethan · 男声 · 阳光温暖的北方口音"},
	{Value: "Chelsie", Label: "Chelsie · 女声 · 二次元虚拟女友"},
	{Value: "Momo", Label: "Momo · 女声 · 撒娇搞怪"},
	{Value: "Vivian", Label: "Vivian · 女声 · 拽拽的可爱小暴躁"},
	{Value: "Moon", Label: "Moon · 男声 · 率性帅气"},
	{Value: "Maia", Label: "Maia · 女声 · 知性与温柔"},
	{Value: "Kai", Label: "Kai · 男声 · 舒缓悦耳"},
	{Value: "Nofish", Label: "Nofish · 男声 · 不会翘舌音的设计师"},
	{Value: "Bella", Label: "Bella · 女声 · 小萝莉"},
	{Value: "Jennifer", Label: "Jennifer · 女声 · 电影质感美语"},
	{Value: "Ryan", Label: "Ryan · 男声 · 节奏拉满、戏感炸裂"},
	{Value: "Katerina", Label: "Katerina · 女声 · 御姐韵律"},
	{Value: "Aiden", Label: "Aiden · 男声 · 精通厨艺的美语大男孩"},
	{Value: "Eldric Sage", Label: "Eldric Sage · 男声 · 沉稳睿智的老者"},
	{Value: "Mia", Label: "Mia · 女声 · 温顺乖巧"},
	{Value: "Mochi", Label: "Mochi · 男声 · 早慧的小大人"},
	{Value: "Bellona", Label: "Bellona · 女声 · 洪亮清晰、江湖豪情"},
	{Value: "Vincent", Label: "Vincent · 男声 · 沙哑烟嗓"},
	{Value: "Bunny", Label: "Bunny · 女声 · 萌属性小萝莉"},
	{Value: "Neil", Label: "Neil · 男声 · 专业新闻主持"},
	{Value: "Elias", Label: "Elias · 女声 · 严谨的知识讲解"},
	{Value: "Arthur", Label: "Arthur · 男声 · 质朴嗓音的乡村老者"},
	{Value: "Nini", Label: "Nini · 女声 · 又软又黏的甜嗓"},
	{Value: "Seren", Label: "Seren · 女声 · 温和舒缓助眠"},
	{Value: "Pip", Label: "Pip · 男声 · 调皮捣蛋充满童真"},
	{Value: "Stella", Label: "Stella · 女声 · 甜腻迷糊少女音"},
	{Value: "Bodega", Label: "Bodega · 男声 · 热情的西班牙大叔"},
	{Value: "Sonrisa", Label: "Sonrisa · 女声 · 热情开朗的拉美大姐"},
	{Value: "Alek", Label: "Alek · 男声 · 俄罗斯风、冷中带暖"},
	{Value: "Dolce", Label: "Dolce · 男声 · 慵懒的意大利大叔"},
	{Value: "Sohee", Label: "Sohee · 女声 · 温柔开朗的韩国欧尼"},
	{Value: "Ono Anna", Label: "Ono Anna · 女声 · 鬼灵精怪的青梅竹马"},
	{Value: "Lenn", Label: "Lenn · 男声 · 理性叛逆的德国青年"},
	{Value: "Emilien", Label: "Emilien · 男声 · 浪漫的法国哥哥"},
	{Value: "Andre", Label: "Andre · 男声 · 磁性沉稳"},
	{Value: "Radio Gol", Label: "Radio Gol · 男声 · 足球解说诗人"},
	{Value: "Jada", Label: "Jada · 女声 · 风风火火的沪上阿姐（上海话）"},
	{Value: "Dylan", Label: "Dylan · 男声 · 北京胡同少年（北京话）"},
	{Value: "Li", Label: "Li · 男声 · 耐心的瑜伽老师（南京话）"},
	{Value: "Marcus", Label: "Marcus · 男声 · 老陕的味道（陕西话）"},
	{Value: "Roy", Label: "Roy · 男声 · 诙谐直爽的台湾哥仔（闽南语）"},
	{Value: "Peter", Label: "Peter · 男声 · 天津相声、专业捧哏（天津话）"},
	{Value: "Sunny", Label: "Sunny · 女声 · 甜到心里的川妹子（四川话）"},
	{Value: "Eric", Label: "Eric · 男声 · 跳脱市井的成都男子（四川话）"},
	{Value: "Rocky", Label: "Rocky · 男声 · 幽默风趣的阿强（粤语）"},
	{Value: "Kiki", Label: "Kiki · 女声 · 甜美的港妹闺蜜（粤语）"},
}

// VoiceOptions 音色枚举（tts 工具 ParamSpecs 与连通性测试共用）。
func VoiceOptions() []provider.ParamOption { return voices }

// DefaultVoice 默认音色。
const DefaultVoice = "Cherry"
