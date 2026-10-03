package main

// translate --srt 译出稿正文替换：非双语输出以译文为字幕文本，双语路径不改入参。

import (
	"testing"

	"github.com/yann0917/voxbox/internal/subtitle"
)

func TestTranslatedSegments(t *testing.T) {
	segs := []subtitle.Segment{
		{Text: "你好。", Translation: "Hello.", StartMS: 0, EndMS: 1000},
		{Text: "世界。", StartMS: 1000, EndMS: 2000}, // 无译文：退化原文
	}
	got := translatedSegments(segs)
	if got[0].Text != "Hello." || got[1].Text != "世界。" {
		t.Fatalf("translatedSegments = %+v", got)
	}
	// 入参不被改动（双语导出仍依赖原 Text/Translation 形状）
	if segs[0].Text != "你好。" || segs[0].Translation != "Hello." {
		t.Fatalf("入参被改动: %+v", segs[0])
	}
}
