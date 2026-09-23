// TOS 实现：火山引擎对象存储官方 SDK（ve-tos-golang-sdk v2，V4 签名）。
// 桶保持私有读：上游经预签名 GET URL 拉取；到期清理走桶生命周期规则（合并语义）。
package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

// presignTTLBounds 预签名有效期边界（SDK 硬约束 [1s, 7d]）。
const (
	presignMinTTL = time.Second
	presignMaxTTL = 7 * 24 * time.Hour
)

type tosClient struct {
	cli    *tos.ClientV2
	bucket string
	prefix string // 已归一（无首尾斜杠），空表示桶根
}

// newTOSClient 校验配置并构造官方客户端。endpoint 原样交给 SDK（其 schemeHost 按前缀
// http:// / https:// 推断协议，裸 host 默认 HTTPS）；InsecureSkipTLSVerify 供自签证书的
// 私有化/MinIO 类部署关闭证书校验。
func newTOSClient(cfg Config) (Client, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	endpoint = strings.TrimPrefix(endpoint, "//")
	if endpoint == "" {
		return nil, fmt.Errorf("%w：endpoint 为空", ErrNotConfigured)
	}
	opts := []tos.ClientOption{
		tos.WithRegion(cfg.Region),
		tos.WithCredentials(tos.NewStaticCredentials(cfg.AccessKey, cfg.SecretKey)),
	}
	if cfg.InsecureSkipTLSVerify {
		opts = append(opts, tos.WithEnableVerifySSL(false))
	}
	cli, err := tos.NewClientV2(endpoint, opts...)
	if err != nil {
		// 用户常从控制台复制到 S3 兼容域名（tos-s3-…），原生 SDK 拒绝：给可操作的提示而非裸错误。
		if strings.Contains(err.Error(), "s3 endpoint") {
			return nil, fmt.Errorf("endpoint %s 是 TOS 的 S3 兼容域名（tos-s3-…），原生通道请使用 tos-<region>.volces.com 形式（S3 兼容通道规划中）", endpoint)
		}
		return nil, fmt.Errorf("初始化 TOS 客户端失败: %w", err)
	}
	return &tosClient{cli: cli, bucket: cfg.Bucket, prefix: cleanPrefix(cfg.Prefix)}, nil
}

func cleanPrefix(p string) string {
	return strings.Trim(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
}

func (t *tosClient) fullKey(key string) string {
	if t.prefix == "" {
		return key
	}
	return t.prefix + "/" + key
}

func (t *tosClient) Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	if size <= 0 {
		// 调用方约定传 *os.File（可 Stat），此分支仅兜底不可 seek 的 reader：整读定长。
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("读取待上传内容失败: %w", err)
		}
		r, size = bytes.NewReader(data), int64(len(data))
	}
	_, err := t.cli.PutObjectV2(ctx, &tos.PutObjectV2Input{
		PutObjectBasicInput: tos.PutObjectBasicInput{
			Bucket:        t.bucket,
			Key:           t.fullKey(key),
			ContentLength: size,
			ContentType:   contentType,
		},
		Content: r,
	})
	if err != nil {
		return fmt.Errorf("上传对象存储失败(%s): %w", t.fullKey(key), humanTOSerr(err))
	}
	return nil
}

func (t *tosClient) PresignGet(key string, ttl time.Duration) (string, error) {
	if ttl < presignMinTTL {
		ttl = presignMinTTL
	}
	if ttl > presignMaxTTL {
		ttl = presignMaxTTL
	}
	out, err := t.cli.PreSignedURL(&tos.PreSignedURLInput{
		HTTPMethod: enum.HttpMethodGet,
		Bucket:     t.bucket,
		Key:        t.fullKey(key),
		Expires:    int64(ttl.Seconds()),
	})
	if err != nil {
		return "", fmt.Errorf("生成预签名 URL 失败: %w", err)
	}
	return out.SignedUrl, nil
}

func (t *tosClient) Ping(ctx context.Context) error {
	if _, err := t.cli.HeadBucket(ctx, &tos.HeadBucketInput{Bucket: t.bucket}); err != nil {
		return fmt.Errorf("对象存储连接失败: %w", humanTOSerr(err))
	}
	return nil
}

// humanTOSerr 把 SDK 错误翻译成可操作的中文提示（保留原始信息兜底）。
func humanTOSerr(err error) error {
	var serr *tos.TosServerError
	if !errors.As(err, &serr) {
		return err
	}
	switch {
	case serr.StatusCode == 403:
		return fmt.Errorf("访问被拒绝(HTTP 403)：请核对 AK/SK 与桶归属、权限（%s）", serr.Code)
	case serr.StatusCode == 404:
		return fmt.Errorf("资源不存在(HTTP 404)：请核对桶名与 endpoint/region 是否匹配")
	case serr.StatusCode == 401:
		return fmt.Errorf("凭证无效(HTTP 401)：请核对 AK/SK（%s）", serr.Code)
	default:
		return fmt.Errorf("HTTP %d %s: %s", serr.StatusCode, serr.Code, serr.Message)
	}
}
