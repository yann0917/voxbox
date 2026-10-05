// Package service 组装配置、存储、注册表与任务引擎，是 CLI 与 Web 的唯一共享入口。
package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/objectstorage"
	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
	"github.com/yann0917/voxbox/internal/provider/gsgc"
	"github.com/yann0917/voxbox/internal/provider/local"
	"github.com/yann0917/voxbox/internal/provider/minimax"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
	"github.com/yann0917/voxbox/internal/provider/openrouter"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
	"github.com/yann0917/voxbox/internal/voicelib"
)

type Service struct {
	// cfg 原子指针：凭证热加载整体换新快照，读方（设置页/连通性测试/任务提交）
	// 始终拿到一致配置，与 HTTP handler 并发无数据竞争。
	cfg    atomic.Pointer[config.Config]
	db     *store.DB
	reg    *provider.Registry
	engine *task.Engine

	// models 本地语音模型管理器:构造期扫盘恢复,与 config 热更新无关(模型目录随 dataDir)。
	models *localmodel.Manager

	// voices 音色库(参考音频,磁盘即真相不落 DB):目录随 dataDir,启动期一次构造。
	voices *voicelib.Library

	// ttsRuntime 本地合成引擎(audiocpp_server)生命周期管理:Close 时回收子进程。
	ttsRuntime *localruntime.TTSRuntime

	// storageMu 守护对象存储客户端的替换（Web 保存存储配置 / 配置文件监听热更新）。
	// 任务提交经 Engine.SetStorageClient 的 getter 取当前客户端，进行中任务不受替换影响。
	// storageInitErr 记录最近一次客户端构造失败的原因（配置非法时 client 为 nil，
	// 若连错误一起丢弃，设置页只会看到"未配置"，无法定位）。
	storageMu       sync.RWMutex
	storageClient   objectstorage.Client
	storageInitErr  error
	storageInitSeen bool
}

func New(cfg *config.Config) (*Service, error) {
	return newWithRoot(cfg)
}

// NewWithHome 以 home 为 ~/.voxbox 根的测试构造。
func NewWithHome(home string) (*Service, error) {
	cfg, err := loadFor(home)
	if err != nil {
		return nil, err
	}
	return newWithRoot(cfg)
}

func loadFor(home string) (*config.Config, error) {
	// 测试场景：直接构造默认配置，DataDir 指向 home/data
	return &config.Config{
		Server:  config.ServerConfig{Port: 0},
		DataDir: filepath.Join(home, "data"),
	}, nil
}

func newWithRoot(cfg *config.Config) (*Service, error) {
	dataDir := cfg.DataDir
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	pronunciation.Init(dataDir) // 发音词典进程单例：TTS 工具合成前文本预处理共用
	db, err := store.Open(filepath.Join(dataDir, "voxbox.db"))
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	reg := provider.NewRegistry()
	if err := volcengine.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := mvsep.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := qianwen.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := xiaomi.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := zhipu.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := openrouter.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := minimax.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	if err := gsgc.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	// 音频剪辑（本地 ffmpeg）无凭证，注册一次即可，不参与设置热更新重注册。
	if err := audiotool.RegisterAll(reg, dataDir); err != nil {
		return nil, err
	}
	models := localmodel.NewManager(dataDir)
	s := &Service{db: db, reg: reg, models: models, voices: voicelib.New(dataDir)}
	// 本地推理工具注册:与 audiotool 同为无凭证本地能力,注册一次不参与热更新重注册。
	ttsRuntime := localruntime.NewTTSRuntime(dataDir, models)
	if err := local.RegisterAll(reg, dataDir, models, ttsRuntime, s.voices); err != nil {
		return nil, err
	}
	s.ttsRuntime = ttsRuntime
	s.cfg.Store(cfg)
	s.rebuildStorageClient(cfg.Storage)
	return s, nil
}

func (s *Service) StartEngine(notify func(task.Event), concurrency int) {
	s.engine = task.New(s.db, s.reg, s.cfg.Load().DataDir, concurrency, notify)
	s.engine.SetStorageClient(s.StorageClient)
}

// StorageClient 当前对象存储客户端（未配置返回 nil），任务引擎经此热取。
// 返回窄接口（Put/PresignGet）：工具只见转存所需的最小能力。
func (s *Service) StorageClient() provider.StorageClient {
	s.storageMu.RLock()
	defer s.storageMu.RUnlock()
	return s.storageClient
}

// rebuildStorageClient 按存储段配置重建客户端（启动与热更新共用）。
// 配置为「未启用」（provider 空）时置 nil 且无错误；配置给了 provider 但不完整或非法时
// 置 nil 并记录错误，供设置页探活时给出可定位的提示。
func (s *Service) rebuildStorageClient(sc config.StorageConfig) {
	cli, err := objectstorage.New(objectstorage.Config{
		Provider:  sc.Provider,
		Endpoint:  sc.Endpoint,
		Region:    sc.Region,
		Bucket:    sc.Bucket,
		AccessKey: sc.AccessKey,
		SecretKey: sc.SecretKey,
		Prefix:    sc.Prefix,
	})
	s.storageMu.Lock()
	s.storageClient = cli
	s.storageInitErr = err
	s.storageInitSeen = true
	s.storageMu.Unlock()
}

func (s *Service) Engine() *task.Engine {
	if s.engine == nil {
		panic("engine not started: call StartEngine first")
	}
	return s.engine
}

func (s *Service) DB() *store.DB                { return s.db }
func (s *Service) Registry() *provider.Registry { return s.reg }
func (s *Service) Config() *config.Config       { return s.cfg.Load() }

// LocalModels 本地语音模型管理器(设置页模型区与 /api/models 消费)。
func (s *Service) LocalModels() *localmodel.Manager { return s.models }

// TTSRuntime 本地推理引擎生命周期管理(实时字幕 live 中转消费:模型解析与会话建立)。
func (s *Service) TTSRuntime() *localruntime.TTSRuntime { return s.ttsRuntime }

// VoiceLibrary 音色库(参考音频管理,音色库端点与克隆配音类工具消费)。
func (s *Service) VoiceLibrary() *voicelib.Library { return s.voices }

// SaveProviderFields 保存一张凭证卡的字段并热应用：按卡声明校验（未知卡/未知字段拒绝），
// secret 留空=不修改，text/select 按提交值落盘（select 空串=合法取值，如 MVSep 主站）。
// 成功后只重注册该卡对应的工具集，进行中任务持有旧实例不受影响。
func (s *Service) SaveProviderFields(name string, fields map[string]string) error {
	var card *provider.ProviderInfo
	for _, c := range providerCards() {
		if c.Name == name {
			cc := c
			card = &cc
			break
		}
	}
	if card == nil || card.Kind != provider.KindCloud {
		return fmt.Errorf("未知凭证卡: %s", name)
	}
	for k := range fields {
		known := false
		for _, f := range card.Fields {
			if f.Key == k {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("字段不属于 %s 卡: %s", name, k)
		}
	}
	for _, f := range card.Fields {
		v, ok := fields[f.Key]
		if !ok || (f.Kind == provider.FieldSecret && v == "") {
			continue // 未提交或 secret 留空 = 不修改
		}
		if err := config.Set(f.ConfigKey, v); err != nil {
			return err
		}
	}
	nc := *s.cfg.Load()
	card.Apply(&nc, fields)
	s.cfg.Store(&nc)
	card.ReRegister(s.reg, nc, nc.DataDir)
	return nil
}

// ReloadDiskConfig 从磁盘配置热应用运行期可变段：各云端卡凭证段 + 对象存储 + AI 默认大模型。
// 配置文件监听（config.Watch）的回调路径：服务运行中另一终端 voxbox config set、
// 手工编辑 config.yaml 的变更即时生效，与 Web 设置保存（SaveProviderFields/SaveStorage
// 同步热应用）殊途同归。卡管辖的段经卡自描述的 Sync 逐卡并入；Storage/StorageChannels/
// Assistant 不属于任何卡，保留显式赋值。仅替换这些段：端口与数据目录是启动期属性
// （监听已绑定、DB 已打开），不跟随文件变更。
func (s *Service) ReloadDiskConfig(disk *config.Config) {
	nc := *s.cfg.Load()
	for _, c := range providerCards() {
		if c.Sync != nil {
			c.Sync(&nc, disk)
		}
	}
	nc.Storage = disk.Storage
	nc.StorageChannels = disk.StorageChannels
	nc.Assistant = disk.Assistant
	s.cfg.Store(&nc)
	// 逐卡热重注册：volcengine 与 mediakit 两卡共用 volcengine.ReRegisterAll 会重复执行
	// 一次——Registry.Replace 幂等、重建实例无副作用，不为此去重。
	for _, c := range providerCards() {
		if c.ReRegister != nil {
			c.ReRegister(s.reg, nc, nc.DataDir)
		}
	}
	s.rebuildStorageClient(nc.Storage)
}

// SaveStorage 持久化对象存储配置到 config.yaml 并热应用（重建客户端，下一任务即用新通道）。
// 各存储类型的连接参数落独立通道段（storage.providers.<名>.*），storage.provider 只记录
// 当前启用哪个：切换类型互不覆盖，切回即恢复原值。表单语义：provider 指定保存并启用的
// 类型（留空=仅停用，各通道段原样保留）；secret_key 留空=沿用该通道已存值。
func (s *Service) SaveStorage(sc config.StorageConfig) error {
	sc.Provider = strings.TrimSpace(sc.Provider)
	switch sc.Provider {
	case "":
	case "tos":
	case "oss":
	default:
		return fmt.Errorf("暂不支持该对象存储 provider %q（当前支持 tos/oss，其他 S3 兼容通道规划中）", sc.Provider)
	}
	// 克隆后再改：StorageChannels 与已发布快照共享底map，直接写会与并发读旧快照竞争。
	channels := maps.Clone(s.cfg.Load().StorageChannels)
	if channels == nil {
		channels = map[string]config.StorageConfig{}
	}
	stored := channels[sc.Provider]
	if sc.Provider != "" {
		missing := []string{}
		for k, v := range map[string]string{
			"endpoint": sc.Endpoint, "region": sc.Region, "bucket": sc.Bucket, "access_key": sc.AccessKey,
		} {
			if strings.TrimSpace(v) == "" {
				missing = append(missing, k)
			}
		}
		// secret 留空且该通道已存值也为空才算缺：留空=沿用该通道已存 SK。
		if sc.SecretKey == "" && stored.SecretKey == "" {
			missing = append(missing, "secret_key")
		}
		if len(missing) > 0 {
			return fmt.Errorf("启用对象存储需填写: %s", strings.Join(missing, ", "))
		}
	}
	if err := config.Set("storage.provider", sc.Provider); err != nil {
		return err
	}
	eff := config.StorageConfig{}
	if sc.Provider != "" {
		prefix := config.StorageChannelPrefix + sc.Provider + "."
		if err := errors.Join(
			config.Set(prefix+"endpoint", strings.TrimSpace(sc.Endpoint)),
			config.Set(prefix+"region", strings.TrimSpace(sc.Region)),
			config.Set(prefix+"bucket", strings.TrimSpace(sc.Bucket)),
			config.Set(prefix+"access_key", strings.TrimSpace(sc.AccessKey)),
			config.Set(prefix+"prefix", strings.TrimSpace(sc.Prefix)),
		); err != nil {
			return err
		}
		if sc.SecretKey != "" {
			if err := config.Set(prefix+"secret_key", sc.SecretKey); err != nil {
				return err
			}
		}
		ch := config.StorageConfig{
			Endpoint:  strings.TrimSpace(sc.Endpoint),
			Region:    strings.TrimSpace(sc.Region),
			Bucket:    strings.TrimSpace(sc.Bucket),
			AccessKey: strings.TrimSpace(sc.AccessKey),
			SecretKey: sc.SecretKey,
			Prefix:    strings.TrimSpace(sc.Prefix),
		}
		if ch.SecretKey == "" {
			// 提交留空：沿用该通道已存 SK（missing 校验已保证存在），内存与盘一致。
			ch.SecretKey = stored.SecretKey
		}
		channels[sc.Provider] = ch
		eff = ch
		eff.Provider = sc.Provider
	}
	// 同步热应用内存快照（磁盘权威值由 ReloadDiskConfig 兜底一致）。
	nc := *s.cfg.Load()
	nc.Storage, nc.StorageChannels = eff, channels
	s.cfg.Store(&nc)
	s.rebuildStorageClient(eff)
	return nil
}

// SaveDataDir 校验并持久化数据保存位置（config.yaml 的 data_dir 键），重启生效：
// 监听与 SQLite 连接是启动期属性，运行期不换（ReloadDiskConfig 对 DataDir 同口径不热应用），
// 因此不做内存快照替换——GET 回显的 data_dir 在重启前保持当前生效值。
// 规则：非空、绝对路径；~/ 前缀展开用户主目录（os 包不处理壳层波浪号）；展开后 Clean。
// 与当前生效值相同的写入视为无操作拒绝（前端同值禁用保存按钮，正常流不会触达）。
// 返回展开后的最终目录供端点回显。
func (s *Service) SaveDataDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("目录不能为空")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法定位用户主目录: %w", err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("目录必须是绝对路径（以 / 或盘符开头）")
	}
	// 卷根防呆（/ 或 C:\）：数据库与产物会直接落在文件系统根，几乎总是误填。
	if dir == filepath.VolumeName(dir)+string(os.PathSeparator) {
		return "", fmt.Errorf("不能使用磁盘根目录，请选择一个专门的文件夹")
	}
	if dir == s.cfg.Load().DataDir {
		return "", fmt.Errorf("与当前目录相同，无需修改")
	}
	if err := config.Set("data_dir", dir); err != nil {
		return "", err
	}
	return dir, nil
}

// TestStorageConnection 对象存储探活（HeadBucket）：桶可达且凭证有效即成功。
// 未就绪时按原因区分提示（未配置 / 配了参数但没选存储类型 / 初始化失败），可定位。
func (s *Service) TestStorageConnection() (string, bool) {
	s.storageMu.RLock()
	cli, initErr, seen := s.storageClient, s.storageInitErr, s.storageInitSeen
	s.storageMu.RUnlock()
	if cli == nil {
		sc := s.cfg.Load().Storage
		switch {
		case sc.Provider == "" && (sc.Endpoint != "" || sc.Bucket != ""):
			// 其他参数在、唯独 provider 空：多半是表单保存丢了选择（如 FormData 读不到自定义 Select）
			return "存储类型未选择（endpoint/bucket 等已填写）：请在设置页选择存储类型后重新保存", false
		case seen && initErr != nil:
			return "存储配置未生效：" + initErr.Error(), false
		default:
			return "对象存储未配置", false
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cli.Ping(ctx); err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}

// Close 回收本地合成引擎子进程(gorm/sqlite 由进程退出回收)。
func (s *Service) Close() error {
	if s.ttsRuntime != nil {
		return s.ttsRuntime.Close()
	}
	return nil
}

// MVSepAlgorithms 拉取 MVSep 算法列表（带 token 才返回分组名），进程内缓存 1 小时。
func (s *Service) MVSepAlgorithms(ctx context.Context, refresh bool) ([]mvsep.Algorithm, error) {
	cfg := s.cfg.Load()
	return mvsepAlgoCache.get(ctx, cfg.MVSep.APIToken, cfg.MVSep.BaseURL, refresh)
}

// MVSepUserQueue 账户信息 + 站点队列/免费额度（分离页头部展示）。
func (s *Service) MVSepUserQueue(ctx context.Context) (*mvsep.User, *mvsep.QueueStatus, error) {
	cfg := s.cfg.Load()
	if cfg.MVSep.APIToken == "" {
		return nil, nil, fmt.Errorf("未配置 MVSep API Token：请在设置页填写")
	}
	c := mvsep.New(cfg.MVSep.APIToken, cfg.MVSep.BaseURL)
	u, err := c.User(ctx)
	if err != nil {
		return nil, nil, err
	}
	qs, qerr := c.Queue(ctx)
	if qerr != nil {
		qs = nil // 队列查询失败不阻断账户信息展示
	}
	return u, qs, nil
}

// MVSepHistory MVSep 云端分离历史（近期任务，供分离页补拉结果）。
func (s *Service) MVSepHistory(ctx context.Context, start, limit int) ([]mvsep.HistoryItem, error) {
	cfg := s.cfg.Load()
	if cfg.MVSep.APIToken == "" {
		return nil, fmt.Errorf("未配置 MVSep API Token：请在设置页填写")
	}
	return mvsep.New(cfg.MVSep.APIToken, cfg.MVSep.BaseURL).History(ctx, start, limit)
}

// mvsepTTL 算法列表缓存时长：上游变更频率低且限频 60/分钟，1 小时足够。
const mvsepTTL = time.Hour

// mvsepAlgoCache 算法列表进程内缓存：仅保留最近一份成功结果，refresh 绕过；
// token/线路变更（缓存键不匹配）即视为过期。
type mvsepAlgoCacheT struct {
	sync.Mutex
	key     string // token + "|" + baseURL
	data    []mvsep.Algorithm
	expires time.Time
}

var mvsepAlgoCache mvsepAlgoCacheT

func (m *mvsepAlgoCacheT) get(ctx context.Context, token, baseURL string, refresh bool) ([]mvsep.Algorithm, error) {
	m.Lock()
	defer m.Unlock()
	key := token + "|" + baseURL
	if !refresh && m.data != nil && m.key == key && time.Now().Before(m.expires) {
		return m.data, nil
	}
	algos, err := mvsep.New(token, baseURL).Algorithms(ctx)
	if err != nil {
		return nil, err
	}
	m.key, m.data, m.expires = key, algos, time.Now().Add(mvsepTTL)
	return algos, nil
}
