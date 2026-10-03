package subtitle

// 字幕翻译纯函数层:归一化比对、锚定回显校验、未翻译检测、分批。
// 协议移植自 SmartSub(design D4/D5):AI 逐条返回 {id:{src,tr}},src 回显与真实
// 原文相似度比对确定性检出「合并翻译导致的整体滑移」;检出的错位条目由引擎层
// 定点补翻(见 internal/translate)。

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// NormalizeForCompare 归一化:去全部空白与常见标点、小写、全角转半角(比对用)。
func NormalizeForCompare(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			continue
		case unicode.IsPunct(r), unicode.IsSymbol(r):
			continue
		}
		if r >= 0xFF01 && r <= 0xFF5E { // 全角 ASCII → 半角
			r -= 0xFEE0
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Similarity 归一化后 rune 级 Levenshtein 相似度(1-距离/较长边);双空为 1。
func Similarity(a, b string) float64 {
	ra, rb := []rune(NormalizeForCompare(a)), []rune(NormalizeForCompare(b))
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	longer := len(ra)
	if len(rb) > longer {
		longer = len(rb)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := 0; j <= len(rb); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			m := prev[j] + 1
			if cur[j-1]+1 < m {
				m = cur[j-1] + 1
			}
			if prev[j-1]+cost < m {
				m = prev[j-1] + cost
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(longer)
}

// BatchIndices 把 n 条(全局下标 0..n-1)按 size 切批;size<1 按 1。
func BatchIndices(n, size int) [][]int {
	if size < 1 {
		size = 1
	}
	out := make([][]int, 0, (n+size-1)/size)
	for i := 0; i < n; i += size {
		end := i + size
		if end > n {
			end = n
		}
		idx := make([]int, 0, end-i)
		for k := i; k < end; k++ {
			idx = append(idx, k)
		}
		out = append(out, idx)
	}
	return out
}

// EchoEntry 锚定协议单条:src 回显 + 译文。
type EchoEntry struct {
	SrcEcho string `json:"src"`
	Tr      string `json:"tr"`
}

// EchoVerdict 锚定校验结论:Accepted=全局下标→可信译文;Flagged=待定点补翻的全局下标。
type EchoVerdict struct {
	Accepted    map[int]string
	Flagged     []int
	EchoChecked int
}

// ValidateEcho 锚定校验:回显与原文归一化相似度 ≥ threshold 才采信;缺条目/空译文/
// 回显漂移均入 Flagged。resp 键为批内 1 起编号;非数字键与越界键忽略。
// 返回的 Flagged 升序且去重。
func ValidateEcho(srcs map[int]string, resp map[string]EchoEntry, threshold float64) EchoVerdict {
	v := EchoVerdict{Accepted: map[int]string{}}
	for id, e := range resp {
		var idx int
		if _, err := fmt.Sscanf(id, "%d", &idx); err != nil {
			continue // 非数字键忽略
		}
		idx-- // 批内 1 起编号 → 全局下标
		src, ok := srcs[idx]
		if !ok {
			continue // 越界键忽略
		}
		v.EchoChecked++
		if strings.TrimSpace(e.Tr) == "" || Similarity(src, e.SrcEcho) < threshold {
			v.Flagged = append(v.Flagged, idx)
			continue
		}
		v.Accepted[idx] = strings.TrimSpace(e.Tr)
	}
	for i := range srcs {
		if _, ok := v.Accepted[i]; !ok {
			v.Flagged = append(v.Flagged, i) // 缺条目也进 Flagged
		}
	}
	sort.Ints(v.Flagged)
	v.Flagged = dedupInts(v.Flagged)
	return v
}

// dedupInts 就地去重已排序切片。
func dedupInts(s []int) []int {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// ValidateSimple 旧协议兜底({id:译文}):条数齐且非空即采信,缺失/空入 Flagged。
func ValidateSimple(srcs map[int]string, resp map[string]string) EchoVerdict {
	v := EchoVerdict{Accepted: map[int]string{}}
	for i := range srcs {
		tr := strings.TrimSpace(resp[fmt.Sprint(i+1)])
		if tr == "" {
			v.Flagged = append(v.Flagged, i)
			continue
		}
		v.Accepted[i] = tr
	}
	sort.Ints(v.Flagged)
	return v
}

// IsSuspectedCopy 译文与原文高相似(≥0.9)即疑似未翻译;目标为 zh-Hant 时豁免
// (简→繁字形差异不足以判定,且逐字转换常被误判)。空译文/双短文本不判。
func IsSuspectedCopy(src, tr, tgtLang string) bool {
	if strings.EqualFold(strings.TrimSpace(tgtLang), "zh-Hant") {
		return false
	}
	ns, nt := NormalizeForCompare(src), NormalizeForCompare(tr)
	if len([]rune(ns)) < 2 || len([]rune(nt)) < 2 {
		return false
	}
	return Similarity(ns, nt) >= 0.9
}
