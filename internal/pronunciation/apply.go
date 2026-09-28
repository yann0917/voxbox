package pronunciation

import (
	"regexp"
	"strings"
)

// Apply 对 text 应用发音词典与 [[词|读音]] 行内覆盖，返回替换后文本。
//
// 合并优先级从低到高：全语言词条（language="*"）→ 请求语言匹配词条（同词覆盖全语言）
// → 行内 [[…]] 临时标注（永远赢）。请求语言为空/"auto" 时仅全语言词条生效。
//
// 匹配语义：
//   - 整词最长优先：长词条压过其前缀短词条（"Dr. Smith" 压过 "Dr"）；
//   - 大小写不敏感（拉丁词）；CJK 词条不做词边界检查（中文没有词边界，
//     「重庆」应能命中「重庆火锅」），拉丁词条两侧要求拉丁词边界（"cat"
//     不得误伤 "category"）；
//   - 单遍从左到右扫描：替换产物不再被扫描，读音里恰好含另一词条也不会
//     连锁替换（天然幂等）。
//
// 任何词典层故障静默降级为原文本，绝不阻断合成。
func Apply(text, language string) (out string) {
	out = text
	if text == "" {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			out = text
		}
	}()
	if d := buildDictionary(Default().entriesFor(langPrefix(language))); d != nil {
		out = d.replace(text)
	}
	return applyInline(out)
}

// langPrefix 请求语言 → 二字母小写前缀；""/"auto" → ""（仅全语言词条生效）。
func langPrefix(language string) string {
	p, err := NormalizeLanguage(language)
	if err != nil || p == "*" {
		return ""
	}
	return p
}

// entriesFor 按请求语言前缀折叠出 {词条折叠形: 替换写法}：全语言打底、语言词条覆盖。
func (s *Store) entriesFor(prefix string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	glob := map[string]string{}
	lang := map[string]string{}
	for _, e := range s.entries {
		if !e.Enabled {
			continue
		}
		key := strings.ToLower(e.Term)
		switch {
		case e.Language == "*":
			glob[key] = e.Replacement
		case prefix != "" && e.Language == prefix:
			lang[key] = e.Replacement
		}
	}
	for k, v := range lang {
		glob[k] = v
	}
	return glob
}

// dictEntry 一条编译后的匹配规则。
type dictEntry struct {
	folded     []rune // 词条小写折叠形
	repl       string
	boundStart bool // 首字符是拉丁词字符时要求左边界
	boundEnd   bool // 尾字符是拉丁词字符时要求右边界
}

// dictionary 分桶词典：按首 rune（折叠后）分桶，桶内词条按长度降序（最长优先）。
type dictionary struct {
	buckets map[rune][]dictEntry
}

func buildDictionary(merged map[string]string) *dictionary {
	if len(merged) == 0 {
		return nil
	}
	d := &dictionary{buckets: map[rune][]dictEntry{}}
	for term, repl := range merged {
		folded := []rune(strings.ToLower(term))
		if len(folded) == 0 {
			continue
		}
		e := dictEntry{
			folded:     folded,
			repl:       repl,
			boundStart: isLatinWordRune(folded[0]),
			boundEnd:   isLatinWordRune(folded[len(folded)-1]),
		}
		head := folded[0]
		d.buckets[head] = append(d.buckets[head], e)
	}
	for head := range d.buckets {
		es := d.buckets[head]
		// 插入排序：桶内条目少，且需稳定的长者优先
		for i := 1; i < len(es); i++ {
			for j := i; j > 0 && len(es[j].folded) > len(es[j-1].folded); j-- {
				es[j], es[j-1] = es[j-1], es[j]
			}
		}
	}
	return d
}

// replace 单遍 rune 扫描替换。命中后跳过整个词条（产物不重扫）。
func (d *dictionary) replace(text string) string {
	runes := []rune(text)
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(runes); {
		if repl, n, ok := d.match(runes, i); ok {
			b.WriteString(repl)
			i += n
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

// match 在 runes[i:] 上尝试命中一条规则：桶内长者优先，全部校验通过才返回。
func (d *dictionary) match(runes []rune, i int) (string, int, bool) {
	for _, e := range d.buckets[foldRune(runes[i])] {
		n := len(e.folded)
		if i+n > len(runes) {
			continue
		}
		if !equalFold(runes[i:i+n], e.folded) {
			continue
		}
		if e.boundStart && i > 0 && isLatinWordRune(runes[i-1]) {
			continue
		}
		if e.boundEnd && i+n < len(runes) && isLatinWordRune(runes[i+n]) {
			continue
		}
		return e.repl, n, true
	}
	return "", 0, false
}

// equalFold 逐 rune 小写比较。
func equalFold(a, b []rune) bool {
	for i := range a {
		if foldRune(a[i]) != b[i] {
			return false
		}
	}
	return true
}

// 行内一次性标注：[[gif|jiff]] → 本处 gif 读作 jiff（竖线前是给作者看的原词标记，
// 只有竖线后进合成）；[[Nuh-VAD-uh]] → 括号内容本身就是要读的文本（括号剥掉）。
// 在词典替换之后解析，对这一处出现永远赢过任何词条。单层方括号（[pause]、
// [voice:] 等）不受影响——正则要求双侧双括号。{0,256} 封顶防止未闭合的
// 长串 "[" 拖出多项式扫描。
var inlineRe = regexp.MustCompile(`\[\[([^\]]{0,256})\]\]`)

func applyInline(text string) string {
	if !strings.Contains(text, "[[") {
		return text
	}
	return inlineRe.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[2 : len(m)-2]
		if idx := strings.Index(inner, "|"); idx >= 0 {
			return inner[idx+1:]
		}
		return inner
	})
}
