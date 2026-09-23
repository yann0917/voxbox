// URL-only 工具的本地文件桥：用户上传的本地文件在任务执行时转存对象存储并换取
// 预签名 GET URL 提交上游，工具侧只看到最终 URL。未配置对象存储时回落「仅公网 URL」
// 并给出设置页指引。桶内对象不再按生命周期清理：中转输入与分离缓存产物均为保留资产。
package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/objectstorage"
)

// bridgePresignTTL 预签名 URL 有效期：闲时任务最长排队 24h，72h 覆盖「排队 + 处理 + 重试」
// 全窗口；对象常驻桶内，URL 过期后重新 PresignGet 即可（分离缓存正是按 key 复用对象）。
const bridgePresignTTL = 72 * time.Hour

// EnsureURLInput 解析 URL-only 输入：params[paramKey] 非空直接返回（须为 http(s) 公网地址）；
// 否则取 Files["audio"] 本地文件，经 in.Storage 转存换取签名 URL。两者皆缺或未配置存储时报
// missingErr 指定的中文错误。label 用于进度文案（如「音频」「音视频」）。
func EnsureURLInput(ctx context.Context, in TaskInput, paramKey, label, missingErr string, report ProgressReporter) (string, error) {
	if u := strings.TrimSpace(paramString(in.Params, paramKey)); u != "" {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return "", fmt.Errorf("%s URL 须以 http(s):// 开头的公网可访问地址", label)
		}
		return u, nil
	}

	src := in.Files["audio"]
	if src == "" {
		return "", fmt.Errorf("%s", missingErr)
	}
	if in.Storage == nil {
		return "", fmt.Errorf("本地文件需要对象存储中转：请在设置页配置对象存储（推荐火山 TOS），或直接提供公网可访问的 %s URL", label)
	}

	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		return "", fmt.Errorf("无法识别该 %s 文件的扩展名，无法中转（URL 版工具依赖扩展名推断格式）", label)
	}
	f, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("读取 %s 文件失败: %w", label, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("读取 %s 文件信息失败: %w", label, err)
	}

	key := objectstorage.ObjectKey(bridgeObjectName(src))
	report(3, fmt.Sprintf("正在转存%s到对象存储（%.1f MB）…", label, float64(fi.Size())/1024/1024), nil)
	if err := withUploadProgress(ctx, report, func() error {
		return in.Storage.Put(ctx, key, objectstorage.ContentTypeByExt(key), f, fi.Size())
	}); err != nil {
		return "", err
	}
	signed, err := in.Storage.PresignGet(key, bridgePresignTTL)
	if err != nil {
		return "", err
	}
	report(8, "转存完成，已取得签名 URL", map[string]any{"storage_key": key})
	return signed, nil
}

// bridgeObjectName 中转对象的可见名＝原文件名原样（音乐链路的落盘名是
// 「歌名 - 歌手.档位」，档位后缀即区分同一首歌的不同音质，不再拼随机串）。
// 上传通道的磁盘名本来就是 uuid（无可保留语义），保持纯 uuid 不加后缀。
// 同名并发覆盖的取舍：同名对象只会是同一首歌同档位的重复转存，且签名 URL 在
// 上传后立即使用，覆盖窗口对运行中任务无实质影响；分离结果缓存按内容哈希寻址，不受影响。
func bridgeObjectName(src string) string {
	ext := strings.ToLower(filepath.Ext(src))
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if _, err := uuid.Parse(base); err == nil {
		return base + ext
	}
	return sanitizeObjectName(base) + ext
}

// sanitizeObjectName 折叠控制字符与路径分隔符为下划线，rune 截断 80（对象 key 余量充足）。
func sanitizeObjectName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20, r == 0x7f, r == '/', r == '\\':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	runes := []rune(out)
	if len(runes) > 80 {
		out = string(runes[:80])
	}
	return out
}

// withUploadProgress 跑 fn 并每 5s 上报一次心跳（大文件转存期间任务进度不冻结）；
// ctx 取消立即返回取消错误（上传内部随 ctx 中断）。
func withUploadProgress(ctx context.Context, report ProgressReporter, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for i := 1; ; i++ {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return fmt.Errorf("转存已取消: %w", ctx.Err())
		case <-t.C:
			report(min(8, 3+i), fmt.Sprintf("仍在转存到对象存储（%ds）…", i*5), nil)
		}
	}
}

// paramString 取字符串参数（缺失或类型不符返回空串）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}
