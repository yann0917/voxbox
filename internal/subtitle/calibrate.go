package subtitle

// 时间轴校准与中文去标点(字幕工坊小件,移植自 SmartSub toolbox/subtitleSync)。

import (
	"fmt"
	"strings"
	"unicode"
)

// CalibrateOpts 校准参数;Mode: shift(整体平移)| scale(比例伸缩)| anchor(双锚点线性)。
type CalibrateOpts struct {
	Mode     string  `json:"mode"`
	OffsetMS int64   `json:"offset_ms"` // shift
	Ratio    float64 `json:"ratio"`     // scale(乘,如 25/24 消除帧率漂移)
	// anchor:首尾双锚点重采样,t' = A1Tgt + (t-A1Src)*(A2Tgt-A1Tgt)/(A2Src-A1Src)
	A1Src int64 `json:"a1_src"`
	A1Tgt int64 `json:"a1_tgt"`
	A2Src int64 `json:"a2_src"`
	A2Tgt int64 `json:"a2_tgt"`
}

// CalibrateSegments 返回校准后的新切片(不改入参);时间钳 0。
func CalibrateSegments(segs []Segment, o CalibrateOpts) ([]Segment, error) {
	mapTS := func(ms int64) (int64, error) {
		switch o.Mode {
		case "shift":
			return ms + o.OffsetMS, nil
		case "scale":
			if o.Ratio <= 0 {
				return 0, fmt.Errorf("比例须为正数")
			}
			return int64(float64(ms)*o.Ratio + 0.5), nil
		case "anchor":
			if o.A1Src == o.A2Src {
				return 0, fmt.Errorf("双锚点的源时刻不能相同")
			}
			return o.A1Tgt + int64(float64(ms-o.A1Src)*float64(o.A2Tgt-o.A1Tgt)/float64(o.A2Src-o.A1Src)), nil
		default:
			return 0, fmt.Errorf("mode 须为 shift | scale | anchor")
		}
	}
	out := make([]Segment, len(segs))
	for i, s := range segs {
		st, err := mapTS(s.StartMS)
		if err != nil {
			return nil, err
		}
		en, err := mapTS(s.EndMS)
		if err != nil {
			return nil, err
		}
		if st < 0 {
			st = 0
		}
		if en < 0 {
			en = 0
		}
		out[i] = Segment{Text: s.Text, Translation: s.Translation, StartMS: st, EndMS: en}
	}
	return out, nil
}

// cjkStripPuncts 中文标点剥离集:全角/中文句读及其 ASCII 形态(行内全部移除);
// 引号、括号等成对符号不在其内,一律保留。
const cjkStripPuncts = "。,、;:…!?!?~·—-，；：！？～"

// StripPunct 中文字幕去标点:仅当行内含 CJK(汉字/假名)才行内剥离句读集
// (含行中标点),行尾空白一并裁掉;纯拉丁文本原样返回。
func StripPunct(s string) string {
	hasCJK := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			hasCJK = true
			break
		}
	}
	if !hasCJK {
		return s
	}
	stripped := strings.Map(func(r rune) rune {
		if strings.ContainsRune(cjkStripPuncts, r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimRight(stripped, " ")
}
