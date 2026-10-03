package subtitle

import "testing"

func calibFixture() []Segment {
	return []Segment{
		{Text: "a", StartMS: 1000, EndMS: 2000},
		{Text: "b", StartMS: 3000, EndMS: 4500},
	}
}

func TestCalibrateShift(t *testing.T) {
	got, err := CalibrateSegments(calibFixture(), CalibrateOpts{Mode: "shift", OffsetMS: -500})
	if err != nil || got[0].StartMS != 500 || got[1].EndMS != 4000 {
		t.Fatalf("shift: %+v err=%v", got, err)
	}
	got, _ = CalibrateSegments(calibFixture(), CalibrateOpts{Mode: "shift", OffsetMS: -2000})
	if got[0].StartMS != 0 || got[0].EndMS != 0 {
		t.Fatalf("shift 负值应钳 0: %+v", got[0])
	}
}

func TestCalibrateScale(t *testing.T) {
	got, err := CalibrateSegments(calibFixture(), CalibrateOpts{Mode: "scale", Ratio: 25.0 / 24.0})
	if err != nil || got[0].EndMS != 2083 || got[1].EndMS != 4688 {
		t.Fatalf("scale 24/25 拉伸: %+v err=%v", got, err)
	}
	if _, err := CalibrateSegments(calibFixture(), CalibrateOpts{Mode: "scale", Ratio: 0}); err == nil {
		t.Fatal("ratio≤0 应报错")
	}
}

func TestCalibrateAnchor(t *testing.T) {
	// 双锚点:1000→2000, 4500→9000(整体拉伸 2 倍再平移)
	got, err := CalibrateSegments(calibFixture(), CalibrateOpts{
		Mode: "anchor", A1Src: 1000, A1Tgt: 2000, A2Src: 4500, A2Tgt: 9000})
	if err != nil || got[0].StartMS != 2000 || got[1].EndMS != 9000 {
		t.Fatalf("anchor: %+v err=%v", got, err)
	}
	if _, err := CalibrateSegments(calibFixture(), CalibrateOpts{
		Mode: "anchor", A1Src: 1000, A1Tgt: 2000, A2Src: 1000, A2Tgt: 3000}); err == nil {
		t.Fatal("双锚点源重合应报错")
	}
	if _, err := CalibrateSegments(calibFixture(), CalibrateOpts{Mode: "nope"}); err == nil {
		t.Fatal("未知 mode 应报错")
	}
}

func TestStripPunct(t *testing.T) {
	if got := StripPunct("你好,世界。再见!"); got != "你好世界再见" {
		t.Fatalf("StripPunct=%q", got)
	}
	if got := StripPunct("Hello, world!"); got != "Hello, world!" {
		t.Fatalf("无 CJK 文本不应处理: %q", got)
	}
	if got := StripPunct("他说:\"你好\""); got != `他说"你好"` {
		t.Fatalf("引号保留: %q", got)
	}
}
