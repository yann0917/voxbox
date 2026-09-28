package provider

import (
	"strings"
	"testing"
)

func TestSplitTextShort(t *testing.T) {
	for _, in := range []string{"短文本", "第一句。第二句！"} {
		if got := SplitText(in, 1000); len(got) != 1 || got[0] != in {
			t.Errorf("SplitText(%q) = %v，want 单段原样", in, got)
		}
	}
	if got := SplitText("", 10); got != nil {
		t.Errorf("空文本 = %v，want nil", got)
	}
}

// 按句切分贪心组装：每段 ≤maxLen、内容不丢、顺序不乱。
func TestSplitTextBySentence(t *testing.T) {
	long := strings.Repeat("这是第一句话。这是第二句话！这是第三句话？", 100) // 1800 字
	segs := SplitText(long, 1000)
	if len(segs) < 2 {
		t.Fatalf("segments = %d, want >= 2", len(segs))
	}
	joined := strings.Join(segs, "")
	if joined != long {
		t.Error("split lost or reordered content")
	}
	for i, s := range segs {
		if n := len([]rune(s)); n > 1000 {
			t.Errorf("seg %d 超限: %d", i, n)
		}
	}
}

// 无任何标点的长串：按次级标点（无）→ 硬切，严格 ≤maxLen。
func TestSplitTextHardCut(t *testing.T) {
	in := strings.Repeat("甲", 2500)
	segs := SplitText(in, 1000)
	if len(segs) != 3 {
		t.Fatalf("segments = %d, want 3", len(segs))
	}
	for i, s := range segs {
		if n := len([]rune(s)); n > 1000 {
			t.Errorf("seg %d 超限: %d", i, n)
		}
	}
	if strings.Join(segs, "") != in {
		t.Error("硬切丢内容")
	}
}

// 单句超长但有次级标点：在逗号处细切，不出现在无意义位置硬断。
func TestSplitTextSubPunct(t *testing.T) {
	in := strings.Repeat("甲乙丙丁，", 300) + "结尾。" // 1500 字单句
	segs := SplitText(in, 1024)
	if len(segs) < 2 {
		t.Fatalf("segments = %d, want >= 2", len(segs))
	}
	if strings.Join(segs, "") != in {
		t.Error("次级标点切分丢内容")
	}
	for i, s := range segs {
		if n := len([]rune(s)); n > 1024 {
			t.Errorf("seg %d 超限: %d", i, n)
		}
		if i < len(segs)-1 && !strings.HasSuffix(s, "，") {
			// 非末段边界应落在次级标点（逗号）之后
			t.Errorf("seg %d 未按次级标点断开，尾字符 = %q", i, []rune(s[len(s)-3:]))
		}
	}
}
