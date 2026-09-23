package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
)

func TestNewRegistersTools(t *testing.T) {
	svc := newTestService(t)
	metas := svc.Registry().List()
	if len(metas) != 33 { // 火山 8 + MVSep 1 + gsgc 11（1 分离 + 10 站点功能）+ zhuanhuanmao 1 分离 + 音频剪辑 12
		t.Fatalf("registered tools = %d, want 33", len(metas))
	}
	if _, ok := svc.Registry().Get("mvsep", "separate"); !ok {
		t.Error("mvsep.separate not found")
	}
	if _, ok := svc.Registry().Get("gsgc", "separate"); !ok {
		t.Error("gsgc.separate not found")
	}
	if _, ok := svc.Registry().Get("zhuanhuanmao", "separate"); !ok {
		t.Error("zhuanhuanmao.separate not found")
	}
	if _, ok := svc.Registry().Get("volcengine", "tts"); !ok {
		t.Error("volcengine.tts not found")
	}
	if _, ok := svc.Registry().Get("volcengine", "asr"); !ok {
		t.Error("volcengine.asr not found")
	}
	if _, ok := svc.Registry().Get("volcengine", "podcast"); !ok {
		t.Error("volcengine.podcast not found")
	}
	if _, ok := svc.Registry().Get("volcengine", "separate"); !ok {
		t.Error("volcengine.separate not found")
	}
}

func TestSubmitUnknownTool(t *testing.T) {
	svc := newTestService(t)
	if _, _, err := svc.Engine().SubmitSync(t.Context(), "volcengine", "nonexistent", map[string]any{}, nil); err == nil {
		t.Error("unknown tool should fail")
	}
}

// TestSaveCredentialsHotReload 验证凭证保存即时生效：内存配置换快照、
// 工具覆盖重注册、YAML 持久化，且空值不覆盖已有凭证。
// 注意：SaveCredentials 写 $HOME/.voxbox/config.yaml，须先隔离 HOME。
func TestSaveCredentialsHotReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := newTestService(t)

	if got := svc.Config().Volc.Speech.AppID; got != "" {
		t.Fatalf("initial app id = %q, want empty", got)
	}
	if err := svc.SaveCredentials("app-1", "tok-1", "key-1", "mk-1", "mv-1", ""); err != nil {
		t.Fatal(err)
	}
	cfg := svc.Config()
	if cfg.Volc.Speech.AppID != "app-1" || cfg.Volc.Speech.AccessToken != "tok-1" ||
		cfg.Volc.Speech.APIKey != "key-1" || cfg.Volc.MediaKit.APIKey != "mk-1" {
		t.Fatalf("in-memory config not hot-applied: %+v %+v", cfg.Volc.Speech, cfg.Volc.MediaKit)
	}
	// 覆盖重注册后工具集完整，且未触发"重复注册"报错
	if _, ok := svc.Registry().Get("volcengine", "tts"); !ok {
		t.Error("volcengine.tts missing after hot reload")
	}
	if len(svc.Registry().List()) != 33 {
		t.Errorf("List len = %d, want 33（火山 8 + MVSep 1 + gsgc 11 + zhuanhuanmao 1 + 音频剪辑 12）", len(svc.Registry().List()))
	}
	// 持久化：重读磁盘配置与内存一致
	persisted, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Volc.Speech.AppID != "app-1" || persisted.Volc.MediaKit.APIKey != "mk-1" {
		t.Fatalf("persisted config mismatch: %+v", persisted.Volc)
	}
	if persisted.MVSep.APIToken != "mv-1" {
		t.Fatalf("mvsep token 未持久化: %+v", persisted.MVSep)
	}
	// 部分保存：空值跳过，其余字段保留
	if err := svc.SaveCredentials("app-2", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg = svc.Config()
	if cfg.Volc.Speech.AppID != "app-2" || cfg.Volc.Speech.AccessToken != "tok-1" || cfg.Volc.MediaKit.APIKey != "mk-1" {
		t.Fatalf("partial save broke existing credentials: %+v", cfg.Volc)
	}
}

// TestReloadVolcFromDisk 配置文件监听回调路径：仅凭证段跟随磁盘配置，
// 端口/数据目录是启动期属性不跟随，工具集覆盖重注册后完整。
func TestReloadVolcFromDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := newTestService(t)
	if err := svc.SaveCredentials("app-1", "tok-1", "", "mk-1", "", ""); err != nil {
		t.Fatal(err)
	}
	before := svc.Config().DataDir

	svc.ReloadDiskConfig(&config.Config{
		Server:  config.ServerConfig{Port: 9999},
		DataDir: "/somewhere/else",
		Volc: config.VolcConfig{
			Speech:   config.SpeechConfig{AppID: "app-9", AccessToken: "tok-9", APIKey: "key-9"},
			MediaKit: config.MediaKitConfig{APIKey: "mk-9"},
		},
	})

	cfg := svc.Config()
	if cfg.Volc.Speech.AppID != "app-9" || cfg.Volc.Speech.APIKey != "key-9" || cfg.Volc.MediaKit.APIKey != "mk-9" {
		t.Fatalf("volc segment not applied: %+v", cfg.Volc)
	}
	if cfg.Server.Port == 9999 || cfg.DataDir != before {
		t.Errorf("启动期属性不应跟随磁盘配置: port=%d dataDir=%s", cfg.Server.Port, cfg.DataDir)
	}
	if _, ok := svc.Registry().Get("volcengine", "tts"); !ok {
		t.Error("volcengine.tts missing after reload")
	}
	if len(svc.Registry().List()) != 33 {
		t.Errorf("List len = %d, want 33（火山 8 + MVSep 1 + gsgc 11 + zhuanhuanmao 1 + 音频剪辑 12）", len(svc.Registry().List()))
	}
}

// newTestService 以临时目录构造 Service 并启动引擎（Engine() 未启动会 panic）。
func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.StartEngine(nil, 1)
	return svc
}

// fakeTool 记录引擎透传的 Files，用于验证 TaskInput.Files 链路。
type fakeTool struct{ gotFiles map[string]string }

func (f *fakeTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{Provider: "fake", Name: "fake", Title: "Fake"}
}
func (f *fakeTool) ParamSpecs() []provider.ParamSpec { return nil }
func (f *fakeTool) Run(_ context.Context, in provider.TaskInput, _ provider.ProgressReporter) (provider.TaskOutput, error) {
	f.gotFiles = in.Files
	return provider.TaskOutput{}, nil
}

// TestSubmitSyncWithFiles 验证引擎把 files 原样透传给工具的 TaskInput.Files。
func TestSubmitSyncWithFiles(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	reg := provider.NewRegistry()
	tool := &fakeTool{}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	e := task.New(db, reg, t.TempDir(), 1, nil)

	const audioPath = "/tmp/whatever.mp3"
	res, _, err := e.SubmitSync(t.Context(), "fake", "fake", map[string]any{}, map[string]string{"audio": audioPath})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != store.StatusSucceeded {
		t.Fatalf("status = %s, want succeeded (err=%s)", res.Status, res.Error)
	}
	if got := tool.gotFiles["audio"]; got != audioPath {
		t.Errorf("Files[audio] = %q, want %q", got, audioPath)
	}
}

// TestSaveStorageChannelSwitch 存储配置按类型分段：停用只清选择器、通道段保留；
// 重新启用 secret 留空沿用该通道已存 SK；落盘形态为选择器 + providers.<名> 通道段。
func TestSaveStorageChannelSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := newTestService(t)

	a := config.StorageConfig{Provider: "tos", Endpoint: "ep-a", Region: "r-a", Bucket: "b-a",
		AccessKey: "ak-a", SecretKey: "sk-a", Prefix: "p"}
	if err := svc.SaveStorage(a); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Storage; got.Provider != "tos" || got.Bucket != "b-a" || got.SecretKey != "sk-a" {
		t.Fatalf("enabled view = %+v", got)
	}

	// 停用：拍平视图归零，tos 通道段原样保留（切回不重填的根基）
	if err := svc.SaveStorage(config.StorageConfig{Provider: ""}); err != nil {
		t.Fatal(err)
	}
	cfg := svc.Config()
	if cfg.Storage.Provider != "" || cfg.Storage.Bucket != "" {
		t.Fatalf("disable should zero flattened view: %+v", cfg.Storage)
	}
	if ch := cfg.StorageChannels["tos"]; ch.Bucket != "b-a" || ch.SecretKey != "sk-a" {
		t.Fatalf("tos channel lost on disable: %+v", ch)
	}

	// 重新启用：secret 留空=沿用该通道已存 SK；其余字段按提交值更新
	if err := svc.SaveStorage(config.StorageConfig{Provider: "tos", Endpoint: "ep-b", Region: "r-a",
		Bucket: "b-a", AccessKey: "ak-a"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Storage; got.Provider != "tos" || got.Endpoint != "ep-b" || got.SecretKey != "sk-a" {
		t.Fatalf("re-enabled view = %+v", got)
	}

	// 落盘：重新读盘得到同样的分段形态（选择器 + 通道段）
	disk, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if disk.Storage.Provider != "tos" || disk.Storage.Endpoint != "ep-b" {
		t.Fatalf("disk selector/flat = %+v", disk.Storage)
	}
	if ch := disk.StorageChannels["tos"]; ch.Endpoint != "ep-b" || ch.SecretKey != "sk-a" {
		t.Fatalf("disk tos channel = %+v", ch)
	}
}

// TestSaveStorageOSSWhitelist oss 通道在白名单内可保存；未知 provider 拒绝并提示支持范围。
func TestSaveStorageOSSWhitelist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := newTestService(t)

	if err := svc.SaveStorage(config.StorageConfig{Provider: "oss", Endpoint: "oss-cn-hangzhou.aliyuncs.com",
		Region: "cn-hangzhou", Bucket: "b-oss", AccessKey: "ak", SecretKey: "sk"}); err != nil {
		t.Fatalf("oss 通道应可保存: %v", err)
	}
	if got := svc.Config().Storage; got.Provider != "oss" || got.Bucket != "b-oss" {
		t.Fatalf("oss 生效视图 = %+v", got)
	}

	err := svc.SaveStorage(config.StorageConfig{Provider: "cos", Endpoint: "e", Region: "r",
		Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err == nil || !strings.Contains(err.Error(), "tos/oss") {
		t.Fatalf("未知 provider 应拒绝并提示支持范围: %v", err)
	}
	// 拒绝不得污染已生效配置
	if got := svc.Config().Storage; got.Provider != "oss" {
		t.Fatalf("被拒保存后生效视图被污染: %+v", got)
	}
}
