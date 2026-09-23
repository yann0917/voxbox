package volcengine

import (
	"strings"
	"testing"
)

func TestBuildSRT(t *testing.T) {
	segs := []ASRSegment{
		{Text: "这是字节跳动，", StartMS: 0, EndMS: 1705},
		{Text: "今日头条母公司。", StartMS: 2110, EndMS: 3696},
	}
	want := "1\n00:00:00,000 --> 00:00:01,705\n这是字节跳动，\n" +
		"\n2\n00:00:02,110 --> 00:00:03,696\n今日头条母公司。\n"
	if got := BuildSRT(segs); got != want {
		t.Errorf("BuildSRT() = %q, want %q", got, want)
	}
	// 时间超 1 小时正常进位。
	srt := BuildSRT([]ASRSegment{{Text: "x", StartMS: 3661000, EndMS: 3662000}})
	if !strings.Contains(srt, "01:01:01,000 --> 01:01:02,000") {
		t.Errorf("超 1 小时时间轴进位错误: %s", srt)
	}
}
