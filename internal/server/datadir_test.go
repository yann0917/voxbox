package server

// 数据保存位置端点：写入往返一致、非法输入拒绝、同值无操作拒绝。
// VOXBOX_HOME 隔离优先于显式配置的语义归 config 包测试（TestDataDirVOXBOXHomeWins）。

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
)

// TestPutDataDirRoundtrip 保存后 config.yaml 落盘、再读一致；响应带 restart_required。
func TestPutDataDirRoundtrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // windows 兜底；非 windows 无效无害
	t.Setenv("VOXBOX_HOME", "")
	ts, _, ac := newTestServer(t)

	newDir := filepath.Join(t.TempDir(), "voxbox-data")
	_, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/data-dir", `{"dir":"`+newDir+`"}`)
	if e.Code != CodeOK {
		t.Fatalf("code = %d (%s), want %d", e.Code, e.Message, CodeOK)
	}
	data, _ := e.Data.(map[string]any)
	if data["dir"] != newDir {
		t.Errorf("data.dir = %v, want %q", data["dir"], newDir)
	}
	if data["restart_required"] != true {
		t.Errorf("data.restart_required = %v, want true", data["restart_required"])
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != newDir {
		t.Errorf("落盘回读 DataDir = %q, want %q", cfg.DataDir, newDir)
	}
}

// TestPutDataDirInvalid 非法输入（空/相对路径/与当前值相同）全部 400 拒绝，不落盘。
func TestPutDataDirInvalid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("VOXBOX_HOME", "")
	ts, _, ac := newTestServer(t)

	// 当前生效值经 GET 取得（测试服务的 home 与 HOME env 不同源，不能从 config.Path() 推）
	cur := getEnvelope(t, ac, ts.URL+"/api/settings").Data.(map[string]any)["data_dir"].(string)

	for name, body := range map[string]string{
		"空目录":   `{"dir":"  "}`,
		"相对路径":  `{"dir":"relative/dir"}`,
		"磁盘根目录": `{"dir":"/"}`,
		"与当前相同": `{"dir":"` + cur + `"}`,
	} {
		_, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/data-dir", body)
		if e.Code != CodeBadRequest {
			t.Errorf("%s: code = %d (%s), want %d", name, e.Code, e.Message, CodeBadRequest)
		}
	}
	home, _ := os.UserHomeDir()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.Join(home, ".voxbox", "data") {
		t.Errorf("非法输入不应落盘: DataDir = %q", cfg.DataDir)
	}
}

// TestPutDataDirTildeHome ~/ 前缀展开用户主目录（ shell 惯例，服务端代为展开）。
func TestPutDataDirTildeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("VOXBOX_HOME", "")
	ts, _, ac := newTestServer(t)

	_, e := doJSON(t, ac, http.MethodPut, ts.URL+"/api/settings/data-dir", `{"dir":"~/music-data"}`)
	if e.Code != CodeOK {
		t.Fatalf("code = %d (%s), want %d", e.Code, e.Message, CodeOK)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "music-data"); cfg.DataDir != want {
		t.Errorf("落盘回读 DataDir = %q, want 展开后 %q", cfg.DataDir, want)
	}
}
