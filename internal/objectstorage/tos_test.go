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

func newTestTOS(t *testing.T, handler http.HandlerFunc) (Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cli, err := New(Config{
		Provider: "tos", Endpoint: srv.URL, Region: "cn-test",
		Bucket: "bkt", AccessKey: "ak", SecretKey: "sk", Prefix: "voxbox",
		InsecureSkipTLSVerify: true,
	})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	return cli, srv
}

func TestNewConfigValidation(t *testing.T) {
	if cli, err := New(Config{}); cli != nil || err != nil {
		t.Fatalf("未启用配置应返回 (nil, nil)，得到 (%v, %v)", cli, err)
	}
	if _, err := New(Config{Provider: "tos"}); err == nil {
		t.Fatal("provider=tos 但配置不全，应报错")
	}
	if _, err := New(Config{Provider: "s3", Endpoint: "e", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}); err == nil {
		t.Fatal("s3 通道未实现，应报「暂未支持」")
	}
}

func TestTOSPutPresignRoundTrip(t *testing.T) {
	var gotPath, gotCT, gotBody string
	cli, _ := newTestTOS(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("x-tos-request-id", "test-req")
		w.WriteHeader(200)
	})
	// 日期分区由 ObjectKey 生成（勿在入参里写死当天日期，跨天会闪断）；
	// 只断言分区为 8 位数字。
	key := ObjectKey("clip/test.mp3")
	if i := strings.IndexByte(key, '/'); i != 8 || !strings.HasSuffix(key, "/clip/test.mp3") {
		t.Fatalf("ObjectKey = %q", key)
	}
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
	if u.RawQuery == "" {
		t.Fatal("签名 URL 缺少签名查询参数")
	}
}

func TestTOSPingMapsErrors(t *testing.T) {
	cli, _ := newTestTOS(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"Code":"AccessDenied"}`))
	})
	err := cli.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("Ping() err = %v, 期望中文提示含 403", err)
	}
}
