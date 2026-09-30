package subtitle

import "testing"

// w 便捷构造 WordSpan（end 由调用方给，便于控制时长）。
func w(text string, start, end int64) WordSpan {
	return WordSpan{Text: text, StartMS: start, EndMS: end}
}

// TestRefineSplitLong 超长句在次级标点处下切（28 字上限），各段时间取字级实际时间。
func TestRefineSplitLong(t *testing.T) {
	// 一句 34 字（含逗号两个次级切点），字级每字 100ms
	words := []WordSpan{}
	text := []rune("今天天气真好，我们一起去公园散步吧，顺便买点吃的和喝的，然后高高兴兴回家。")
	for i, r := range text {
		words = append(words, w(string(r), int64(i)*100, int64(i+1)*100))
	}
	segs := RefineTokens(words, nil)
	if len(segs) < 2 {
		t.Fatalf("超长句应被下切成多段: %d", len(segs))
	}
	for i, seg := range segs {
		if n := len([]rune(seg.Text)); n > MaxSegChars {
			t.Errorf("段 %d 超 %d 字上限(%d): %q", i, MaxSegChars, n, seg.Text)
		}
	}
	// 首段应在第一个逗号处收（次级标点随前段）
	if segs[0].Text[len(segs[0].Text)-len("，"):] != "，" {
		t.Fatalf("首段应在次级标点处收尾: %q", segs[0].Text)
	}
	// 时间轴单调且取字级实际时间
	if segs[0].StartMS != 0 || segs[len(segs)-1].EndMS != int64(len(text))*100 {
		t.Fatalf("时间轴不符: 首=%d 末=%d", segs[0].StartMS, segs[len(segs)-1].EndMS)
	}
}

// TestRefineHardCut 无次级标点的超长句按上限硬切（word 粒度）。
func TestRefineHardCut(t *testing.T) {
	words := []WordSpan{}
	text := []rune("一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十") // 32 字
	for i, r := range text {
		words = append(words, w(string(r), int64(i)*100, int64(i+1)*100))
	}
	segs := RefineTokens(words, nil)
	if len(segs) < 2 {
		t.Fatalf("无标点超长句应硬切: %d", len(segs))
	}
	for i, seg := range segs {
		if n := len([]rune(seg.Text)); n > MaxSegChars {
			t.Errorf("段 %d 超 %d 字(%d)", i, MaxSegChars, n)
		}
	}
}

// TestRefineMergeShort 过短段先向后并合（合并后 28 字与 CPS 双约束通过）。
func TestRefineMergeShort(t *testing.T) {
	words := []WordSpan{
		w("好", 0, 150), w("。", 150, 160), // 160ms < 833ms：过短
		w("今", 400, 500), w("天", 500, 600), w("很", 600, 700),
		w("开", 700, 800), w("心", 800, 900), w("。", 900, 910),
	}
	segs := RefineTokens(words, nil)
	if len(segs) != 1 {
		t.Fatalf("过短段应并入后段成一句: %+v", segs)
	}
	if segs[0].Text != "好。今天很开心。" {
		t.Fatalf("并合后文本不符: %q", segs[0].Text)
	}
	if segs[0].StartMS != 0 || segs[0].EndMS != 910 {
		t.Fatalf("并合后时间不符: %+v", segs[0])
	}
}

// TestRefineMergeShortFallback 无法向后并合（后段合并后超 28 字/CPS 超限）→ 并入前段。
func TestRefineMergeShortFallback(t *testing.T) {
	// 首句 27 字（不可再并后段），尾段「中。」160ms 过短：
	// 向后并（无后段）→ 向前并：合并后 28 字、时长 2710ms → CPS 10.3 超限失败
	// → 两不可行则延长至 833ms
	long := []rune("今天天气真好我们一起去公园散步吧顺便买点吃的喝的。")
	words := []WordSpan{}
	for i, r := range long {
		words = append(words, w(string(r), int64(i)*100, int64(i+1)*100))
	}
	words = append(words, w("中", 2700, 2780), w("。", 2780, 2800))
	segs := RefineTokens(words, nil)
	// 尾段「中。」时长 20ms 过短：向后无段、向前 CPS 超限 → 延长至 833ms 保留独立
	last := segs[len(segs)-1]
	if last.Text != "中。" {
		t.Fatalf("尾段文本不符: %+v", last)
	}
	if d := last.EndMS - last.StartMS; d < MinDurMS {
		t.Fatalf("不可并合的过短段应延长至最短时长: %+v", last)
	}
}

// TestRefineClampAndGap 超 7 秒钳制 + 相邻间隙 ≥83ms。
func TestRefineClampAndGap(t *testing.T) {
	words := []WordSpan{
		w("第", 0, 100), w("一", 100, 200), w("句", 200, 300), w("。", 300, 8000),
		w("第", 8000, 8100), w("二", 8100, 8200), w("句", 8200, 8300), w("。", 8300, 8400),
	}
	segs := RefineTokens(words, nil)
	if len(segs) != 2 {
		t.Fatalf("应两句: %+v", segs)
	}
	if d := segs[0].EndMS - segs[0].StartMS; d > MaxDurMS {
		t.Fatalf("首段应钳制到 %dms,实际 %d", MaxDurMS, d)
	}
	if gap := segs[1].StartMS - segs[0].EndMS; gap < GapMS {
		t.Fatalf("相邻间隙应 ≥%dms,实际 %d", GapMS, gap)
	}
}

// TestRefineSegmentsProportional 无字级的句级段（长文本 TTS 兜底）按字符比例规整。
func TestRefineSegmentsProportional(t *testing.T) {
	segs := RefineSegments([]Segment{
		{Text: "这是一个特别长的句子中间没有任何标点需要按照二十八个字符的上限硬切开", StartMS: 0, EndMS: 6000},
		{Text: "短句。", StartMS: 6100, EndMS: 6200},
	})
	if len(segs) < 2 {
		t.Fatalf("超长段应被比例下切: %+v", segs)
	}
	for i, seg := range segs {
		if n := len([]rune(seg.Text)); n > MaxSegChars {
			t.Errorf("段 %d 超 %d 字(%d): %q", i, MaxSegChars, n, seg.Text)
		}
	}
	if d := segs[len(segs)-1].EndMS - segs[len(segs)-1].StartMS; d < MinDurMS {
		t.Fatalf("过短尾段应延长: %+v", segs[len(segs)-1])
	}
}

// TestRefineMergeOrphanPunct 真实案例回归：句末「。」落在尾静音里、与前文相隔 2 秒
// （前句无句末标点），聚句后曾自成一段孤立句号——纯标点 token 必须并入前文。
func TestRefineMergeOrphanPunct(t *testing.T) {
	words := []WordSpan{
		w("太", 24000, 24100), w("贵", 24100, 25320), // 前句无句末标点
		w("。", 25500, 26333), // 2 秒后的尾静音里,833ms 恰好不触发过短
	}
	segs := RefineTokens(words, nil)
	if len(segs) != 1 {
		t.Fatalf("孤立句号应并入前文成一段: %+v", segs)
	}
	if segs[0].Text != "太贵。" {
		t.Fatalf("文本不符: %q", segs[0].Text)
	}
	if segs[0].StartMS != 24000 || segs[0].EndMS != 26333 {
		t.Fatalf("时间窗应延伸到句号结束: %+v", segs[0])
	}
}

// TestMergePunctTokensLeading 流首孤立标点并入后一个内容 token。
func TestMergePunctTokensLeading(t *testing.T) {
	got, ends := mergePunctTokens(
		[]WordSpan{w("。", 0, 100), w("你", 200, 300), w("好", 300, 400)},
		[]bool{false, false, true},
	)
	if len(got) != 2 {
		t.Fatalf("流首标点应并入后 token: %+v", got)
	}
	if got[0].Text != "。你" || got[0].StartMS != 0 {
		t.Fatalf("合并文本/时间窗不符: %+v", got[0])
	}
	if !ends[1] {
		t.Fatal("句界应随合并保留")
	}
}

// TestIsPunctOnly 边界：空串不算、含内容字不算、空白忽略。
func TestIsPunctOnly(t *testing.T) {
	cases := map[string]bool{
		"。": true, "，": true, "！？": true, " . ": true, "…": true,
		"": false, "好": false, "好。": false,
	}
	for text, want := range cases {
		if got := isPunctOnly(text); got != want {
			t.Errorf("isPunctOnly(%q) = %v, want %v", text, got, want)
		}
	}
}
