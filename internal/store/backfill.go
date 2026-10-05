package store

import (
	"fmt"

	"github.com/yann0917/voxbox/internal/provider"
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
		title := provider.ASRTitleFromSummary(t.Summary)
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
