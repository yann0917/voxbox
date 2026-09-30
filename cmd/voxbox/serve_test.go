package main

import "testing"

// TestBrowserURL 通配/IPv6 绑定归一为浏览器可访问地址，常规地址原样保留。
func TestBrowserURL(t *testing.T) {
	cases := []struct {
		listen string
		port   int
		want   string
	}{
		{"127.0.0.1:8080", 8080, "http://127.0.0.1:8080"},
		{"0.0.0.0:8080", 8080, "http://127.0.0.1:8080"},
		{"[::]:8080", 8080, "http://127.0.0.1:8080"},
		{"[::1]:8080", 8080, "http://localhost:8080"},
		{"192.168.1.8:0", 49152, "http://192.168.1.8:49152"}, // 端口 0=系统分配,取实际端口
	}
	for _, c := range cases {
		if got := browserURL(c.listen, c.port); got != c.want {
			t.Errorf("browserURL(%q,%d) = %q, want %q", c.listen, c.port, got, c.want)
		}
	}
}
