// OSS 实现：阿里云对象存储官方 SDK（aliyun-oss-go-sdk，固定 V4 签名——2025-03 起新建桶
// 已停用 V1 签名，V4 需 region 参与签名域，故 Region 必填）。桶保持私有读：上游经预签名
// GET URL 拉取。
package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type ossClient struct {
	bkt    *oss.Bucket
	prefix string // 已归一（无首尾斜杠），空表示桶根
}

// newOSSClient 构造官方客户端。注意该 SDK 对裸 host 默认走 HTTP（与 TOS SDK 相反）：
// 公有云统一补 https://，本地假服务/私有化部署可显式传 http:// 前缀。
func newOSSClient(cfg Config) (Client, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	endpoint = strings.TrimPrefix(endpoint, "//")
	if endpoint == "" {
		return nil, fmt.Errorf("%w：endpoint 为空", ErrNotConfigured)
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	opts := []oss.ClientOption{
		oss.AuthVersion(oss.AuthV4),
		oss.Region(cfg.Region),
	}
	if cfg.InsecureSkipTLSVerify {
		opts = append(opts, oss.InsecureSkipVerify(true))
	}
	cli, err := oss.New(endpoint, cfg.AccessKey, cfg.SecretKey, opts...)
	if err != nil {
		return nil, fmt.Errorf("初始化 OSS 客户端失败: %w", err)
	}
	bkt, err := cli.Bucket(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("初始化 OSS 客户端失败: %w", err)
	}
	return &ossClient{bkt: bkt, prefix: cleanPrefix(cfg.Prefix)}, nil
}

func (o *ossClient) fullKey(key string) string {
	if o.prefix == "" {
		return key
	}
	return o.prefix + "/" + key
}

// SDK 无 WithContext 变体，超时由 SDK 自身连接/读写超时与上层任务预算约束。
func (o *ossClient) Put(_ context.Context, key, contentType string, r io.Reader, size int64) error {
	if size <= 0 {
		// 调用方约定传 *os.File（可 Stat），此分支仅兜底不可 seek 的 reader：整读定长。
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("读取待上传内容失败: %w", err)
		}
		r, size = bytes.NewReader(data), int64(len(data))
	}
	if err := o.bkt.PutObject(o.fullKey(key), r, oss.ContentType(contentType), oss.ContentLength(size)); err != nil {
		return fmt.Errorf("上传对象存储失败(%s): %w", o.fullKey(key), humanOSSerr(err))
	}
	return nil
}

func (o *ossClient) PresignGet(key string, ttl time.Duration) (string, error) {
	if ttl < presignMinTTL {
		ttl = presignMinTTL
	}
	if ttl > presignMaxTTL {
		ttl = presignMaxTTL
	}
	signed, err := o.bkt.SignURL(o.fullKey(key), oss.HTTPGet, int64(ttl.Seconds()))
	if err != nil {
		return "", fmt.Errorf("生成预签名 URL 失败: %w", err)
	}
	return signed, nil
}

func (o *ossClient) Ping(_ context.Context) error {
	// GetBucketInfo 与 TOS 侧 HeadBucket 同语义：404=桶不存在、403=凭证/权限不符。
	if _, err := o.bkt.Client.GetBucketInfo(o.bkt.BucketName); err != nil {
		return fmt.Errorf("对象存储连接失败: %w", humanOSSerr(err))
	}
	return nil
}

// humanOSSerr 把 SDK 错误翻译成可操作的中文提示（保留原始信息兜底）。
func humanOSSerr(err error) error {
	var serr oss.ServiceError
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
