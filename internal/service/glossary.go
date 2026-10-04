package service

// 翻译术语表(AI 自动沉淀)的 GlossaryStore 适配:把 internal/translate 的持久化接口
// 落到 store 的 GlossaryPair 存取上,顺带完成 store→translate 的类型转换,
// 保持 store 包不依赖 internal/translate。仅 server 接线;CLI 不传(纯内存)。

import (
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/translate"
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
