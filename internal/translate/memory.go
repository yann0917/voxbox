package translate

// 跨批翻译记忆:场景摘要(仅会话内)+ 术语表(经 GlossaryStore 持久化,跨任务沉淀)。
// AI 源每批首轮响应顺带回传 summary/glossary 增量,Learn 合并后供后续批次 prompt 注入,
// 实现长片人名/称谓译法前后一致;store 为 nil 时纯内存(CLI 路径)。
// 术语表为机器自动生成与维护,无 UI;与 config.yaml 手工 dicts 资产互不相干。

import "strings"

// GlossaryTerm 术语对:Src 原文词,Dst 统一译法。
type GlossaryTerm struct {
	Src string
	Dst string
}

// GlossaryStore 术语表持久化接口(按目标语言隔离)。store 为 nil = 纯内存。
// 实现见 service 层适配器;失败一律不阻断翻译(降级为当次会话内记忆)。
type GlossaryStore interface {
	// LoadGlossary 取最近 limit 条(按最近使用在前返回)。
	LoadGlossary(targetLang string, limit int) ([]GlossaryTerm, error)
	// UpsertGlossary 合并写入:同 (targetLang, Src) 覆盖 Dst 并刷新时间。
	UpsertGlossary(targetLang string, terms []GlossaryTerm) error
}

const (
	glossaryInjectMax = 200      // prompt 注入条数上限
	glossaryMaxBytes  = 8 * 1024 // prompt 注入字节预算(含格式开销)
	summaryMaxRunes   = 200      // 场景摘要截断
	termSrcMaxRunes   = 32       // 单词条原文长度上限(过滤整句误入)
	termDstMaxRunes   = 64       // 单词条译文长度上限
)

// TranslationMemory 单次 Run 内的跨批记忆;Glossary 按新旧排序(旧在前,新在后)。
type TranslationMemory struct {
	Summary  string
	Glossary []GlossaryTerm
	store    GlossaryStore
	lang     string
}

func newTranslationMemory(store GlossaryStore, targetLang string) *TranslationMemory {
	return &TranslationMemory{store: store, lang: targetLang}
}

// load 预热:从 store 取最近 glossaryInjectMax 条反转成旧→新;失败降级纯内存。
func (m *TranslationMemory) load() {
	if m.store == nil {
		return
	}
	terms, err := m.store.LoadGlossary(m.lang, glossaryInjectMax)
	if err != nil || len(terms) == 0 {
		return
	}
	for i, j := 0, len(terms)-1; i < j; i, j = i+1, j-1 {
		terms[i], terms[j] = terms[j], terms[i]
	}
	m.Glossary = terms
}

// Learn 合并本批学到的摘要与术语并落库:同词覆盖译法并移到最新位;
// 非法项(空/超长/src=dst)静默忽略;落库失败不阻断翻译。
func (m *TranslationMemory) Learn(summary string, terms []GlossaryTerm) {
	if s := truncateRunes(strings.TrimSpace(summary), summaryMaxRunes); s != "" {
		m.Summary = s
	}
	var learned []GlossaryTerm
	for _, t := range terms {
		src, dst := strings.TrimSpace(t.Src), strings.TrimSpace(t.Dst)
		// 非法项静默忽略:空、超长(整句误入,截断会产生错误的部分词)、src=dst(无需约定)
		if src == "" || dst == "" || src == dst ||
			len([]rune(src)) > termSrcMaxRunes || len([]rune(dst)) > termDstMaxRunes {
			continue
		}
		pos := -1
		for i := range m.Glossary {
			if m.Glossary[i].Src == src {
				pos = i
				break
			}
		}
		if pos >= 0 {
			m.Glossary[pos].Dst = dst
			t := m.Glossary[pos]
			m.Glossary = append(m.Glossary[:pos], m.Glossary[pos+1:]...)
			m.Glossary = append(m.Glossary, t)
		} else {
			m.Glossary = append(m.Glossary, GlossaryTerm{Src: src, Dst: dst})
		}
		learned = append(learned, GlossaryTerm{Src: src, Dst: dst})
	}
	if m.store != nil && len(learned) > 0 {
		_ = m.store.UpsertGlossary(m.lang, learned)
	}
}

// injectGlossary 从最新往回套条数/字节预算,返回旧→新顺序供 prompt 呈现。
func injectGlossary(all []GlossaryTerm) []GlossaryTerm {
	budget := glossaryMaxBytes
	var out []GlossaryTerm
	for i := len(all) - 1; i >= 0 && len(out) < glossaryInjectMax; i-- {
		cost := len(all[i].Src) + len(all[i].Dst) + 8
		if cost > budget {
			break
		}
		budget -= cost
		out = append(out, all[i])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
