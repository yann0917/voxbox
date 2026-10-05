package provider

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// ASR 系结果契约:Summary(JSON)的标题派生。火山/本地 ASR 工具的 ResultTitle
// 实现、实时字幕入库(live_save)与存量标题回填(store backfill)共用此契约。

// ResultTitleer 工具可选能力:从成功结果的 Summary(JSON)派生人类可读任务标题。
// 适用提交时无文本可派生的工具(如 ASR 用识别文本前缀替换文件名/URL 机器标题);
// 引擎在成功落库前调用,用户手改过标题(title_edited)则让位。
type ResultTitleer interface {
	ResultTitle(summaryJSON string) string
}

// ASRTitleFromSummary 从 ASR 任务 Summary 提取可读标题：text 键优先（本地/live 引擎
// 的纯文本），否则按序拼接 segments（火山系分句）取前缀。解析不出返回空串（调用方
// 保留原标题）。
func ASRTitleFromSummary(summaryJSON string) string {
	if strings.TrimSpace(summaryJSON) == "" {
		return ""
	}
	var sum struct {
		Text     string `json:"text"`
		Segments []struct {
			Text string `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(summaryJSON), &sum); err != nil {
		return ""
	}
	if strings.TrimSpace(sum.Text) != "" {
		return TitleFromText(sum.Text)
	}
	// 拼接分句到足够截断即止，标题口径=转写全文前 20 字（非首分句）。
	var b strings.Builder
	for _, seg := range sum.Segments {
		b.WriteString(seg.Text)
		if utf8.RuneCountInString(b.String()) > asrTitleMaxRunes {
			break
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		return TitleFromText(s)
	}
	return ""
}

// TitleFromText 识别文本 → 单行短标题：压平全部空白（换行/多空格→单空格）后截
// 20 rune 加省略号。ASR 完成时派生标题与实时字幕入库共用。
func TitleFromText(s string) string {
	runes := []rune(strings.Join(strings.Fields(s), " "))
	if len(runes) > asrTitleMaxRunes {
		return string(runes[:asrTitleMaxRunes]) + "…"
	}
	return string(runes)
}

// asrTitleMaxRunes 识别文本派生标题的截断长度：用户口径「前 20 个字」。
const asrTitleMaxRunes = 20
