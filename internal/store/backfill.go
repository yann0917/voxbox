package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// backfillTaskTitles 一次性回填存量 ASR 任务的机器标题（2026-10-03 引入「识别文本
// 前缀派生标题」前的三类自动标题不可读：实时字幕+时间、浏览器录音文件名、产物联动
// 的产物 id 前缀），用识别文本前缀重写。启动时在 Open 内执行：
//   - 幂等：重写后的标题来自转写文本，天然不再命中旧模式（极端同文重复执行结果不变）；
//   - 安全：title_edited=0 才参与——用户手改的标题一律不动；解析不出文本的跳过保留原样。
func (d *DB) backfillTaskTitles() error {
	var tasks []Task
	if err := d.gorm.Where(
		"tool = ? AND status = ? AND title_edited = ? AND (title LIKE ? OR title LIKE ? OR title LIKE ?)",
		"asr", StatusSucceeded, false, "实时字幕 %", "录音-%", "产物 %",
	).Find(&tasks).Error; err != nil {
		return err
	}
	for i := range tasks {
		t := &tasks[i]
		title := ASRTitleFromSummary(t.Summary)
		if title == "" || title == t.Title {
			continue
		}
		if err := d.gorm.Model(&Task{}).Where("id = ?", t.ID).
			Update("title", title).Error; err != nil {
			return fmt.Errorf("回填任务 %s 标题失败: %w", t.ID, err)
		}
	}
	return nil
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
