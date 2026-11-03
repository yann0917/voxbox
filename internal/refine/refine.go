// Package refine 录音笔记加工层的提示词组装：把 asr/minutes 成功任务的逐行转写
// （HH:MM:SS 说话人N：文本）交给内置模式（纪要/待办/日程/导图）或自定义指令的
// system prompt。输出契约固定——minutes/todos/events 为纯 JSON（minutes 为对象、
// 后两者为数组），mindmap/custom 为自由文本，由 /api/refine handler 流式下发并
// 合并落任务 Summary.refined。minutes 的条目带 evidence 转写行号，handler 解析成
// 时间戳后落盘（契约见 docs/json-contract.md）。
package refine

import "strings"

// 加工模式：四种内置 + custom（指令由调用方给定，handler 校验非空）。
// summary（自由文本总结）已被 minutes（结构化纪要）取代：模式同位替换，旧任务
// 落盘的 refined.summary 由前端只读兼容展示。
const (
	ModeMinutes = "minutes"
	ModeTodos   = "todos"
	ModeEvents  = "events"
	ModeMindmap = "mindmap"
	ModeCustom  = "custom"
)

// maxTranscriptRunes 转写长度上限（rune）：超出截断，防超长录音打爆模型上下文。
const maxTranscriptRunes = 30000

// truncatedSuffix 截断标记：明示模型与用户「所给非全文」。
const truncatedSuffix = "（转写过长，已截断）"

// ValidMode mode 是否为合法加工模式。
func ValidMode(mode string) bool {
	switch mode {
	case ModeMinutes, ModeTodos, ModeEvents, ModeMindmap, ModeCustom:
		return true
	}
	return false
}

// 内置 system prompt（输出契约不可变）：minutes/todos/events 只准输出 JSON——
// 解析失败由 handler 原文兜底落盘，不报错给前端；mindmap/custom 产自由文本。
const (
	systemMinutes = `你是会议纪要助手。基于给定转写输出结构化会议纪要，只输出 JSON 对象，` +
		`不要输出 JSON 以外的任何文字。结构：
{"title":"会议短标题","summary":"一段话整体概述",
 "topics":[{"title":"议题名","summary":"一句话小结","points":[{"text":"要点","evidence":["L12"]}],"evidence":["L10"]}],
 "conclusions":[{"text":"结论","owner":"负责人或空串","evidence":["L30"]}],
 "todos":[{"content":"待办","owner":"负责人或空串","due":"截止时间原文或空串","evidence":["L40"]}],
 "terms":[{"name":"术语规范写法","note":"一句话说明","heard":["转写中的听错写法"]}]}
规则：
- 转写行首是 [L行号 时间码 说话人]：文本。evidence 填支撑该条内容的转写行号数组（如 ["L4","L7"]），` +
		`除 terms 外每条要点/结论/待办必须携带；topics 的 title/summary 不带 evidence
- topics 数量与会议时长匹配（约每 3 分钟 1 个，最多 8 个），每个话题的 points 不超过 4 条；` +
		`conclusions 不超过 5 条；terms 不超过 10 条
- terms 收录转写里识别错误的专有名词/人名/术语：name 用正确写法，heard 列出转写中的错误写法；` +
		`没有听错就不收录
- 不得编造转写里没有的内容，每条要点与结论必须能在 evidence 指向的行里找到依据`
	systemTodos = `从转写中提取全部待办事项，只输出 JSON 数组，元素形如 {"content":"事项",` +
		`"owner":"负责人，未提到则空串","due":"截止时间原文，未提到则空串"}。不要输出 JSON 以外的任何文字。`
	systemEvents = `从转写中提取日历级事件（会议/截止/约定时间），只输出 JSON 数组，元素形如 ` +
		`{"title":"事件名","start":"YYYY-MM-DD HH:mm","end":"YYYY-MM-DD HH:mm 或空串","description":"上下文一句话"}。` +
		`相对时间（如下周三）以录音日期为基准推算，录音日期会提供。不要输出 JSON 以外的任何文字。`
	systemMindmap = `你是视频/录音内容结构化助手。基于给定转写输出内容思维导图，只输出嵌套无序列表：` +
		`每行以 "- " 开头，子级用两个空格缩进，不得使用标题、序号或代码块。第一层 3~7 个主题分支，` +
		`每个节点一句话（不超过 20 字），关键结论与数据放对应分支。只依据转写内容，不要编造。`
)

// systemMinutesIncremental 实时纪要的增量更新提示词：输入给出当前纪要与新增行，
// 输出更新后的完整纪要 JSON（整体覆盖而非片段），已有条目保持行文稳定。
const systemMinutesIncremental = systemMinutes + `
这是进行中会议的增量更新：输入给出当前已有纪要与新增转写行。输出更新后的完整 JSON（整体覆盖，不是增量片段）：
- 已有条目无新证据时原样保留，不要改写已有行文
- 每次最多新增 1 个话题；当前最新话题开始不足 2 分钟时不得开新话题，新内容并入现有话题
- 新条目的 evidence 按全局行号给`

// BuildMessages 组装一次加工调用的 system 与 user 消息。instruction 仅 custom 模式
// 使用（原样作为 system）；transcript 为调用方渲染好的逐行转写文本（minutes 模式
// 为带行号形态），超过 30000 字符截断并追加标记。events 模式的录音日期由 handler
// 附在转写头部（system 已声明锚点）。
func BuildMessages(mode, instruction, transcript string) (system, user string) {
	transcript = strings.TrimSpace(transcript)
	if runes := []rune(transcript); len(runes) > maxTranscriptRunes {
		transcript = string(runes[:maxTranscriptRunes]) + truncatedSuffix
	}
	switch mode {
	case ModeMinutes:
		return systemMinutes, "请基于以下带行号的录音转写文本输出会议纪要：\n\n" + transcript
	case ModeTodos:
		return systemTodos, "请从以下录音转写文本中提取待办事项：\n\n" + transcript
	case ModeEvents:
		return systemEvents, "请从以下录音转写文本中提取日历事件：\n\n" + transcript
	case ModeMindmap:
		return systemMindmap, "请把以下录音/视频转写整理成思维导图：\n\n" + transcript
	case ModeCustom:
		return instruction, transcript
	}
	return "", transcript // handler 已先行校验模式，防御性兜底
}

// BuildMinutesIncremental 组装实时纪要的增量更新调用：notesJSON 为当前纪要 JSON
// （空串=首稿，走全量提示词），transcript 为「上下文行 + 新增行」的行号转写
// （行号为会话级全局行号，evidence 由此可回链）。
func BuildMinutesIncremental(notesJSON, transcript string) (system, user string) {
	transcript = strings.TrimSpace(transcript)
	if runes := []rune(transcript); len(runes) > maxTranscriptRunes {
		transcript = string(runes[:maxTranscriptRunes]) + truncatedSuffix
	}
	if notesJSON == "" {
		return systemMinutes, "请基于以下带行号的录音转写文本输出会议纪要：\n\n" + transcript
	}
	return systemMinutesIncremental, "当前纪要：\n" + notesJSON + "\n\n新增转写：\n" + transcript
}
