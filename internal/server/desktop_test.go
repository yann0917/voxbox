package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yann0917/voxbox/internal/service"
)

// newDesktopServer 桌面形态测试服：VOXBOX_DESKTOP=1 + 引导 admin 行，返回未登录客户端可访问的 ts。
func newDesktopServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("VOXBOX_DESKTOP", "1")
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)
	if _, err := s.EnsureBootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// 桌面形态：未登录 /api/auth/me 直接以 admin 身份返回，desktop:true，不触发强制改密。
func TestDesktopUnauthenticatedMeIsAdmin(t *testing.T) {
	ts := newDesktopServer(t)
	resp, err := http.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	d, _ := body["data"].(map[string]any)
	if d == nil {
		t.Fatalf("响应缺少 data: %v", body)
	}
	if d["username"] != "admin" || d["role"] != "admin" {
		t.Fatalf("身份 = %v/%v, want admin/admin", d["username"], d["role"])
	}
	if d["desktop"] != true {
		t.Fatal("desktop = false, want true")
	}
	if d["must_change_password"] == true {
		t.Fatal("must_change_password = true, 桌面形态不应触发强制改密")
	}
}

// 回归：VOXBOX_ADMIN_USERNAME 自定义用户名时，桌面免登录身份与引导行同源，
// 不再写死 "admin"（旧行为下此类部署引导出 custom-admin 行、免登录却找 admin，未登录直接 401）。
func TestDesktopCustomAdminUsername(t *testing.T) {
	t.Setenv("VOXBOX_ADMIN_USERNAME", "custom-admin")
	ts := newDesktopServer(t)
	resp, err := http.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	d, _ := body["data"].(map[string]any)
	if d == nil {
		t.Fatalf("响应缺少 data: %v", body)
	}
	if d["username"] != "custom-admin" || d["role"] != "admin" {
		t.Fatalf("身份 = %v/%v, want custom-admin/admin", d["username"], d["role"])
	}
}

// 回归：非桌面形态未登录仍是 401（envelope code 7）。
func TestNonDesktopUnauthenticatedMeIs401(t *testing.T) {
	t.Setenv("VOXBOX_DESKTOP", "")
	svc, err := service.NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	s := New(svc)
	if _, err := s.EnsureBootstrapAdmin(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
