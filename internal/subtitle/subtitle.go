// Package subtitle 实现字幕的本地解析与导出（纯 Go、零 API 成本）：
// SRT 解析、文稿分句草稿（按字数比例在总时长内分配时间）、SRT/ASS 导出，
// ASS 支持逐字卡拉 OK（行内均匀分布，词级精确时间戳需上游对齐能力，后置）。
package subtitle

import (
	"fmt"
	"strings"
)

type Segment struct {
	Text    string `json:"text"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// ---- 时间格式 ----

// parseSRTTime 解析 SRT 时间 "HH:MM:SS,mmm"（毫秒分隔符兼容 "."）。
func parseSRTTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	var h, m, sec, ms int
	sep := ","
	if i := strings.Index(s, "."); i >= 0 && strings.Count(s, ",") == 0 {
		sep = "."
	}
	if _, err := fmt.Sscanf(strings.ReplaceAll(s, sep, ":"), "%d:%d:%d:%d", &h, &m, &sec, &ms); err != nil {
		return 0, fmt.Errorf("无法解析 SRT 时间 %q", s)
	}
	return int64(h)*3600000 + int64(m)*60000 + int64(sec)*1000 + int64(ms), nil
}

func formatSRTTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	rest := ms % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, rest)
}

// formatASSTime ASS 时间轴 "h:mm:ss.cc"（厘秒）。
func formatASSTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	cs := (ms % 1000) / 10
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}

// ---- SRT 解析 ----

// ParseSRT 宽松解析 SRT：块间以空行分隔，忽略序号行，多行文本以 \n 连接；
// 个别坏块跳过，全部无法解析时报错。
func ParseSRT(content string) ([]Segment, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	blocks := strings.Split(content, "\n\n")
	out := make([]Segment, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 {
			continue
		}
		ti := -1
		for i, line := range lines {
			if strings.Contains(line, " --> ") {
				ti = i
				break
			}
		}
		if ti < 0 {
			continue // 无时间轴行的块（纯注释/空块）跳过
		}
		parts := strings.SplitN(lines[ti], " --> ", 2)
		start, err := parseSRTTime(parts[0])
		if err != nil {
			continue
		}
		end, err := parseSRTTime(parts[1])
		if err != nil || end <= start {
			continue
		}
		text := strings.TrimSpace(strings.Join(lines[ti+1:], "\n"))
		if text == "" {
			continue
		}
		out = append(out, Segment{Text: text, StartMS: start, EndMS: end})
	}
	if len(out) == 0 && strings.TrimSpace(content) != "" {
		return nil, fmt.Errorf("未解析到任何字幕块，请确认是标准 SRT 格式")
	}
	return out, nil
}

// ---- 文稿分句草稿 ----

// sentenceEnders 声明为 var 以便测试中临时扩展。
var sentenceEnders = map[rune]bool{'。': true, '！': true, '？': true, '…': true, '!': true, '?': true, ';': true, '；': true}

// closingQuotes 句末标点后紧随的引号/括号并入前句，避免「他说：“好。”」被拆断。
var closingQuotes = map[rune]bool{'”': true, '』': true, '」': true, '）': true, ')': true, '"': true, '\'': true}

// SplitSentences 按中英文句末标点与换行分句；标点归属前句，空句丢弃。
func SplitSentences(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		cur.Reset()
		if s != "" {
			out = append(out, s)
		}
	}
	runes := []rune(text)
	// 手写索引循环：句末标点后的引号/括号要并入本句并跳过这些 rune（range 改 i 不生效）
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\n' {
			flush()
			continue
		}
		cur.WriteRune(r)
		if sentenceEnders[r] {
			j := i + 1
			for j < len(runes) && closingQuotes[runes[j]] {
				cur.WriteRune(runes[j])
				j++
			}
			i = j - 1
			flush()
		}
	}
	flush()
	return out
}

// DraftSegments 文稿转字幕草稿：各句按字符数占比在 durationMS 内顺序分配
// （时长缺失时每句占 2s 占位，交由用户在编辑表里校准）。
func DraftSegments(text string, durationMS int64) []Segment {
	sentences := SplitSentences(text)
	if len(sentences) == 0 {
		return nil
	}
	if durationMS <= 0 {
		durationMS = int64(len(sentences)) * 2000
	}
	weights := make([]int64, len(sentences))
	var total int64
	for i, s := range sentences {
		w := int64(len([]rune(s)))
		if w == 0 {
			w = 1
		}
		weights[i] = w
		total += w
	}
	out := make([]Segment, 0, len(sentences))
	var cursor int64
	for i, s := range sentences {
		var span int64
		if i == len(sentences)-1 {
			span = durationMS - cursor // 末句吃掉取整余数
		} else {
			span = durationMS * weights[i] / total
		}
		if span < 200 {
			span = 200
		}
		out = append(out, Segment{Text: s, StartMS: cursor, EndMS: cursor + span})
		cursor += span
	}
	return out
}

// ---- ASS 导出 ----

// Style 字幕样式（ASS v4+ 单样式）；颜色统一 #RRGGBB，导出时转 ASS 的 &HBBGGRR。
type Style struct {
	Name      string `json:"name"`
	FontName  string `json:"font_name"`
	FontSize  int    `json:"font_size"`
	Bold      bool   `json:"bold"`
	Primary   string `json:"primary"`   // 主色：非卡拉 OK 的字幕色 / 卡拉 OK 的「已唱」色
	Secondary string `json:"secondary"` // 卡拉 OK「未唱」基色
	Outline   string `json:"outline"`   // 描边色
	OutlineW  int    `json:"outline_w"` // 描边宽度
	MarginV   int    `json:"margin_v"`  // 垂直边距（底部对齐）
	Karaoke   bool   `json:"karaoke"`   // 逐字卡拉 OK（行内均匀分布）
}

// Presets 内置样式预设。
var Presets = []Style{
	{Name: "琥珀", FontName: "Fira Sans", FontSize: 64, Bold: true, Primary: "#FF8A3D", Secondary: "#F0EAE2", Outline: "#141210", OutlineW: 2, MarginV: 72, Karaoke: true},
	{Name: "经典白", FontName: "Fira Sans", FontSize: 64, Bold: false, Primary: "#FFFFFF", Secondary: "#FFFFFF", Outline: "#000000", OutlineW: 2, MarginV: 72, Karaoke: false},
	{Name: "信号绿", FontName: "Fira Sans", FontSize: 60, Bold: true, Primary: "#4CD487", Secondary: "#F5F1E8", Outline: "#0D0B09", OutlineW: 2, MarginV: 60, Karaoke: true},
	{Name: "天蓝", FontName: "Fira Sans", FontSize: 64, Bold: false, Primary: "#4DA6FF", Secondary: "#FFFFFF", Outline: "#101418", OutlineW: 2, MarginV: 80, Karaoke: false},
}

// PresetByName 按名取预设，未命中回退第一个。
func PresetByName(name string) Style {
	for _, p := range Presets {
		if p.Name == name {
			return p
		}
	}
	return Presets[0]
}

// assColor "#RRGGBB" → "&H00BBGGRR"（ASS 颜色为 BGR 序，Alpha 00=不透明）。
func assColor(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "&H00FFFFFF"
	}
	r, g, b := hex[0:2], hex[2:4], hex[4:6]
	return fmt.Sprintf("&H00%s%s%s", b, g, r)
}

func assText(text string) string {
	// 花括号是 ASS 覆写标签语法，必须移除防注入；换行转 \N
	t := strings.ReplaceAll(text, "{", "(")
	t = strings.ReplaceAll(t, "}", ")")
	return strings.ReplaceAll(t, "\n", "\\N")
}

// BuildASS 生成 ASS 文档。卡拉 OK 模式下每句按字符数均匀分配 \k 厘秒
// （精确到字的时间需上游词级对齐，此处为观感近似）。
func BuildASS(segments []Segment, style Style) []byte {
	var b strings.Builder
	b.WriteString("[Script Info]\n")
	b.WriteString("; Generated by voxbox 字幕工坊\n")
	b.WriteString("ScriptType: v4.00+\n")
	b.WriteString("PlayResX: 1920\n")
	b.WriteString("PlayResY: 1080\n")
	b.WriteString("WrapStyle: 2\n")
	b.WriteString("ScaledBorderAndShadow: yes\n\n")

	bold := 0
	if style.Bold {
		bold = 1
	}
	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	b.WriteString(fmt.Sprintf(
		"Style: Tool,%s,%d,%s,%s,%s,&H00000000,%d,0,0,0,100,100,0,0,1,%d,0,2,80,80,%d,1\n",
		style.FontName, style.FontSize,
		assColor(style.Primary), assColor(style.Secondary), assColor(style.Outline),
		bold, style.OutlineW, style.MarginV,
	))
	b.WriteString("\n[Events]\n")
	b.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	for _, seg := range segments {
		if seg.EndMS <= seg.StartMS || strings.TrimSpace(seg.Text) == "" {
			continue
		}
		body := assText(seg.Text)
		if style.Karaoke {
			body = karaokeText(seg.Text, seg.EndMS-seg.StartMS)
		}
		b.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,Tool,,0,0,0,,%s\n",
			formatASSTime(seg.StartMS), formatASSTime(seg.EndMS), body))
	}
	return []byte(b.String())
}

// karaokeText 生成 "{\k12}文{\k8}字"：句时长按 rune 数均分（厘秒向下取整），
// 末字吃掉取整余数，保证总唱时长与句时长一致（末字未唱完不消失）。
func karaokeText(text string, durationMS int64) string {
	runes := []rune(strings.ReplaceAll(text, "\n", " "))
	n := len(runes)
	if n == 0 {
		return ""
	}
	total := durationMS / 10 // 厘秒
	per := total / int64(n)
	if per < 1 {
		per = 1
	}
	var b strings.Builder
	for i, r := range runes {
		cs := per
		if i == n-1 {
			if rest := total - per*int64(n-1); rest > cs {
				cs = rest
			}
		}
		fmt.Fprintf(&b, "{\\k%d}%c", cs, r)
	}
	return b.String()
}

// ---- SRT 导出 ----

func BuildSRT(segments []Segment) []byte {
	var b strings.Builder
	for i, seg := range segments {
		if seg.EndMS <= seg.StartMS || strings.TrimSpace(seg.Text) == "" {
			continue
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1,
			formatSRTTime(seg.StartMS), formatSRTTime(seg.EndMS),
			strings.TrimSpace(seg.Text))
	}
	return []byte(b.String())
}
