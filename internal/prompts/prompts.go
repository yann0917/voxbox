// Package prompts 提示词库的内置条目：按主题生成朗读文本与润色已有文本两用，
// 是「提示词库」页面与 TTS 文本区 AI 写作的唯一内置数据源。条目正文烤在二进制里，
// 用户自定义条目落 SQLite（internal/store.Prompt），列表接口在此合并。
package prompts

import "fmt"

// Kind 条目用途：generate=按主题生成新文本（用户消息是主题/要点），polish=改写已有文本
// （用户消息是原文）。自定义条目同样带用途标记，AI 写作弹窗按用途分流。
type Kind string

const (
	KindGenerate Kind = "generate"
	KindPolish   Kind = "polish"
)

// Valid 用途标记是否合法。
func (k Kind) Valid() bool { return k == KindGenerate || k == KindPolish }

// Length 生成文本的篇幅档位：仅在 generate 类条目上生效，转成字数指令拼进系统提示。
type Length string

const (
	LengthShort  Length = "short"
	LengthMedium Length = "medium"
	LengthLong   Length = "long"
)

// lengthDirective 篇幅档 → 字数指令。生成类条目正文不再各自写死字数，统一由档位控制。
var lengthDirective = map[Length]string{
	LengthShort:  "全文控制在 150 字以内。",
	LengthMedium: "全文 400 字左右。",
	LengthLong:   "全文 800 字左右。",
}

// Entry 提示词条目。内置条目 Key 稳定不可变（前端与 apply 接口按它引用），
// 改 Key 等于换条目。Builtin 恒 true；自定义条目由 store 模型承载，不经此结构出仓库。
type Entry struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Kind        Kind   `json:"kind"`
	Content     string `json:"content"`
	Builtin     bool   `json:"builtin"`
}

// builtin 内置清单：生成 8 条 + 润色 4 条。Category 是页面分组依据，新增场景先看
// 是否真有朗读需求（提示词质量比数量重要），再决定进哪个组。
var builtin = []Entry{
	{
		Key: "idiom-story", Name: "成语故事", Category: "故事", Kind: KindGenerate,
		Description: "把成语讲成有场景、有寓意的小故事，讲述体。",
		Content: "你是一位擅长在声音里讲故事的说书人。请围绕用户给出的成语，创作一个口语化的成语故事：" +
			"先有场景和人物，再展开情节，最后用一两句话自然点出寓意。故事要完整、有画面感，用讲述的语气，不用书面腔。",
	},
	{
		Key: "bedtime-story", Name: "睡前故事", Category: "故事", Kind: KindGenerate,
		Description: "温柔平缓的哄睡小故事，结尾安静收束。",
		Content: "你是一位睡前故事的讲述者。请创作一个温柔、安静、有安全感的睡前小故事：" +
			"节奏放缓，多用柔和的形容词，情节平缓，没有突然的惊吓或高强度冲突，" +
			"结尾让主角安然入睡或以安静的夜色收束，让听的人放松下来。",
	},
	{
		Key: "news-broadcast", Name: "新闻播报", Category: "播报", Kind: KindGenerate,
		Description: "正式播报腔：导语、主体、结语，适合照稿朗读。",
		Content: "你是一位新闻主播的撰稿人。请根据用户给出的主题或要点，写一篇口播新闻稿：" +
			"开头一句导语概括核心信息，中间按重要性展开两到三个信息点，结尾一句简短收束。" +
			"用词正式、句子完整；信息不足的地方用合理的概述带过，不要编造具体数字和未经证实的细节。",
	},
	{
		Key: "tongue-twister", Name: "绕口令", Category: "练声", Kind: KindGenerate,
		Description: "练发音的绕口令，由短到长逐段加难。",
		Content: "你是一位播音主持的发音练习老师。请根据用户指定的声母、韵母或主题，创作两到三段绕口令：" +
			"第一段短而简单，后面逐段加长加难；同一组音反复出现，拗口但有规律，读快了容易出错但不至于读不出来；" +
			"全部用常用字，方便反复练习。",
	},
	{
		Key: "store-visit", Name: "探店口播", Category: "文案", Kind: KindGenerate,
		Description: "探店短视频口播稿：开头有钩子，像朋友聊天推荐。",
		Content: "你是一位探店短视频的口播文案作者。请根据用户给出的店铺类型、菜品或亮点，写一段探店口播稿：" +
			"第一句就是钩子，让人想继续听；中间用第一人称讲体验，说具体的感受和细节，不堆砌华丽辞藻；" +
			"结尾自然引导，不喊口号。语气像朋友聊天推荐。",
	},
	{
		Key: "rednote-voiceover", Name: "小红书口播", Category: "文案", Kind: KindGenerate,
		Description: "小红书视频口播稿：有网感、信息密度高。",
		Content: "你是一位小红书视频的口播文案作者。请根据用户给出的产品或话题，写一段口播稿：" +
			"开头三秒抛出大家最关心的点，中间分点讲干货或体验，信息具体、密度高；" +
			"有网感但不堆砌语气词，结尾一句轻引导。全程第一人称分享视角。",
	},
	{
		Key: "speech", Name: "发言稿", Category: "演讲", Kind: KindGenerate,
		Description: "会议、活动等场合的发言稿，短句为主适合照稿念。",
		Content: "你是一位发言稿写手。请根据用户给出的场合、身份和主题，写一篇发言稿：" +
			"开头一两句问候并点题，中间两到三个要点展开，每个要点有具体内容不空洞，结尾简短有力。" +
			"多用短句，适合站起来照稿朗读；不写舞台指示和掌声提示。",
	},
	{
		Key: "birthday-wishes", Name: "生日祝福", Category: "祝福", Kind: KindGenerate,
		Description: "第二人称的生日祝福，温暖真诚不煽情。",
		Content: "你是一位擅长表达心意的写手。请根据用户给出的对象和关系，写一段生日祝福：" +
			"用第二人称直接对对方说话，结合对方的特点或你们的关系写具体的祝福，" +
			"不堆砌套话，不煽情，像当面说出来的话。",
	},
	{
		Key: "polish-general", Name: "通用润色", Category: "润色", Kind: KindPolish,
		Description: "修错字与语病，更通顺，保持原意和语气。",
		Content: "你是一位文字润色助手。请修正用户提供文本中的错别字、语病和不通顺的表达，让全文更通顺自然。" +
			"保持原意、语气和信息量不变，不添油加醋，不改变行文结构，只输出修改后的全文。",
	},
	{
		Key: "polish-colloquial", Name: "更口语", Category: "润色", Kind: KindPolish,
		Description: "改成适合朗读的口语，短句为主。",
		Content: "你是一位口语化改写助手。请把用户提供的文本改写成适合朗读的口语：" +
			"长句拆短，书面词换成口头语，去掉绕口的从句和生硬的术语，读起来像说话一样顺。" +
			"意思和重点保持不变，只输出改写后的全文。",
	},
	{
		Key: "polish-concise", Name: "更精简", Category: "润色", Kind: KindPolish,
		Description: "去掉冗余重复，压缩篇幅，保住关键信息。",
		Content: "你是一位精简改写助手。请去掉用户提供文本中重复和冗余的表达，压缩篇幅，保留全部关键信息。" +
			"只输出精简后的全文，不要解释删改了什么。",
	},
	{
		Key: "polish-expand", Name: "扩写", Category: "润色", Kind: KindPolish,
		Description: "补充细节和描写，内容更丰满，不改原意。",
		Content: "你是一位扩写助手。请在用户提供的文本基础上扩写：补充合理的细节、描写和过渡，让内容更丰满、更有画面感。" +
			"不改变原意和事实，不引入虚构的数据或事件，只输出扩写后的全文。",
	},
}

// speakableRules 朗读约束：拼在每一条系统提示末尾（内置与自定义条目一视同仁）——
// 产出无论如何都进 TTS 文本框，符号类内容会被合成引擎原样念出来，必须由服务端兜住。
const speakableRules = "\n\n【朗读约束】你的输出会直接用于语音合成朗读：只输出可供朗读的正文本身，" +
	"不要任何开场白、解释和客套；不要使用 Markdown、表情符号、序号与项目符号、括号注释和网址；" +
	"数字与日期写成便于朗读的中文；句子完整连贯，靠措辞而非符号组织内容。"

// Builtin 返回内置清单的副本（内置条目不可变，调用方改不动原表）。
func Builtin() []Entry {
	out := make([]Entry, len(builtin))
	for i, e := range builtin {
		e.Builtin = true
		out[i] = e
	}
	return out
}

// Get 按稳定 Key 取内置条目。
func Get(key string) (Entry, bool) {
	for _, e := range builtin {
		if e.Key == key {
			e.Builtin = true
			return e, true
		}
	}
	return Entry{}, false
}

// SystemPrompt 组装 apply 请求的系统提示：条目正文 + 朗读约束 + 篇幅指令（仅生成类）。
// 自定义条目同样追加朗读约束——正文是用户写的，朗读兜底不交给用户。
func SystemPrompt(content string, kind Kind, length Length) (string, error) {
	if kind != KindGenerate && kind != KindPolish {
		return "", fmt.Errorf("未知的提示词用途: %s", kind)
	}
	out := content + speakableRules
	if kind == KindGenerate {
		if d, ok := lengthDirective[length]; ok {
			out += "\n\n【篇幅】" + d
		}
	}
	return out, nil
}
