// Package refine 录音笔记加工层的提示词组装：把 asr/minutes 成功任务的逐行转写
// （HH:MM:SS 说话人N：文本）交给内置三种模式（纪要/待办/日程）或自定义指令的
// system prompt。输出契约固定——summary/custom 为自由文本，todos/events 为纯
// JSON 数组，由 /api/refine handler 流式下发并合并落任务 Summary.refined。
package refine

import "strings"

// 加工模式：三种内置 + custom（指令由调用方给定，handler 校验非空）。
const (
	ModeSummary = "summary"
	ModeTodos   = "todos"
	ModeEvents  = "events"
	ModeCustom  = "custom"
)

// maxTranscriptRunes 转写长度上限（rune）：超出截断，防超长录音打爆模型上下文。
const maxTranscriptRunes = 30000

// truncatedSuffix 截断标记：明示模型与用户「所给非全文」。
const truncatedSuffix = "（转写过长，已截断）"

// ValidMode mode 是否为合法加工模式。
func ValidMode(mode string) bool {
	switch mode {
	case ModeSummary, ModeTodos, ModeEvents, ModeCustom:
		return true
	}
	return false
}

// 内置 system prompt（输出契约不可变）：summary/custom 产自由文本；todos/events
// 只准输出 JSON 数组——解析失败由 handler 原文兜底落盘，不报错给前端。
const (
	systemSummary = "你是会议/录音纪要助手。基于给定转写文本输出 markdown 纪要：一段话概述 + " +
		"要点列表（保留说话人归属）+ 明确结论与分歧。不要编造转写中没有的内容。"
	systemTodos = `从转写中提取全部待办事项，只输出 JSON 数组，元素形如 {"content":"事项",` +
		`"owner":"负责人，未提到则空串","due":"截止时间原文，未提到则空串"}。不要输出 JSON 以外的任何文字。`
	systemEvents = `从转写中提取日历级事件（会议/截止/约定时间），只输出 JSON 数组，元素形如 ` +
		`{"title":"事件名","start":"YYYY-MM-DD HH:mm","end":"YYYY-MM-DD HH:mm 或空串","description":"上下文一句话"}。` +
		`相对时间（如下周三）以录音日期为基准推算，录音日期会提供。不要输出 JSON 以外的任何文字。`
)

// BuildMessages 组装一次加工调用的 system 与 user 消息。instruction 仅 custom 模式
// 使用（原样作为 system）；transcript 为调用方渲染好的逐行转写文本，超过 30000 字符
// 截断并追加标记。events 模式的录音日期由 handler 附在转写头部（system 已声明锚点）。
func BuildMessages(mode, instruction, transcript string) (system, user string) {
	transcript = strings.TrimSpace(transcript)
	if runes := []rune(transcript); len(runes) > maxTranscriptRunes {
		transcript = string(runes[:maxTranscriptRunes]) + truncatedSuffix
	}
	switch mode {
	case ModeSummary:
		return systemSummary, "请基于以下录音转写文本输出纪要：\n\n" + transcript
	case ModeTodos:
		return systemTodos, "请从以下录音转写文本中提取待办事项：\n\n" + transcript
	case ModeEvents:
		return systemEvents, "请从以下录音转写文本中提取日历事件：\n\n" + transcript
	case ModeCustom:
		return instruction, transcript
	}
	return "", transcript // handler 已先行校验模式，防御性兜底
}
