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
	if len(metas) != 41 { // 火山 8 + MVSep 1 + 千问 2 + 小米 2 + 智谱 4 + gsgc 11（1 分离 + 10 站点功能）+ zhuanhuanmao 1 分离 + 音频剪辑 12
		t.Fatalf("registered tools = %d, want 41", len(metas))
	}
	if _, ok := svc.Registry().Get("zhipu", "tts"); !ok {
		t.Error("zhipu.tts not found")
	}
	if _, ok := svc.Registry().Get("zhipu", "voice_clone"); !ok {
		t.Error("zhipu.voice_clone not found")
	}
	if _, ok := svc.Registry().Get("xiaomi", "tts"); !ok {
		t.Error("xiaomi.tts not found")
	}
	if _, ok := svc.Registry().Get("xiaomi", "asr"); !ok {
		t.Error("xiaomi.asr not found")
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

// TestSaveProviderFieldsHotReload 验证多卡按卡保存即时生效：内存配置换快照、
// 工具覆盖重注册、YAML 持久化，且 secret 留空不覆盖已有凭证。
// 注意：SaveProviderFields 写 $HOME/.voxbox/config.yaml，须先隔离 HOME。
func TestSaveProviderFieldsHotReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	svc := newTestService(t)

	if got := svc.Config().Volc.Speech.AppID; got != "" {
		t.Fatalf("initial app id = %q, want empty", got)
	}
	if err := svc.SaveProviderFields("volcengine", map[string]string{
		"app_id": "app-1", "access_token": "tok-1", "api_key": "key-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProviderFields("mediakit", map[string]string{"api_key": "mk-1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProviderFields("mvsep", map[string]string{"api_token": "mv-1"}); err != nil {
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
	if len(svc.Registry().List()) != 41 {
		t.Errorf("List len = %d, want 41（火山 8 + MVSep 1 + 千问 2 + 小米 2 + 智谱 4 + gsgc 11 + zhuanhuanmao 1 + 音频剪辑 12）", len(svc.Registry().List()))
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
	// 部分保存：secret 留空跳过，其余字段保留
	if err := svc.SaveProviderFields("volcengine", map[string]string{"app_id": "app-2"}); err != nil {
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
	if err := svc.SaveProviderFields("volcengine", map[string]string{"app_id": "app-1", "access_token": "tok-1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProviderFields("mediakit", map[string]string{"api_key": "mk-1"}); err != nil {
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
	if len(svc.Registry().List()) != 41 {
		t.Errorf("List len = %d, want 41（火山 8 + MVSep 1 + 千问 2 + 小米 2 + 智谱 4 + gsgc 11 + zhuanhuanmao 1 + 音频剪辑 12）", len(svc.Registry().List()))
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

// SaveProviderFields：字段落盘、快照更新、secret 留空不改、未知字段拒绝、热重注册触发。
func TestSaveProviderFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VOXBOX_HOME", home)
	svc, err := NewWithHome(home)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// 1) 保存千问 key：落盘 + 快照可见
	if err := svc.SaveProviderFields("qianwen", map[string]string{"api_key": "sk-1"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Qianwen.APIKey; got != "sk-1" {
		t.Errorf("快照 Qianwen.APIKey = %q", got)
	}
	disk, err := config.Load()
	if err != nil || disk.Qianwen.APIKey != "sk-1" {
		t.Errorf("落盘不符: %q err=%v", disk.Qianwen.APIKey, err)
	}
	// 守护「secret 不回传值」：设置态里千问 secret 字段只回 has_value，不带值。
	for _, st := range svc.ProviderStates(svc.Config()) {
		if st.Name != "qianwen" {
			continue
		}
		for _, f := range st.Fields {
			if f.Key == "api_key" && (f.Kind != provider.FieldSecret || !f.HasValue || f.Value != "") {
				t.Errorf("secret 字段应只回 has_value: %+v", f)
			}
		}
	}

	// 2) secret 留空 = 不改（空串提交不覆盖）
	if err := svc.SaveProviderFields("qianwen", map[string]string{"api_key": ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Qianwen.APIKey; got != "sk-1" {
		t.Errorf("secret 留空不应清空: %q", got)
	}

	// 3) 未知字段拒绝
	if err := svc.SaveProviderFields("qianwen", map[string]string{"hacker": "x"}); err == nil {
		t.Error("未知字段应报错")
	}

	// 4) 未知卡拒绝
	if err := svc.SaveProviderFields("nope", map[string]string{}); err == nil {
		t.Error("未知卡应报错")
	}

	// 5) 热重注册：保存火山凭证后注册表内 tts 工具可取出（凭证已进实例）
	if err := svc.SaveProviderFields("volcengine", map[string]string{"api_key": "vk-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Registry().Get("volcengine", "tts"); !ok {
		t.Error("volcengine.tts 应已注册")
	}
	if _, ok := svc.Registry().Get("qianwen", "tts"); !ok {
		t.Error("qianwen.tts 应已注册（Task 7 newWithRoot 注册）")
	}

	// 6) text 字段空串=清空（与 secret 留空不改相对）：app_id 提交空串后，
	//    内存快照与 config.Load() 落盘均应为空。
	if err := svc.SaveProviderFields("volcengine", map[string]string{"app_id": "a"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Volc.Speech.AppID; got != "a" {
		t.Fatalf("app_id = %q, want a", got)
	}
	if err := svc.SaveProviderFields("volcengine", map[string]string{"app_id": ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Config().Volc.Speech.AppID; got != "" {
		t.Errorf("text 字段空串提交应清空快照: app_id = %q", got)
	}
	disk, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := disk.Volc.Speech.AppID; got != "" {
		t.Errorf("text 字段空串提交应清空落盘: app_id = %q", got)
	}
}

// TestQianwenConnection：未配置直接报未配置。
func TestQianwenConnectionUnconfigured(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	msg, ok := svc.TestQianwenConnection()
	if ok || msg == "" {
		t.Errorf("未配置应 (false, 提示), got (%v, %q)", ok, msg)
	}
}

// TestXiaomiConnection：未配置直接报未配置（不发请求）。
func TestXiaomiConnectionUnconfigured(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	msg, ok := svc.TestXiaomiConnection()
	if ok || msg == "" {
		t.Errorf("未配置应 (false, 提示), got (%v, %q)", ok, msg)
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

// TestZhipuConnection：未配置直接报未配置（不发请求）。
func TestZhipuConnectionUnconfigured(t *testing.T) {
	svc, err := NewWithHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	msg, ok := svc.TestZhipuConnection()
	if ok || msg == "" {
		t.Errorf("未配置应 (false, 提示), got (%v, %q)", ok, msg)
	}
}
