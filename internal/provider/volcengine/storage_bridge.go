package volcengine

import (
	"context"

	"github.com/yann0917/voxbox/internal/provider"
)

// ensureURLInput URL-only 工具的本地文件桥，实现已上移 provider 包（qianwen 等其他
// provider 共用），此处保留同名薄包装，包内 5 处调用点零改动。
func ensureURLInput(ctx context.Context, in provider.TaskInput, paramKey, label, missingErr string, report provider.ProgressReporter) (string, error) {
	return provider.EnsureURLInput(ctx, in, paramKey, label, missingErr, report)
}
