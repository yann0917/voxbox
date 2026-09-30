// refine.go 字幕断句规范层：在句法聚合（句末标点）之上按可读性标准规整。
// 基准为 Netflix Timed Text Style Guide（简中专篇 + 通用要求）：
// 每行 16 字 × 2 行 → 单段上限按用户裁定取保守值 28 字；最短显示 5/6 秒（833ms）、
// 最长 7 秒、相邻间隔 ≈2 帧（83ms）、成人简中阅读速度上限 9 字/秒（仅标记不强制）。
package subtitle

import (
	"strings"
	"unicode/utf8"
)

// 断句规范常量（单一事实来源：本地 ASR、流式/长文本 TTS、字幕工坊文稿草稿共用）。
const (
	MaxSegChars     = 28  // 单段 rune 上限（保守值：双行 × 14 字，为 ASS 渲染留余量）
	MinDurMS        = 833 // 最短显示时长（5/6 秒）
	MaxDurMS        = 7000
	GapMS           = 83  // 相邻段最小间隔（≈2 帧）
	MaxCPS          = 9.0 // 阅读速度上限（成人简中，仅标记不强制）
	secondaryPuncts = "，、："
)

// RefineTokens 由 token/字级时间流构建符合断句规范的字幕段。句界优先取 sentenceEnds
// （显式句界，如长文本 TTS 的分句边界；与 words 一一对应），为 nil 时按 token 文本
// 含句末标点自动判定。流程：聚句 → 超长下切（次级标点优先，无则硬切）→ 过短并合
// （先向后再向前，合并后受 28 字与 CPS 双约束，两不可行则延长至最短时长）→ 时长钳制
// → 相邻间隙。
func RefineTokens(words []WordSpan, sentenceEnds []bool) []Segment {
	if len(words) == 0 {
		return nil
	}
	if sentenceEnds == nil {
		sentenceEnds = make([]bool, len(words))
		for i, w := range words {
			sentenceEnds[i] = strings.ContainsAny(w.Text, sentenceEndPuncts)
		}
	}
	// 聚句（每句收集 word 下标区间）
	type sentence struct {
		lo, hi int // words 下标 [lo,hi)
	}
	sentences := make([]sentence, 0, 4)
	lo := 0
	for i := range words {
		if sentenceEnds[i] {
			sentences = append(sentences, sentence{lo: lo, hi: i + 1})
			lo = i + 1
		}
	}
	if lo < len(words) {
		sentences = append(sentences, sentence{lo: lo, hi: len(words)})
	}

	// 超长下切（句内 word 序列切块）+ 产出句级段
	segs := make([]Segment, 0, len(sentences))
	for _, s := range sentences {
		for _, chunk := range splitByLimit(words[s.lo:s.hi]) {
			if len(chunk) == 0 {
				continue
			}
			segs = append(segs, Segment{
				Text:    joinWords(chunk),
				StartMS: chunk[0].StartMS,
				EndMS:   chunk[len(chunk)-1].EndMS,
			})
		}
	}

	// 过短并合：先向后（合并后 rune≤28 且 CPS≤9），再向前（同约束），两不可行则延长
	out := make([]Segment, 0, len(segs))
	for i := 0; i < len(segs); i++ {
		cur := segs[i]
		if cur.EndMS-cur.StartMS < MinDurMS {
			merged := false
			if i+1 < len(segs) && canMergeSegs(cur, segs[i+1]) {
				segs[i+1] = Segment{
					Text:    cur.Text + segs[i+1].Text,
					StartMS: cur.StartMS,
					EndMS:   segs[i+1].EndMS,
				}
				merged = true
			} else if len(out) > 0 && canMergeSegs(out[len(out)-1], cur) {
				prev := out[len(out)-1]
				out[len(out)-1] = Segment{Text: prev.Text + cur.Text, StartMS: prev.StartMS, EndMS: cur.EndMS}
				merged = true
			}
			if !merged {
				cur.EndMS = cur.StartMS + MinDurMS
			}
			if merged {
				continue // cur 已并入邻段
			}
		}
		out = append(out, cur)
	}

	// 时长钳制 + 相邻间隙
	for i := range out {
		if out[i].EndMS-out[i].StartMS > MaxDurMS {
			out[i].EndMS = out[i].StartMS + MaxDurMS
		}
		if i+1 < len(out) {
			if limit := out[i+1].StartMS - GapMS; out[i].EndMS > limit {
				if floor := out[i].StartMS + MinDurMS; limit > floor {
					out[i].EndMS = limit
				}
			}
		}
	}
	return out
}

// RefineSegments 对句级段（无字级时间戳，如长文本 TTS 上游未带 words）做规范规整：
// 段内时间按 rune 数比例线性分配出伪字级流后走 RefineTokens 同一管线。
func RefineSegments(segs []Segment) []Segment {
	var words []WordSpan
	for _, s := range segs {
		n := utf8.RuneCountInString(s.Text)
		if n == 0 || s.EndMS <= s.StartMS {
			words = append(words, WordSpan{Text: s.Text, StartMS: s.StartMS, EndMS: s.EndMS})
			continue
		}
		per := float64(s.EndMS-s.StartMS) / float64(n)
		runes := []rune(s.Text)
		for i, r := range runes {
			start := s.StartMS + int64(float64(i)*per)
			end := s.StartMS + int64(float64(i+1)*per)
			words = append(words, WordSpan{Text: string(r), StartMS: start, EndMS: end})
		}
	}
	// 段界即句界（显式传入，避免比例流里标点缺失时聚错句）
	if len(words) == 0 {
		return nil
	}
	ends := make([]bool, len(words))
	{
		i := 0
		for _, s := range segs {
			n := utf8.RuneCountInString(s.Text)
			if n == 0 {
				continue
			}
			ends[i+n-1] = true
			i += n
		}
	}
	return RefineTokens(words, ends)
}

// splitByLimit 句内切块：仅当句超过 MaxSegChars 时才切——累计 rune 达上限时，
// 优先回退到最后一个次级标点（，、：）之后（该切点前段必 ≤ 上限），无次级标点
// 则在当前 word 后硬切（word 为切分粒度，英文多字词不腰斩）。不超限的句子原样返回。
func splitByLimit(ws []WordSpan) [][]WordSpan {
	if rcount(ws) <= MaxSegChars {
		return [][]WordSpan{ws}
	}
	chunks := make([][]WordSpan, 0, 2)
	rest := ws
	for len(rest) > 0 {
		if rcount(rest) <= MaxSegChars {
			chunks = append(chunks, rest)
			break
		}
		var (
			runes       int
			secondaryAt = -1
		)
		cut := len(rest)
		for i, w := range rest {
			runes += utf8.RuneCountInString(w.Text)
			if strings.ContainsAny(w.Text, secondaryPuncts) {
				secondaryAt = i
			}
			if runes >= MaxSegChars {
				if secondaryAt >= 0 {
					cut = secondaryAt + 1 // 次级标点处切：前段必 ≤ 上限
				} else {
					cut = i + 1 // 无次级标点：word 后硬切
				}
				break
			}
		}
		if cut <= 0 {
			cut = 1
		}
		chunks = append(chunks, rest[:cut])
		rest = rest[cut:]
	}
	return chunks
}

// canMergeSegs 过短段并合约束：合并后 rune ≤ MaxSegChars、时长 ≤ MaxDurMS
// （防止并合吞掉长停顿后钳制砍掉后续句的语音窗）且 CPS ≤ MaxCPS。
func canMergeSegs(short, next Segment) bool {
	runes := utf8.RuneCountInString(short.Text) + utf8.RuneCountInString(next.Text)
	if runes > MaxSegChars {
		return false
	}
	dur := next.EndMS - short.StartMS
	if dur <= 0 {
		return true
	}
	if dur > MaxDurMS {
		return false
	}
	return float64(runes)/(float64(dur)/1000) <= MaxCPS
}

func joinWords(ws []WordSpan) string {
	var b strings.Builder
	for _, w := range ws {
		b.WriteString(w.Text)
	}
	return strings.TrimSpace(b.String())
}

func rcount(ws []WordSpan) int {
	n := 0
	for _, w := range ws {
		n += utf8.RuneCountInString(w.Text)
	}
	return n
}
