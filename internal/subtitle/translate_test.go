package subtitle

import "testing"

func TestNormalizeForCompare(t *testing.T) {
	// 去空白/标点、小写、全角转半角
	got := NormalizeForCompare("Hello, 世界！ＡＢＣ ")
	if want := "hello世界abc"; got != want {
		t.Fatalf("NormalizeForCompare=%q want %q", got, want)
	}
}

func TestSimilarity(t *testing.T) {
	if got := Similarity("今天天气不错", "今天天气不错"); got != 1 {
		t.Fatalf("相同文本相似度=%v want 1", got)
	}
	if got := Similarity("今天天气不错", "明天天气不错"); got <= 0.7 || got >= 1 {
		t.Fatalf("近义文本相似度=%v 应在 (0.7,1)", got)
	}
	if got := Similarity("今天天气不错", " completely different text "); got > 0.3 {
		t.Fatalf("无关文本相似度=%v 应≤0.3", got)
	}
	if got := Similarity("", ""); got != 1 {
		t.Fatalf("空文本相似度=%v want 1", got)
	}
}

func TestBatchIndices(t *testing.T) {
	got := BatchIndices(7, 3)
	if len(got) != 3 || len(got[0]) != 3 || len(got[2]) != 1 || got[2][0] != 6 {
		t.Fatalf("BatchIndices(7,3)=%v", got)
	}
	if got := BatchIndices(0, 3); len(got) != 0 {
		t.Fatalf("BatchIndices(0,3) 应为空")
	}
}

func TestValidateEcho(t *testing.T) {
	srcs := map[int]string{0: "今天天气不错", 1: "我们去公园散步"}
	resp := map[string]EchoEntry{
		"1": {SrcEcho: "今天天气不错", Tr: "The weather is nice today"},
		"2": {SrcEcho: "我们去公园走走", Tr: "Let's walk in the park"}, // 回显漂移
	}
	v := ValidateEcho(srcs, resp, 0.9)
	if v.EchoChecked != 2 || len(v.Accepted) != 1 || len(v.Flagged) != 1 {
		t.Fatalf("ValidateEcho verdict=%+v", v)
	}
	if v.Accepted[0] != "The weather is nice today" || v.Flagged[0] != 1 {
		t.Fatalf("Accepted/Flagged 对位错误: %+v", v)
	}
}

func TestValidateSimple(t *testing.T) {
	srcs := map[int]string{0: "a", 1: "b"}
	v := ValidateSimple(srcs, map[string]string{"1": "x"})
	if len(v.Accepted) != 1 || len(v.Flagged) != 1 || v.Flagged[0] != 1 {
		t.Fatalf("ValidateSimple verdict=%+v", v)
	}
}

func TestIsSuspectedCopy(t *testing.T) {
	if !IsSuspectedCopy("hello world", "hello world", "en") {
		t.Fatal("完全复制应判疑似未翻译")
	}
	if IsSuspectedCopy("这部电影很感人", "這部電影很感人", "zh-Hant") {
		t.Fatal("简→繁目标豁免")
	}
	if IsSuspectedCopy("这部电影很感人", "This movie is touching", "en") {
		t.Fatal("正常译文不应判复制")
	}
}
