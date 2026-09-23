package volcengine

import (
	"fmt"
	"strings"
)

// BuildSRT 将分句时间戳渲染为标准 SRT 字幕：序号从 1 起，
// 时间轴格式 HH:MM:SS,mmm --> HH:MM:SS,mmm，条目间空行分隔。
func BuildSRT(segments []ASRSegment) string {
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n",
			i+1, srtTimestamp(seg.StartMS), srtTimestamp(seg.EndMS), seg.Text)
	}
	return b.String()
}

// srtTimestamp 毫秒 → SRT 时间戳 HH:MM:SS,mmm（超 1 小时正常进位）。
func srtTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	ms %= 3600000
	m := ms / 60000
	ms %= 60000
	s := ms / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
