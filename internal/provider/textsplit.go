package provider

import "strings"

// SplitText 将文本切分为长度不超过 maxLen（按 rune 计）的段落：
// 先按句末标点（。！？!?与换行）切句，再贪心组装；单句超长先按次级标点
// （，,、；;：:）细切，仍超长按 maxLen 硬切——返回的任何一段都不超过 maxLen，
// 上游「单次请求字符上限」类硬限制依赖此契约。
func SplitText(text string, maxLen int) []string {
	runes := []rune(strings.TrimSpace(text))
	if maxLen <= 0 || len(runes) <= maxLen {
		if len(runes) == 0 {
			return nil
		}
		return []string{strings.TrimSpace(text)}
	}

	var segs []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
			curLen = 0
		}
	}
	for _, sent := range splitSentences(runes) {
		if len(sent) > maxLen {
			// 单句超长：先冲掉已攒内容，再细切这句（次级标点 → 硬切兜底）
			flush()
			for _, piece := range splitLongSentence(sent, maxLen) {
				segs = append(segs, piece)
			}
			continue
		}
		if curLen+len(sent) > maxLen {
			flush()
		}
		cur.WriteString(string(sent))
		curLen += len(sent)
	}
	flush()
	return segs
}

// splitSentences 按句末标点切句，标点跟随前句（含换行）。
func splitSentences(runes []rune) [][]rune {
	sentEnds := "。！？!?\n"
	var sents [][]rune
	start := 0
	for i, r := range runes {
		if strings.ContainsRune(sentEnds, r) {
			sents = append(sents, runes[start:i+1])
			start = i + 1
		}
	}
	if start < len(runes) {
		sents = append(sents, runes[start:])
	}
	return sents
}

// subSentPuncts 句内次级标点：长句细切的落点（顿号/逗号/分号/冒号）。
const subSentPuncts = "，,、；;：:"

// splitLongSentence 把超长句切成不超过 maxLen 的段落：先按次级标点切，
// 得到的子句仍超长（无标点的长串）按 maxLen 硬切。
func splitLongSentence(sent []rune, maxLen int) []string {
	var pieces []string
	start := 0
	for i, r := range sent {
		if strings.ContainsRune(subSentPuncts, r) {
			pieces = append(pieces, string(sent[start:i+1]))
			start = i + 1
		}
	}
	if start < len(sent) {
		pieces = append(pieces, string(sent[start:]))
	}

	var out []string
	for _, p := range pieces {
		rs := []rune(p)
		for len(rs) > maxLen {
			out = append(out, string(rs[:maxLen]))
			rs = rs[maxLen:]
		}
		if len(rs) > 0 {
			out = append(out, string(rs))
		}
	}
	return out
}
