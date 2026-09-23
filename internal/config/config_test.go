package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	setHome(t, t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8081 {
		t.Errorf("Port = %d, want 8081", cfg.Server.Port)
	}
	if !strings.HasSuffix(cfg.DataDir, ".voxbox/data") {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
}

func TestSetAndLoadSecretMasked(t *testing.T) {
	setHome(t, t.TempDir())
	if err := Set("volc.speech.app_id", "123456789"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %v, want -rwx for 0600", info.Mode().Perm())
	}
	kvs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kv := range kvs {
		if kv.Key == "volc.speech.app_id" {
			found = true
			if kv.Value != "1******9" && !strings.Contains(kv.Value, "*") {
				t.Errorf("secret not masked: %q", kv.Value)
			}
		}
	}
	if !found {
		t.Error("app_id not listed")
	}
	cfg, _ := Load()
	if cfg.Volc.Speech.AppID != "123456789" {
		t.Errorf("AppID = %q", cfg.Volc.Speech.AppID)
	}
	_ = filepath.Join // keep import
}

func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // windows
}

// TestWatchHotReload 监听配置文件变更并以重读结果回调（serve 热加载外部变更的基础）。
func TestWatchHotReload(t *testing.T) {
	setHome(t, t.TempDir())
	old := watchDebounce
	watchDebounce = 20 * time.Millisecond
	t.Cleanup(func() { watchDebounce = old })

	got := make(chan *Config, 8)
	stop, err := Watch(func(c *Config) { got <- c })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	time.Sleep(200 * time.Millisecond) // viper 的 watcher goroutine 异步建立

	if err := Set("volc.speech.app_id", "watch-1"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		select {
		case c := <-got:
			if c.Volc.Speech.AppID == "watch-1" {
				return
			}
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("配置变更未触发回调")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestWatchStop 后 stop 不再派发回调。
func TestWatchStop(t *testing.T) {
	setHome(t, t.TempDir())
	old := watchDebounce
	watchDebounce = 20 * time.Millisecond
	t.Cleanup(func() { watchDebounce = old })

	fired := make(chan struct{}, 4)
	stop, err := Watch(func(*Config) { fired <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	stop()
	if err := Set("volc.speech.app_id", "after-stop"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
		t.Fatal("stop 后不应再回调")
	case <-time.After(500 * time.Millisecond):
	}
}

// TestWatchRenameStyleSave 验证 darwin kqueue 会漏事件的原子替换保存
// （写同目录临时文件+改名覆盖，即 sed -i / vim 的行为）由 stat 轮询兜底捕获。
func TestWatchRenameStyleSave(t *testing.T) {
	setHome(t, t.TempDir())
	old := watchDebounce
	watchDebounce = 20 * time.Millisecond
	t.Cleanup(func() { watchDebounce = old })

	got := make(chan *Config, 8)
	stop, err := Watch(func(c *Config) { got <- c })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, []byte("volc:\n    speech:\n        app_id: renamed-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, Path()); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		select {
		case c := <-got:
			if c.Volc.Speech.AppID == "renamed-1" {
				return
			}
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("原子替换保存未被轮询兜底捕获")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStorageChannelMigration 旧版平铺 storage.* 字段读取时一次性迁移进
// storage.providers.<名> 通道段（内存态；盘上旧键残留无害，viper 无删键能力，
// 新段一旦存在旧键不再参与读取）。
func TestStorageChannelMigration(t *testing.T) {
	setHome(t, t.TempDir())
	old := "storage:\n  provider: tos\n  endpoint: tos-cn-beijing.volces.com\n  region: cn-beijing\n" +
		"  bucket: old-bucket\n  access_key: OLD_AK\n  secret_key: OLD_SK\n  prefix: pp\n"
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := cfg.StorageChannels["tos"]
	if !ok {
		t.Fatalf("tos channel not migrated: %+v", cfg.StorageChannels)
	}
	if ch.Endpoint != "tos-cn-beijing.volces.com" || ch.Bucket != "old-bucket" ||
		ch.SecretKey != "OLD_SK" {
		t.Fatalf("migrated channel = %+v", ch)
	}
	if cfg.Storage.Provider != "tos" || cfg.Storage.Bucket != "old-bucket" {
		t.Fatalf("flattened view = %+v", cfg.Storage)
	}
}

// TestStorageChannelSelection 新 schema：storage.provider 为启用通道选择器（空=停用），
// providers.<名> 为各类型独立配置；停用只归零拍平视图，切回选择器即恢复。
func TestStorageChannelSelection(t *testing.T) {
	setHome(t, t.TempDir())
	body := "storage:\n  provider: \"\"\n  providers:\n    tos:\n      endpoint: ep\n      bucket: bk\n" +
		"      access_key: AK\n      secret_key: SK\n"
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Provider != "" || cfg.Storage.Bucket != "" {
		t.Fatalf("disabled flattened view should be zero: %+v", cfg.Storage)
	}
	if ch := cfg.StorageChannels["tos"]; ch.Bucket != "bk" || ch.SecretKey != "SK" {
		t.Fatalf("tos channel = %+v", ch)
	}

	// 选择器切回 tos：拍平视图从通道段恢复
	body = "storage:\n  provider: tos\n  providers:\n    tos:\n      endpoint: ep\n      bucket: bk\n"
	if err := os.WriteFile(Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Provider != "tos" || cfg.Storage.Bucket != "bk" || cfg.Storage.Endpoint != "ep" {
		t.Fatalf("re-enabled flattened view = %+v", cfg.Storage)
	}
}

// TestLoadQianwen 读取 qianwen.api_key 凭证（VOXBOX_HOME 指向临时目录，t.Setenv
// 测试结束自动还原，无需手动 Unsetenv）。
func TestLoadQianwen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VOXBOX_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".voxbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "qianwen:\n  api_key: sk-test-123\n"
	if err := os.WriteFile(filepath.Join(dir, ".voxbox", "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Qianwen.APIKey != "sk-test-123" {
		t.Errorf("Qianwen.APIKey = %q, 期望 sk-test-123", cfg.Qianwen.APIKey)
	}
}
