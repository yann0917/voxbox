package qingmusic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ---------------------------------------------------------------- 常量

// 服务线路：内置在 App 里，用户改不了（对应 bundle 里 #8586 的 SERVICE_LINES）。
//
// 注意「服务线路」与「音源」是两层不同的东西：
//
//	服务线路 = 解析网关的 baseUrl（primary / backup），决定「问谁要播放地址」
//	音源     = 平台（kw/kg/wy/tx/mg/bili）+ 该平台允许的音质档，决定「要哪个平台的资源」
//
// 两者的配置来源也完全不同：线路写死在 bundle 里，音源由用户导入（见 sourceconfig.go）。
var ServiceLines = []ServiceLine{
	{
		ID: "primary", Name: "主线路", BaseURL: "https://musicserver.haitangw.cc/v1",
		Services: map[string]LineService{
			ServiceAppAPI: {HealthPath: MusicHealthPath, HealthProfile: ProfileMusicGateway},
			ServiceAuth:   {HealthPath: AuthHealthPath, HealthProfile: ProfileAccount},
		},
	},
	{
		ID: "backup", Name: "备用线路", BaseURL: "https://qt.haitangw.net/v1",
		Services: map[string]LineService{
			ServiceAppAPI: {HealthPath: MusicHealthPath, HealthProfile: ProfileMusicGateway},
			ServiceAuth:   {HealthPath: AuthHealthPath, HealthProfile: ProfileAccount},
		},
	},
}

// 健康检查路径与解析路径。auth 与 music 共用同一个 baseUrl 主机，但路径不同。
const (
	MusicHealthPath = "/music/health"
	AuthHealthPath  = "/health"
	ResolvePath     = "/music/resolve-url"
)

// 线路提供的两个子服务（App 里 line.services 的 key）。
const (
	ServiceAppAPI = "appApi" // 音乐解析网关
	ServiceAuth   = "auth"   // 账号后端
)

// 健康探针类型（App 里 healthProfile 的取值，决定怎么判「这条线路活着」）。
const (
	ProfileMusicGateway = "musicGateway"
	ProfileAccount      = "account"
)

// LineService 线路下某个子服务的接入信息。
type LineService struct {
	HealthPath    string `json:"healthPath"`
	HealthProfile string `json:"healthProfile"`
}

// ServiceLine 一条服务线路（对应 bundle 里 SERVICE_LINES 的一个元素）。
type ServiceLine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`

	// Services 通常是 {appApi:{...}, auth:{...}}。
	// 为空时按 MusicHealthPath / AuthHealthPath 兜底。
	Services map[string]LineService `json:"services,omitempty"`
}

// HealthPath 取子服务的健康检查路径，缺省时回退到内置路径。
func (l ServiceLine) HealthPath(service string) string {
	if s, ok := l.Services[service]; ok && s.HealthPath != "" {
		return s.HealthPath
	}
	if service == ServiceAuth {
		return AuthHealthPath
	}
	return MusicHealthPath
}

// ServiceLineByID 按 id 找内置线路。
func ServiceLineByID(id string) (ServiceLine, bool) {
	for _, l := range ServiceLines {
		if l.ID == id {
			return l, true
		}
	}
	return ServiceLine{}, false
}

// 音质档位。取值来源是 bundle 里 #12879（档位 → 显示标签/码率表，
// 也就是「音质选择」弹窗里那 9 行），这里补上 bili192 —— B 站专用档，
// 标签「约192kbps · M4A」，前面几档是 standard/320kbps/FLAC/FLAC/Master/Spatial/Surround。
const (
	LevelStandard  = "standard"
	LevelExhigh    = "exhigh"
	LevelLossless  = "lossless"
	LevelHires     = "hires"
	LevelAtmos     = "atmos"
	LevelAtmosPlus = "atmos_plus"
	LevelMaster    = "master"
	LevelSky       = "sky"
	LevelJYEffect  = "jyeffect"
	LevelJYMaster  = "jymaster"
	LevelClear     = "clear"
	LevelBili192   = "bili192"
)

// LevelLabel 档位的展示标签 + 码率，复刻 #12879 的返回结果。
// 未知档位返回 {Label: 档位名, Bitrate: ""}（App 的行为）。
//
//	standard  128kbps      exhigh   320kbps     lossless FLAC
//	hires     FLAC         jymaster Master      sky      Spatial
//	jyeffect  Surround     bili192  约192kbps · M4A
func LevelLabel(level string) (label, bitrate string) {
	switch level {
	case LevelStandard:
		return "标准音质", "128kbps"
	case LevelExhigh:
		return "极高音质", "320kbps"
	case LevelLossless:
		return "无损音质", "FLAC"
	case LevelHires:
		return "Hi-Res", "FLAC"
	case LevelJYMaster:
		return "臻品母带", "Master"
	case LevelSky:
		return "空间音频", "Spatial"
	case LevelJYEffect:
		return "环绕声", "Surround"
	case LevelBili192:
		return "B站音质", "约192kbps · M4A"
	case LevelAtmos:
		return "全景声", "Atmos"
	case LevelAtmosPlus:
		return "全景声+", "Atmos"
	case LevelMaster:
		return "母带", "Master"
	case LevelClear:
		return "清澈人声", ""
	}
	return level, ""
}

// levelRank 音质高低排序（数值越大越好）。用于「首选档位不被该音源支持时
// 退到哪一档」以及「生成降级链」—— 复刻 App 的 clampLevel 语义。
// 表中没有的档位按 0 处理（排在 standard 之下），这样未知档位永远是最后才试。
var levelRank = map[string]int{
	LevelStandard:  10,
	LevelClear:     15,
	LevelExhigh:    20,
	LevelBili192:   20,
	LevelLossless:  30,
	LevelHires:     40,
	LevelSky:       45,
	LevelJYEffect:  46,
	LevelMaster:    50,
	LevelJYMaster:  50,
	LevelAtmos:     55,
	LevelAtmosPlus: 56,
}

// LevelRankOf 取档位权重，未知档位为 0。
func LevelRankOf(level string) int { return levelRank[level] }

// 音源标识（对应 music.json 的 lines[].id，也是 resolve-url 的 source 字段）。
const (
	SourceKw   = "kw"   // 酷我
	SourceKg   = "kg"   // 酷狗
	SourceWy   = "wy"   // 网易云
	SourceTx   = "tx"   // QQ 音乐
	SourceMg   = "mg"   // 咪咕
	SourceBili = "bili" // B 站
)

// ---------------------------------------------------------------- 数据模型

// Track 归一化后的歌曲信息。Rid 是各平台的歌曲标识，直接喂给 ResolveURL。
type Track struct {
	Source   string `json:"source"`
	Rid      string `json:"rid"`
	Name     string `json:"name"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Duration int64  `json:"duration"` // 毫秒
	Pic      string `json:"pic,omitempty"`
}

// Resolved 是 /music/resolve-url 返回的播放信息。
type Resolved struct {
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers,omitempty"`
	PlaybackHeaders map[string]string `json:"playbackHeaders,omitempty"`
	ExpiresAt       int64             `json:"expiresAt,omitempty"` // 毫秒时间戳
	TraceID         string            `json:"traceId,omitempty"`
}

// ------------------------------------------------ resolve-url 线上协议结构

type resolveRequest struct {
	Source string `json:"source"`
	Rid    string `json:"rid"`
	Level  string `json:"level"`
}

type resolveResponse struct {
	Code *json.RawMessage `json:"code"`
	Data *struct {
		URL             string            `json:"url"`
		Headers         map[string]string `json:"headers"`
		PlaybackHeaders map[string]string `json:"playbackHeaders"`
		ExpiresAt       json.Number       `json:"expiresAt"`
	} `json:"data"`
	TraceID string `json:"traceId"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ---------------------------------------------------------------- 错误

// GatewayError 语义与 App 内定义保持一致，便于对拍日志。
type GatewayError struct {
	Code       string // GATEWAY_TIMEOUT / NETWORK_UNREACHABLE / GATEWAY_REQUEST_FAILED / INVALID_GATEWAY_RESPONSE
	Message    string
	TraceID    string
	HTTPStatus int
}

func (e *GatewayError) Error() string {
	if e.TraceID != "" {
		return fmt.Sprintf("qingmusic: %s (code=%s, http=%d, trace=%s)", e.Message, e.Code, e.HTTPStatus, e.TraceID)
	}
	return fmt.Sprintf("qingmusic: %s (code=%s, http=%d)", e.Message, e.Code, e.HTTPStatus)
}

// RemoteApiError 对应 JS 侧的 RemoteApiError(status, code, message)。
type RemoteApiError struct {
	Status  int
	Code    string
	Message string
}

func (e *RemoteApiError) Error() string {
	return fmt.Sprintf("qingmusic/remote: %s (status=%d, code=%s)", e.Message, e.Status, e.Code)
}

// ---------------------------------------------------------------- HTTP 客户端

// NewHTTPClient 与 App 侧的超时保持一致（网关 15s）。
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout}
}
