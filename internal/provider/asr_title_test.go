package provider

import "testing"

// TestASRTitleFromSummary text 优先、segments 次之、全空/损坏返回空串。
func TestASRTitleFromSummary(t *testing.T) {
	if got := ASRTitleFromSummary(`{"text":"  多  行\n文本  "}`); got != "多 行 文本" {
		t.Errorf("text 形状 = %q", got)
	}
	if got := ASRTitleFromSummary(`{"segments":[{"text":"  "},{"text":"第二句"}]}`); got != "第二句" {
		t.Errorf("segments 跳过空分句 = %q", got)
	}
	if got := ASRTitleFromSummary(`{"segments":[{"text":""}]}`); got != "" {
		t.Errorf("全空 = %q, want 空", got)
	}
	if got := ASRTitleFromSummary(""); got != "" {
		t.Errorf("空 summary = %q", got)
	}
}
