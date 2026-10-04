package store

import (
	"gorm.io/gorm/clause"
)

// 翻译术语表(AI 自动沉淀)的存取。机器维护的全局内容,无 user 隔离、无 UI;
// 读写失败由调用方降级为会话内记忆,不阻断翻译。

// glossaryKeepMax 每目标语言保留条数上限(按最近使用修剪)。
const glossaryKeepMax = 1000

// GlossaryPair 术语对(store 侧不依赖 internal/translate,由 service 层适配转换)。
type GlossaryPair struct {
	Src string
	Dst string
}

// UpsertGlossary 合并写入:同 (target_lang, src) 覆盖 dst 并刷新 updated_at
// (全仓首个 clause.OnConflict 用例);写入后修剪超出上限的最旧条目。
func (d *DB) UpsertGlossary(targetLang string, terms []GlossaryPair) error {
	if len(terms) == 0 {
		return nil
	}
	rows := make([]TranslateGlossary, 0, len(terms))
	for _, t := range terms {
		rows = append(rows, TranslateGlossary{TargetLang: targetLang, Src: t.Src, Dst: t.Dst})
	}
	if err := d.gorm.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "target_lang"}, {Name: "src"}},
		DoUpdates: clause.AssignmentColumns([]string{"dst", "updated_at"}),
	}).Create(&rows).Error; err != nil {
		return err
	}
	return d.gorm.Where("target_lang = ? AND id NOT IN (?)", targetLang,
		d.gorm.Model(&TranslateGlossary{}).Select("id").Where("target_lang = ?", targetLang).
			Order("updated_at DESC, id DESC").Limit(glossaryKeepMax),
	).Delete(&TranslateGlossary{}).Error
}

// LoadGlossary 取最近 limit 条(最近使用在前);limit<=0 不限制。
func (d *DB) LoadGlossary(targetLang string, limit int) ([]GlossaryPair, error) {
	q := d.gorm.Where("target_lang = ?", targetLang).Order("updated_at DESC, id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []TranslateGlossary
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]GlossaryPair, 0, len(rows))
	for _, r := range rows {
		out = append(out, GlossaryPair{Src: r.Src, Dst: r.Dst})
	}
	return out, nil
}
