package store

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 翻译术语表的存取:AI 自动沉淀(UpsertGlossary/LoadGlossary)与设置页人工
// 增删改查(ListGlossaryRows/Create/Update/Delete)共用一表。读写失败由调用方
// 降级或回显,不阻断翻译。

// ErrGlossaryExists 同语言下原文词条已存在(人工新增/改词撞唯一约束)。
var ErrGlossaryExists = errors.New("该原文词条已存在")

// GlossaryPair 术语对(store 侧不依赖 internal/translate,由 service 层适配转换)。
type GlossaryPair struct {
	Src string
	Dst string
}

// UpsertGlossary AI 学习写入:同 (target_lang, src) 覆盖 dst 并刷新 updated_at
// (clause.OnConflict 全仓首例)。不修剪:人工维护 + AI 沉淀共存,丢数据不可接受;
// (target_lang, updated_at) 索引保证预热查询不随表增长变慢。
func (d *DB) UpsertGlossary(targetLang string, terms []GlossaryPair) error {
	if len(terms) == 0 {
		return nil
	}
	rows := make([]TranslateGlossary, 0, len(terms))
	for _, t := range terms {
		rows = append(rows, TranslateGlossary{TargetLang: targetLang, Src: t.Src, Dst: t.Dst})
	}
	return d.gorm.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "target_lang"}, {Name: "src"}},
		DoUpdates: clause.AssignmentColumns([]string{"dst", "updated_at"}),
	}).Create(&rows).Error
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

// ListGlossaryRows 人工维护视图(带 id 全字段):lang 空=全部语言,按最近使用在前。
func (d *DB) ListGlossaryRows(lang string) ([]TranslateGlossary, error) {
	q := d.gorm.Order("updated_at DESC, id DESC")
	if lang != "" {
		q = q.Where("target_lang = ?", lang)
	}
	var rows []TranslateGlossary
	return rows, q.Find(&rows).Error
}

// CreateGlossaryTerm 人工新增:同语言同原文已存在时报 ErrGlossaryExists(不静默覆盖,
// 人工输入要给明确反馈;AI 学习走 UpsertGlossary 才覆盖)。
func (d *DB) CreateGlossaryTerm(targetLang, src, dst string) (*TranslateGlossary, error) {
	var n int64
	if err := d.gorm.Model(&TranslateGlossary{}).
		Where("target_lang = ? AND src = ?", targetLang, src).Count(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrGlossaryExists
	}
	row := TranslateGlossary{TargetLang: targetLang, Src: src, Dst: dst}
	if err := d.gorm.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// UpdateGlossaryTerm 人工修改:按 id 整行覆盖;改出的 (lang, src) 与其他行冲突时报 ErrGlossaryExists。
func (d *DB) UpdateGlossaryTerm(id uint, targetLang, src, dst string) (*TranslateGlossary, error) {
	var row TranslateGlossary
	if err := d.gorm.First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var n int64
	if err := d.gorm.Model(&TranslateGlossary{}).
		Where("target_lang = ? AND src = ? AND id <> ?", targetLang, src, id).Count(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrGlossaryExists
	}
	row.TargetLang, row.Src, row.Dst = targetLang, src, dst
	if err := d.gorm.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// DeleteGlossaryTerm 人工删除;不存在报 ErrNotFound。
func (d *DB) DeleteGlossaryTerm(id uint) error {
	res := d.gorm.Delete(&TranslateGlossary{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
