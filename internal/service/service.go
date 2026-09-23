// Package service 组装配置、存储、注册表与任务引擎，是 CLI 与 Web 的唯一共享入口。
package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/objectstorage"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
	"github.com/yann0917/voxbox/internal/provider/gsgc"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/task"
)

type Service struct {
	// cfg 原子指针：凭证热加载整体换新快照，读方（设置页/连通性测试/任务提交）
	// 始终拿到一致配置，与 HTTP handler 并发无数据竞争。
	cfg    atomic.Pointer[config.Config]
	db     *store.DB
	reg    *provider.Registry
	engine *task.Engine

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
	if err := gsgc.RegisterAll(reg, *cfg, dataDir); err != nil {
		return nil, err
	}
	// 音频剪辑（本地 ffmpeg）无凭证，注册一次即可，不参与设置热更新重注册。
	if err := audiotool.RegisterAll(reg, dataDir); err != nil {
		return nil, err
	}
	s := &Service{db: db, reg: reg}
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

// SaveCredentials 将非空凭证持久化到 ~/.voxbox/config.yaml 并热应用：
// 整体换新配置快照、以新凭证覆盖重注册火山与 MVSep 工具，Web 设置保存后即时生效，
// 无需重启。空值跳过（与设置页"留空表示不修改"语义一致）。进行中任务持有旧工具实例，不受影响。
func (s *Service) SaveCredentials(appID, accessToken, apiKey, mediaKitAPIKey, mvsepToken, mvsepBaseURL string) error {
	set := func(key, val string) error {
		if val == "" {
			return nil
		}
		return config.Set(key, val)
	}
	if err := errors.Join(
		set("volc.speech.app_id", appID),
		set("volc.speech.access_token", accessToken),
		set("volc.speech.api_key", apiKey),
		set("volc.mediakit.api_key", mediaKitAPIKey),
		set("mvsep.api_token", mvsepToken),
		set("mvsep.base_url", mvsepBaseURL),
	); err != nil {
		return err
	}
	nc := *s.cfg.Load()
	if appID != "" {
		nc.Volc.Speech.AppID = appID
	}
	if accessToken != "" {
		nc.Volc.Speech.AccessToken = accessToken
	}
	if apiKey != "" {
		nc.Volc.Speech.APIKey = apiKey
	}
	if mediaKitAPIKey != "" {
		nc.Volc.MediaKit.APIKey = mediaKitAPIKey
	}
	if mvsepToken != "" {
		nc.MVSep.APIToken = mvsepToken
	}
	// 线路（base_url）允许显式清空回落主站：与凭证不同，空串本身是合法取值（主站），
	// 故仅当值发生变化才写盘，且保存值直接生效。
	if mvsepBaseURL != nc.MVSep.BaseURL {
		if err := config.Set("mvsep.base_url", mvsepBaseURL); err != nil {
			return err
		}
		nc.MVSep.BaseURL = mvsepBaseURL
	}
	s.cfg.Store(&nc)
	volcengine.ReRegisterAll(s.reg, nc, nc.DataDir)
	mvsep.ReRegisterAll(s.reg, nc, nc.DataDir)
	return nil
}

// ReloadDiskConfig 从磁盘配置热应用运行期可变段：火山凭证 + 对象存储。
// 配置文件监听（config.Watch）的回调路径：服务运行中另一终端 voxbox config set、
// 手工编辑 config.yaml 的变更即时生效，与 Web 设置保存（SaveCredentials/SaveStorage
// 同步热应用）殊途同归。仅替换这两段：端口与数据目录是启动期属性（监听已绑定、
// DB 已打开），不跟随文件变更。
func (s *Service) ReloadDiskConfig(disk *config.Config) {
	nc := *s.cfg.Load()
	nc.Volc = disk.Volc
	nc.Storage = disk.Storage
	nc.StorageChannels = disk.StorageChannels
	nc.MVSep = disk.MVSep
	s.cfg.Store(&nc)
	volcengine.ReRegisterAll(s.reg, nc, nc.DataDir)
	mvsep.ReRegisterAll(s.reg, nc, nc.DataDir)
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

func (s *Service) Close() error { return nil } // gorm/sqlite 由进程退出回收；预留关闭钩子

// TestMVSepConnection MVSep 连通性探测：GET /api/app/user 验证 token 并顺带取回
// 账户名；未配置时直接报未配置，不发请求。
func (s *Service) TestMVSepConnection() (string, bool) {
	cfg := s.cfg.Load()
	if cfg.MVSep.APIToken == "" {
		return "未配置 MVSep API Token：请执行 voxbox config set mvsep.api_token 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := mvsep.New(cfg.MVSep.APIToken, cfg.MVSep.BaseURL)
	u, err := c.User(ctx)
	if err != nil {
		return err.Error(), false
	}
	qs, qerr := c.Queue(ctx)
	if qerr == nil && qs.FreeMax > 0 {
		return fmt.Sprintf("连接成功（%s，今日免费分离余量 %d/%d）", u.Name, qs.FreeLeft, qs.FreeMax), true
	}
	return fmt.Sprintf("连接成功（%s）", u.Name), true
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

// TestSpeechConnection 用音色/凭证连通性检测：构造 TTS 客户端发 1 字合成请求。
// 注意：真实调用会消耗少量合成配额，可接受。
func (s *Service) TestSpeechConnection() (string, bool) {
	cfg := s.cfg.Load()
	cred := volcengine.SpeechCred{
		AppID: cfg.Volc.Speech.AppID, AccessToken: cfg.Volc.Speech.AccessToken, APIKey: cfg.Volc.Speech.APIKey,
	}
	if err := cred.Validate(); err != nil {
		return err.Error(), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := volcengine.NewTTSClient(cred)
	_, err := client.Synthesize(ctx, volcengine.TTSSynthesizeReq{Text: "测", VoiceType: "zh_female_cancan_mars_bigtts", Format: "mp3"})
	if err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}

// TestMediaKitConnection MediaKit 连通性探测（与人声分离工具同域、同 Bearer 鉴权头）：
// GET 一个必然不存在的任务 ID——404/400 表示鉴权通过（任务不存在属预期）→ 连接成功；
// 401/403 → 凭证无效；网络错误透传错误信息。未配置 apiKey 时直接报未配置，不发起请求。
func (s *Service) TestMediaKitConnection() (string, bool) {
	if s.cfg.Load().Volc.MediaKit.APIKey == "" {
		return "未配置 AI MediaKit API Key：请执行 voxbox config set volc.mediakit.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		volcengine.MediaKitBaseURL+fmt.Sprintf(volcengine.MediaKitQueryPathFmt, "nonexistent-connectivity-probe"), nil)
	if err != nil {
		return err.Error(), false
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Load().Volc.MediaKit.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error(), false
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "凭证无效", false
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest:
		return "连接成功", true
	default:
		return fmt.Sprintf("MediaKit 探测异常(HTTP %d)", resp.StatusCode), false
	}
}
