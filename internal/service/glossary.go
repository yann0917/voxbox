package service

// 翻译术语表:GlossaryStore 适配(AI 自动沉淀,字幕翻译引擎消费) + 人工增删改查
// (设置页维护)。适配层顺带完成 store→translate 的类型转换,保持 store 包不依赖
// internal/translate。CLI 不传 store(纯内存)。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/translate"
)

// glossaryTermMaxRunes 人工输入的单词条长度上限(远宽于 AI 学习的 32/64,给手写留余量)。
const (
	glossarySrcMaxRunes = 200
	glossaryDstMaxRunes = 500
)

// TranslateGlossaryStore 字幕 AI 翻译术语表的持久化适配器;db 未开(理论不可达)返回 nil。
func (s *Service) TranslateGlossaryStore() translate.GlossaryStore {
	if s.db == nil {
		return nil
	}
	return glossaryStore{db: s.db}
}

type glossaryStore struct {
	db *store.DB
}

func (g glossaryStore) LoadGlossary(targetLang string, limit int) ([]translate.GlossaryTerm, error) {
	pairs, err := g.db.LoadGlossary(targetLang, limit)
	if err != nil {
		return nil, err
	}
	out := make([]translate.GlossaryTerm, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, translate.GlossaryTerm{Src: p.Src, Dst: p.Dst})
	}
	return out, nil
}

func (g glossaryStore) UpsertGlossary(targetLang string, terms []translate.GlossaryTerm) error {
	pairs := make([]store.GlossaryPair, 0, len(terms))
	for _, t := range terms {
		pairs = append(pairs, store.GlossaryPair{Src: t.Src, Dst: t.Dst})
	}
	return g.db.UpsertGlossary(targetLang, pairs)
}

// —— 人工维护(设置页)——

// ListGlossary 术语全量列表(lang 空=全部语言),按最近使用在前。
func (s *Service) ListGlossary(lang string) ([]store.TranslateGlossary, error) {
	return s.db.ListGlossaryRows(lang)
}

// normalizeGlossaryInput 去空白并校验:目标语言须在 32 语种清单内,原文/译文非空且不超长。
func normalizeGlossaryInput(lang, src, dst string) (string, string, string, error) {
	lang, src, dst = strings.TrimSpace(lang), strings.TrimSpace(src), strings.TrimSpace(dst)
	if lang == "" {
		return "", "", "", errors.New("缺少目标语言")
	}
	valid := false
	for _, l := range volcengine.MTLanguages() {
		if l.Code == lang {
			valid = true
			break
		}
	}
	if !valid {
		return "", "", "", fmt.Errorf("目标语言 %q 不在支持清单内", lang)
	}
	if src == "" || dst == "" {
		return "", "", "", errors.New("原文与译文都必填")
	}
	if len([]rune(src)) > glossarySrcMaxRunes || len([]rune(dst)) > glossaryDstMaxRunes {
		return "", "", "", errors.New("词条过长:原文不超过 200 字,译文不超过 500 字")
	}
	return lang, src, dst, nil
}

// CreateGlossaryTerm 人工新增;同语言同原文已存在报 store.ErrGlossaryExists。
func (s *Service) CreateGlossaryTerm(lang, src, dst string) (*store.TranslateGlossary, error) {
	lang, src, dst, err := normalizeGlossaryInput(lang, src, dst)
	if err != nil {
		return nil, err
	}
	return s.db.CreateGlossaryTerm(lang, src, dst)
}

// UpdateGlossaryTerm 人工修改;词条不存在报 store.ErrNotFound,撞唯一约束报 ErrGlossaryExists。
func (s *Service) UpdateGlossaryTerm(id uint, lang, src, dst string) (*store.TranslateGlossary, error) {
	lang, src, dst, err := normalizeGlossaryInput(lang, src, dst)
	if err != nil {
		return nil, err
	}
	return s.db.UpdateGlossaryTerm(id, lang, src, dst)
}

// DeleteGlossaryTerm 人工删除;不存在报 store.ErrNotFound。
func (s *Service) DeleteGlossaryTerm(id uint) error {
	return s.db.DeleteGlossaryTerm(id)
}
