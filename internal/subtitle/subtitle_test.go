package subtitle

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestParseSRT(t *testing.T) {
	content := "1\n00:00:01,000 --> 00:00:03,500\n第一句\n\n" +
		"2\n00:00:03.500 --> 00:00:05,000\n第二句第一行\n第二行\n\n" +
		"坏块没有时间轴\n\n" +
		"3\n00:00:05,000 --> 00:00:06,000\n第三句\n"
	segs, err := ParseSRT(content)
	if err != nil {
		t.Fatalf("ParseSRT: %v", err)
	}
	if len(segs) != 3 {
		t.Fatalf("期望 3 段，得到 %d: %+v", len(segs), segs)
	}
	if segs[0].StartMS != 1000 || segs[0].EndMS != 3500 || segs[0].Text != "第一句" {
		t.Errorf("第 1 段不符: %+v", segs[0])
	}
	if !strings.Contains(segs[1].Text, "\n") {
		t.Errorf("多行文本应保留 \\n: %q", segs[1].Text)
	}
	if segs[2].StartMS != 5000 {
		t.Errorf("毫秒分隔符 \".\" 应兼容: %+v", segs[2])
	}

	if _, err := ParseSRT("完全不是 srt 的内容"); err == nil {
		t.Error("全部坏块应报错")
	}
}

func TestSplitSentences(t *testing.T) {
	got := SplitSentences("大家好！今天讲三件事：第一，演示。他说：“好。”然后结束")
	want := []string{"大家好！", "今天讲三件事：第一，演示。", "他说：“好。”", "然后结束"}
	if len(got) != len(want) {
		t.Fatalf("期望 %d 句，得到 %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 句：期望 %q，得到 %q", i, want[i], got[i])
		}
	}
}

func TestSplitSentencesMixed(t *testing.T) {
	got := SplitSentences("Hello world! What's up?\n第二行内容")
	if len(got) != 3 || got[0] != "Hello world!" || got[1] != "What's up?" || got[2] != "第二行内容" {
		t.Errorf("中英混合分句不符: %v", got)
	}
}

func TestDraftSegments(t *testing.T) {
	segs := DraftSegments("短句。这是一个明显更长的句子。", 10000)
	if len(segs) != 2 {
		t.Fatalf("期望 2 段，得到 %d", len(segs))
	}
	if segs[0].EndMS-segs[0].StartMS >= segs[1].EndMS-segs[1].StartMS {
		t.Errorf("长句应分到更长时长: %+v", segs)
	}
	if segs[1].EndMS != 10000 {
		t.Errorf("末句应吃满总时长: %+v", segs[1])
	}
	// 无时长 → 2s/句占位
	segs = DraftSegments("甲。乙。", 0)
	if segs[0].EndMS != 2000 || segs[1].EndMS != 4000 {
		t.Errorf("占位时长不符: %+v", segs)
	}
}

func assDialogueLines(t *testing.T, body []byte) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "Dialogue:") {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		t.Fatal("ASS 输出中没有 Dialogue 行")
	}
	return out
}

func TestBuildASSKaraoke(t *testing.T) {
	segs := []Segment{{Text: "字幕测试", StartMS: 1000, EndMS: 5000}}
	body := BuildASS(segs, Presets[0])
	dialogue := assDialogueLines(t, body)[0]

	if !strings.Contains(dialogue, `{\k100}字`) {
		t.Errorf("卡拉 OK 标签缺失或不符: %s", dialogue)
	}
	// Σ\k 厘秒 == 句时长/10
	re := regexp.MustCompile(`\\k(\d+)`)
	sum := int64(0)
	for _, m := range re.FindAllStringSubmatch(dialogue, -1) {
		var v int64
		_, _ = fmt.Sscanf(m[1], "%d", &v)
		sum += v
	}
	if sum != 400 { // 4000ms = 400cs
		t.Errorf("卡拉 OK 总时长应等于句时长 400cs，得到 %d", sum)
	}
	// 时间轴格式
	if !strings.Contains(dialogue, "0:00:01.00,0:00:05.00") {
		t.Errorf("ASS 时间轴格式不符: %s", dialogue)
	}
	// 样式行含 BGR 转换：#FF8A3D → &H003D8AFF
	if !strings.Contains(string(body), "&H003D8AFF") {
		t.Errorf("主色 BGR 转换不符:\n%s", body)
	}
}

func TestBuildASSEscaping(t *testing.T) {
	segs := []Segment{{Text: "带{标签}与\n换行", StartMS: 0, EndMS: 1000}}
	body := string(BuildASS(segs, Presets[1]))
	if strings.Contains(body, "{标签}") || strings.Contains(body, "{\\k") && !strings.Contains(body, "带(") {
		t.Errorf("花括号未中和: %s", body)
	}
	if !strings.Contains(body, `带(标签)与\N换行`) {
		t.Errorf("换行应转 \\N: %s", body)
	}
}

func TestBuildSRT(t *testing.T) {
	segs := []Segment{
		{Text: "甲", StartMS: 500, EndMS: 1500},
		{Text: "乙", StartMS: 1500, EndMS: 3000},
	}
	out := string(BuildSRT(segs))
	if !strings.Contains(out, "00:00:00,500 --> 00:00:01,500") || !strings.Contains(out, "2\n00:00:01,500") {
		t.Errorf("SRT 输出不符:\n%s", out)
	}
}
