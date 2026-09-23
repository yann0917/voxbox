// Package config 管理 ~/.voxbox/config.yaml：凭证、端口、数据目录。
package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

type Config struct {
	Server  ServerConfig
	DataDir string
	Volc    VolcConfig
	Storage StorageConfig
	// StorageChannels 各存储类型各自独立的完整配置（键=provider 名，如 "tos"）。
	// Storage 是其选中项的拍平视图：切换启用类型互不覆盖，切回即恢复。
	StorageChannels map[string]StorageConfig
	MVSep           MVSepConfig
	// Qianwen 千问平台（platform.qianwenai.com）凭证：语音合成/识别共用一个 API Key，
	// BaseURL 固定官方 maas.qianwenaiapi.com，不提供覆写。
	Qianwen QianwenConfig
}

type ServerConfig struct {
	Port int
	// Host 监听地址：默认 127.0.0.1（本地姿态不变）；公网部署经反代时保持本机回环，
	// 直连公网才显式配 0.0.0.0。
	Host string
}

type VolcConfig struct {
	Speech   SpeechConfig
	MediaKit MediaKitConfig
}

type SpeechConfig struct {
	AppID       string
	AccessToken string
	APIKey      string
}

type MediaKitConfig struct{ APIKey string }

// MVSepConfig MVSep（mvsep.com，音乐源分离）凭证：一个 api_token（mvsep.com 注册后在
// 全页 API 页获取）；BaseURL 留空走主站（geo 自动就近），可覆写为区域镜像
// （de/de2/hk.mvsep.com）——同一任务只能由提交时所在区域节点服务，生命周期须固定同一线路。
type MVSepConfig struct {
	APIToken string
	BaseURL  string
}

// QianwenConfig 千问平台凭证：语音合成/识别共用一个 API Key。
type QianwenConfig struct{ APIKey string }

// StorageConfig 对象存储（大文件中转）：语音识别/人声分离/妙记等 URL-only 工具的本地文件
// 会在任务执行时转存到该桶并取预签名 URL 提交上游。Provider 留空表示未启用；
// "tos" 为火山 TOS 官方 SDK 通道（S3 兼容通道为 OSS/腾讯 COS 预留，暂未开放）。
// 双重语义：作为 Config.Storage 时是「当前启用通道」的拍平生效视图（Provider=启用中的
// 类型）；作为 StorageChannels 的值时是单个类型的独立配置（Provider 字段忽略）。
// mapstructure tag 必须显式：UnmarshalKey 读 providers 段时，access_key 这类下划线键
// 与字段名的默认匹配不含「去下划线」归一（access_key≠AccessKey），缺 tag 会静默得到空值。
type StorageConfig struct {
	Provider      string `mapstructure:"provider"`
	Endpoint      string `mapstructure:"endpoint"`
	Region        string `mapstructure:"region"`
	Bucket        string `mapstructure:"bucket"`
	AccessKey     string `mapstructure:"access_key"`
	SecretKey     string `mapstructure:"secret_key"`
	Prefix        string `mapstructure:"prefix"`         // 对象 key 前缀，空则落桶根
	LifecycleDays int    `mapstructure:"lifecycle_days"` // 生命周期（天）：上游消费完即无用，桶内前缀对象到期自动清理；0 表示不设置
}

type KV struct{ Key, Value string }

// Dict 命名词典：热词（ASR/妙记）与术语（翻译）的可复用资产，落 config.yaml 的 dicts 段。
type Dict struct {
	Name     string `json:"name"`
	Hotwords string `json:"hotwords,omitempty"`
	Terms    string `json:"terms,omitempty"`
}

type DictEntry struct {
	Hotwords string `mapstructure:"hotwords"`
	Terms    string `mapstructure:"terms"`
}

// Dicts 读取全部命名词典，按名称排序；无 dicts 段时返回空切片。
func Dicts() ([]Dict, error) {
	entries, err := readDicts()
	if err != nil {
		return nil, err
	}
	out := make([]Dict, 0, len(entries))
	for name, e := range entries {
		out = append(out, Dict{Name: name, Hotwords: e.Hotwords, Terms: e.Terms})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// readDicts 注意：UnmarshalKey 把 key 处的「值」解码进目标，因此目标直接是
// map[string]DictEntry，不能再包一层 struct（字段对不上会静默得到空值）。
func readDicts() (map[string]DictEntry, error) {
	v := newFileViper()
	if err := v.ReadInConfig(); err != nil {
		if _, statErr := os.Stat(Path()); statErr == nil {
			return nil, fmt.Errorf("读取配置失败: %w", err)
		}
	}
	entries := map[string]DictEntry{}
	if err := v.UnmarshalKey("dicts", &entries); err != nil {
		return nil, fmt.Errorf("解析词典失败: %w", err)
	}
	return entries, nil
}

// SetDict 以整段替换的方式 upsert 一个词典（名称含 `.` 时 dotted Set 会拆错层级）。
func SetDict(name string, e DictEntry) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("词典名称不能为空")
	}
	entries, err := readDicts()
	if err != nil {
		return err
	}
	entries[name] = e
	return writeDicts(entries)
}

// RemoveDict 删除词典；不存在时报错。
func RemoveDict(name string) error {
	entries, err := readDicts()
	if err != nil {
		return err
	}
	if _, ok := entries[name]; !ok {
		return fmt.Errorf("词典不存在: %s", name)
	}
	delete(entries, name)
	return writeDicts(entries)
}

func writeDicts(m map[string]DictEntry) error {
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return err
	}
	v := viper.New()
	v.SetConfigFile(Path())
	v.SetConfigType("yaml")
	_ = v.ReadInConfig()
	v.Set("dicts", m)
	if err := v.WriteConfigAs(Path()); err != nil {
		return err
	}
	return os.Chmod(Path(), 0o600)
}

// homeDir 返回配置/数据根目录：默认用户主目录，VOXBOX_HOME 覆盖
// （服务器部署时数据与运行用户解耦，如 /var/lib/voxbox）。
func homeDir() string {
	if h := strings.TrimSpace(os.Getenv("VOXBOX_HOME")); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Path 返回配置文件路径 ~/.voxbox/config.yaml。
func Path() string {
	return filepath.Join(homeDir(), ".voxbox", "config.yaml")
}

func Load() (*Config, error) {
	v := newFileViper()
	// viper 对不存在的 SetConfigFile 返回 *fs.PathError，直接忽略任何读取错误：
	// 仅当文件确实存在时读取失败才报错。
	if err := v.ReadInConfig(); err != nil {
		if _, statErr := os.Stat(Path()); statErr == nil {
			return nil, fmt.Errorf("读取配置失败: %w", err)
		}
	}
	return configFromViper(v), nil
}

// newFileViper 绑定 config.yaml 并预置默认值（Load 与 Watch 共用）。
// Set 不走这里：写入路径预置默认值会把它们显式落进用户配置文件。
func newFileViper() *viper.Viper {
	v := viper.New()
	v.SetDefault("server.port", 8081)
	v.SetDefault("server.host", "127.0.0.1")
	v.SetDefault("data_dir", filepath.Join(homeDir(), ".voxbox", "data"))
	v.SetConfigFile(Path())
	v.SetConfigType("yaml")
	return v
}

func configFromViper(v *viper.Viper) *Config {
	st := storageFromViper(v)
	return &Config{
		Server:  ServerConfig{Port: v.GetInt("server.port"), Host: v.GetString("server.host")},
		DataDir: v.GetString("data_dir"),
		Volc: VolcConfig{
			Speech: SpeechConfig{
				AppID:       v.GetString("volc.speech.app_id"),
				AccessToken: v.GetString("volc.speech.access_token"),
				APIKey:      v.GetString("volc.speech.api_key"),
			},
			MediaKit: MediaKitConfig{APIKey: v.GetString("volc.mediakit.api_key")},
		},
		Storage:         st.flattened,
		StorageChannels: st.channels,
		MVSep: MVSepConfig{
			APIToken: strings.TrimSpace(v.GetString("mvsep.api_token")),
			BaseURL:  strings.TrimSpace(v.GetString("mvsep.base_url")),
		},
		Qianwen: QianwenConfig{APIKey: strings.TrimSpace(v.GetString("qianwen.api_key"))},
	}
}

// StorageChannelPrefix 各存储类型独立配置段的键前缀：storage.providers.<名>.*
// （读取见 storageFromViper，写入见 service.SaveStorage，两处共用此布局）。
const StorageChannelPrefix = "storage.providers."

// storageView 一次解析得到的存储配置双视图：channels 是各类型独立配置（落盘形态），
// flattened 是其中选中类型的拍平生效视图（运行期消费形态）。
type storageView struct {
	flattened StorageConfig
	channels  map[string]StorageConfig
}

// storageFromViper 读存储配置：storage.provider 为启用通道选择器（空=未启用），
// storage.providers.<名>.* 为各类型独立配置段——切换启用类型互不覆盖。
// 旧版平铺字段（storage.endpoint 等）在内存中一次性迁移进对应通道段；旧键保留在盘上
// 不再读取（viper 无删键能力，残留无害），下一次保存起以新段为准。
// providers 段解析失败按缺段处理并走平铺迁移兜底：该段只由本程序写入，形状异常只可能
// 出自手工编辑，不应为此让整个服务启动失败。
func storageFromViper(v *viper.Viper) storageView {
	channels := map[string]StorageConfig{}
	_ = v.UnmarshalKey("storage.providers", &channels)
	if len(channels) == 0 {
		if p := strings.TrimSpace(v.GetString("storage.provider")); p != "" &&
			(strings.TrimSpace(v.GetString("storage.endpoint")) != "" || strings.TrimSpace(v.GetString("storage.bucket")) != "") {
			channels[p] = StorageConfig{
				Endpoint:  strings.TrimSpace(v.GetString("storage.endpoint")),
				Region:    strings.TrimSpace(v.GetString("storage.region")),
				Bucket:    strings.TrimSpace(v.GetString("storage.bucket")),
				AccessKey: strings.TrimSpace(v.GetString("storage.access_key")),
				SecretKey: strings.TrimSpace(v.GetString("storage.secret_key")),
				Prefix:    strings.TrimSpace(v.GetString("storage.prefix")),
			}
		}
	}
	active := strings.TrimSpace(v.GetString("storage.provider"))
	eff := channels[active]
	eff.Provider = active
	return storageView{flattened: eff, channels: channels}
}

// watchDebounce 文件事件防抖：一次保存（尤其编辑器临时文件原子替换）常触发多个事件。
var watchDebounce = 300 * time.Millisecond // pollInterval 事件兜底轮询间隔。darwin 的 fsnotify 走 kqueue 后端（目录轮扫合成事件），
// 会漏掉"临时文件+改名"式原子替换保存（sed -i / vim 等）；stat 轮询保证编辑器无关的
// 最终一致。CLI config set 与 Web 保存是 truncate 直写，事件可靠、轮询只是冗余兜底。
const pollInterval = 2 * time.Second

// Watch 监听配置文件变更，防抖后以重新读盘的结果回调 onChange。供长驻进程（serve）
// 热加载服务外部的变更：另一终端 voxbox config set、手工编辑 config.yaml；
// Web 设置保存本身同步热应用，不依赖此监听。回调在独立 goroutine 触发，须线程安全。
// 注意：可监听的是配置文件（fsnotify + 低频 stat 兜底）；OS 环境变量无变更通知机制，
// 不在可监听范围。返回的 stop 停止事件与轮询两个触发源。
func Watch(onChange func(*Config)) (stop func(), err error) {
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return nil, err
	}
	// 预建空文件：文件不存在时 viper 建立不了监听（记日志后放弃，后续变更全部丢失）
	if f, ferr := os.OpenFile(Path(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); ferr == nil {
		f.Close()
	} else if !os.IsExist(ferr) {
		return nil, ferr
	}

	stopCh := make(chan struct{})
	var stopOnce sync.Once
	var mu sync.Mutex
	var timer *time.Timer
	stopped := false
	schedule := func() {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		if timer != nil {
			timer.Stop()
		}
		// 重载一律重新读盘而非读事件方缓存：rename 式保存会漏事件，viper 的
		// 内部配置可能已过期。撕裂读（写一半）时 Load 报错，短重试兜底。
		timer = time.AfterFunc(watchDebounce, func() {
			for i := 0; i < 3; i++ {
				if cfg, lerr := Load(); lerr == nil {
					onChange(cfg)
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}

	v := newFileViper()
	v.WatchConfig()
	v.OnConfigChange(func(fsnotify.Event) { schedule() })

	last, serr := os.Stat(Path())
	var lastMod time.Time
	var lastSize int64
	if serr == nil {
		lastMod, lastSize = last.ModTime(), last.Size()
	}
	go func() {
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				if fi, ferr := os.Stat(Path()); ferr == nil {
					if fi.ModTime() != lastMod || fi.Size() != lastSize {
						lastMod, lastSize = fi.ModTime(), fi.Size()
						schedule()
					}
				}
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopCh)
			mu.Lock()
			stopped = true
			if timer != nil {
				timer.Stop()
			}
			mu.Unlock()
		})
	}, nil
}

func Set(key, value string) error {
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return err
	}
	v := viper.New()
	v.SetConfigFile(Path())
	v.SetConfigType("yaml")
	_ = v.ReadInConfig()
	v.Set(key, value)
	if err := v.WriteConfigAs(Path()); err != nil {
		return err
	}
	return os.Chmod(Path(), 0o600)
}

func List() ([]KV, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	rows := []KV{
		{"server.port", fmt.Sprint(cfg.Server.Port)},
		{"server.host", cfg.Server.Host},
		{"data_dir", cfg.DataDir},
		{"volc.speech.app_id", mask(cfg.Volc.Speech.AppID)},
		{"volc.speech.access_token", mask(cfg.Volc.Speech.AccessToken)},
		{"volc.speech.api_key", mask(cfg.Volc.Speech.APIKey)},
		{"volc.mediakit.api_key", mask(cfg.Volc.MediaKit.APIKey)},
		{"storage.provider", cfg.Storage.Provider},
		{"mvsep.api_token", mask(cfg.MVSep.APIToken)},
		{"mvsep.base_url", cfg.MVSep.BaseURL},
		{"qianwen.api_key", mask(cfg.Qianwen.APIKey)},
	}
	// 各通道段独立列出（bucket/AK/SK）；键即真实落盘布局，可直接指导 config set。
	for _, name := range slices.Sorted(maps.Keys(cfg.StorageChannels)) {
		ch := cfg.StorageChannels[name]
		rows = append(rows,
			KV{StorageChannelPrefix + name + ".bucket", ch.Bucket},
			KV{StorageChannelPrefix + name + ".access_key", mask(ch.AccessKey)},
			KV{StorageChannelPrefix + name + ".secret_key", mask(ch.SecretKey)},
		)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// mask 打码敏感值：保留首尾各 1 字符，其余以 * 填充；空值原样返回。
func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 2 {
		return strings.Repeat("*", len(s))
	}
	return s[:1] + strings.Repeat("*", len(s)-2) + s[len(s)-1:]
}
