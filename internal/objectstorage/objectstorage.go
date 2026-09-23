// Package objectstorage 对象存储抽象：语音识别/人声分离/妙记等 URL-only 工具的本地文件
// 中转通道。任务执行时把本地文件 Put 到桶内（前缀 + 日期分区），再取预签名 GET URL 提交上游。
// 接口按「最小必要」设计（写入/签名读/探活/生命周期）：火山 TOS 与阿里 OSS 各用官方 SDK
// 实现；腾讯 COS 等其余 S3 兼容通道规划中（New 的 provider 分派已预留）。
package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"time"
)

// Config 连接与行为参数（与 config.StorageConfig 字段一一对应，解耦避免反向依赖）。
type Config struct {
	Provider  string // ""=未启用 | "tos" | "oss"（"s3" 预留）
	Endpoint  string // 如 tos-cn-beijing.volces.com（允许带 scheme，SDK 按前缀推断协议）
	Region    string // 如 cn-beijing
	Bucket    string
	AccessKey string
	SecretKey string
	Prefix    string // 对象 key 前缀，空则落桶根

	// InsecureSkipTLSVerify 关闭 TLS 证书校验：自签证书的私有化对象存储（MinIO 等）用；
	// 公有云通道请保持 false。不入 config.yaml（高级场景走 S3 兼容通道时再暴露）。
	InsecureSkipTLSVerify bool
}

// Enabled 配置是否构成一个可用通道（provider 已选且关键字段齐全）。
func Enabled(c Config) bool {
	return c.Provider != "" && c.Endpoint != "" && c.Region != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != ""
}

// Client 对象存储通道。实现须并发安全（工具任务 goroutine 并发调用 Put/PresignGet）。
type Client interface {
	// Put 流式写入对象；size 为内容字节数（≤0 表示未知，实现自行兜底）。
	Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error
	// PresignGet 生成预签名 GET URL（对象保持私有读，上游凭 URL 拉取）。
	PresignGet(key string, ttl time.Duration) (string, error)
	// Ping 探活：桶可达且凭证有效（权限不足/桶不存在返回带语义的中文错误）。
	Ping(ctx context.Context) error
}

// ErrNotConfigured 未配置或配置不完整时的统一哨兵，调用方据此给设置指引。
var ErrNotConfigured = errors.New("对象存储未配置或配置不完整")

// New 按 provider 构造客户端；未启用返回 (nil, nil)。
func New(cfg Config) (Client, error) {
	if !Enabled(cfg) {
		if cfg.Provider == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("%w：请检查 endpoint/region/bucket/AK/SK", ErrNotConfigured)
	}
	switch cfg.Provider {
	case "tos":
		return newTOSClient(cfg)
	case "oss":
		return newOSSClient(cfg)
	default:
		// 腾讯 COS、MinIO 等 S3 兼容通道预留：接口已按最小必要收敛，接实现即可，不改调用方。
		return nil, fmt.Errorf("对象存储 provider %q 暂未支持（当前支持 tos/oss，其他 S3 兼容通道规划中）", cfg.Provider)
	}
}

// ObjectKey 任务侧对象相对名：<yyyyMMdd>/<uuid>.<ext>；前缀由实现拼接。
// 日期分区便于人工在桶上排查，也使生命周期按前缀清理的观测更直观。
func ObjectKey(name string) string {
	return path.Join(time.Now().Format("20060102"), name)
}

// extOf 取小写扩展名（含点），空扩展名返回空串（调用方决定是否拒绝）。
func extOf(name string) string {
	ext := strings.ToLower(path.Ext(name))
	return ext
}

// ContentTypeByExt 按扩展名给上传对象定 Content-Type：上游按 URL 拉取时不敏感，
// 但正确的类型让桶上的临时分享/人工检查更友好。未知扩展名回退 octet-stream。
func ContentTypeByExt(name string) string {
	switch extOf(name) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".flac":
		return "audio/flac"
	case ".amr":
		return "audio/amr"
	case ".spx":
		return "audio/ogg"
	case ".pcm":
		return "audio/pcm"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	}
	if t := mime.TypeByExtension(extOf(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}
