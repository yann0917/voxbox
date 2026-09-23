package objectstorage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestOSS(t *testing.T, handler http.HandlerFunc) (Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cli, err := New(Config{
		Provider: "oss", Endpoint: srv.URL, Region: "cn-test",
		Bucket: "bkt", AccessKey: "ak", SecretKey: "sk", Prefix: "voxbox",
	})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	return cli, srv
}

// TestOSSPutPresignRoundTrip 上传与预签名往返；签名钉住 V4（2025-03 起新桶停用 V1）。
func TestOSSPutPresignRoundTrip(t *testing.T) {
	var gotPath, gotCT, gotBody string
	cli, _ := newTestOSS(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	})
	key := ObjectKey("20260917/test.mp3")
	if err := cli.Put(context.Background(), key, ContentTypeByExt(key), strings.NewReader("hello-audio"), 11); err != nil {
		t.Fatalf("Put() err = %v", err)
	}
	if !strings.Contains(gotPath, "bkt") || !strings.HasSuffix(gotPath, key) {
		t.Fatalf("上传路径 = %q, 期望含桶与前缀 key", gotPath)
	}
	if gotCT != "audio/mpeg" {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	if gotBody != "hello-audio" {
		t.Fatalf("上传内容 = %q", gotBody)
	}

	signed, err := cli.PresignGet(key, 72*time.Hour)
	if err != nil {
		t.Fatalf("PresignGet() err = %v", err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("签名 URL 无法解析: %v", err)
	}
	if !strings.HasSuffix(u.Path, key) {
		t.Fatalf("签名 URL 路径 = %q, 期望含 key", u.Path)
	}
	q := u.Query()
	if q.Get("x-oss-signature-version") != "OSS4-HMAC-SHA256" {
		t.Fatalf("签名 URL 非预期签名版本: %v", q)
	}
	if q.Get("x-oss-credential") == "" || !strings.Contains(q.Get("x-oss-credential"), "cn-test") {
		t.Fatalf("V4 签名凭据缺少 region: %v", q)
	}
}

func TestOSSPingMapsErrors(t *testing.T) {
	cli, _ := newTestOSS(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
	})
	err := cli.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("Ping() err = %v, 期望中文提示含 403", err)
	}
}

// TestOSSBareEndpointDefaultsHTTPS 该 SDK 对裸 host 默认 HTTP（与 TOS SDK 相反），必须归一到 https。
func TestOSSBareEndpointDefaultsHTTPS(t *testing.T) {
	if _, err := New(Config{Provider: "oss", Endpoint: "oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou",
		Bucket: "bkt", AccessKey: "ak", SecretKey: "sk"}); err != nil {
		t.Fatalf("New() err = %v", err)
	}
}
