package volcengine

import (
	"strings"
	"testing"
)

func TestSplitText(t *testing.T) {
	long := strings.Repeat("这是第一句话。这是第二句话！这是第三句话？", 100) // 1800 字
	segs := splitText(long, 1000)
	if len(segs) < 2 {
		t.Fatalf("segments = %d, want >= 2", len(segs))
	}
	joined := strings.Join(segs, "")
	if len([]rune(joined)) != len([]rune(long)) {
		t.Error("split lost content")
	}
	for i, s := range segs {
		if len([]rune(s)) > 1100 { // 句子本身超长时允许少量溢出
			t.Errorf("seg %d too long: %d", i, len([]rune(s)))
		}
	}
	if got := splitText("短文本", 1000); len(got) != 1 {
		t.Errorf("short text segments = %d", len(got))
	}
}
