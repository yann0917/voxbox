package volcengine

import "strings"

// splitText 将文本按句末标点切句，再贪心组装为不超过 maxLen（按 rune 计）的段落。
func splitText(text string, maxLen int) []string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxLen {
		return []string{strings.TrimSpace(text)}
	}
	sentEnds := "。！？!?\n"
	var sentences []string
	start := 0
	for i, r := range runes {
		if strings.ContainsRune(sentEnds, r) {
			sentences = append(sentences, string(runes[start:i+1]))
			start = i + 1
		}
	}
	if start < len(runes) {
		sentences = append(sentences, string(runes[start:]))
	}

	var segs []string
	var cur strings.Builder
	curLen := 0
	for _, s := range sentences {
		sLen := len([]rune(s))
		if sLen > maxLen {
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
				curLen = 0
			}
			segs = append(segs, s) // 单句超长：整句独立成段
			continue
		}
		if curLen+sLen > maxLen {
			segs = append(segs, cur.String())
			cur.Reset()
			curLen = 0
		}
		cur.WriteString(s)
		curLen += sLen
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}
