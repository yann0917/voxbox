package volcengine

import "github.com/yann0917/voxbox/internal/provider"

// splitText 文本分段，共享实现（句末标点贪心组装；超长句次级标点细切、
// 硬切兜底，保证段长严格 ≤maxLen）。
func splitText(text string, maxLen int) []string {
	return provider.SplitText(text, maxLen)
}
