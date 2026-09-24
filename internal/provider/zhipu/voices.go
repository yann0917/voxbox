package zhipu

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// VoiceEntry 音色列表条目（GET /paas/v4/voice/list，运行时拉取：官方音色 + 用户复刻音色）。
type VoiceEntry struct {
	Voice       string `json:"voice"`        // 音色 ID（合成 voice 参数值）
	VoiceName   string `json:"voice_name"`   // 音色名称
	VoiceType   string `json:"voice_type"`   // OFFICIAL=官方 PRIVATE=复刻
	DownloadURL string `json:"download_url"` // 试听音频链接
	CreateTime  string `json:"create_time"`
}

type voiceListResponse struct {
	VoiceList []VoiceEntry `json:"voice_list"`
	apiError
}

// OfficialVoices 官方系统音色（api-reference 文本转语音 voice 枚举，快照 2026-09-25）。
// 列表接口需要凭证；未配置时以此静态表兜底（/api/voices 与 ParamSpecs 枚举共用）。
var OfficialVoices = []VoiceEntry{
	{Voice: "tongtong", VoiceName: "彤彤（默认）", VoiceType: "OFFICIAL"},
	{Voice: "chuichui", VoiceName: "锤锤", VoiceType: "OFFICIAL"},
	{Voice: "xiaochen", VoiceName: "小陈", VoiceType: "OFFICIAL"},
	{Voice: "jam", VoiceName: "动动动物圈 jam", VoiceType: "OFFICIAL"},
	{Voice: "kazi", VoiceName: "动动动物圈 kazi", VoiceType: "OFFICIAL"},
	{Voice: "douji", VoiceName: "动动动物圈 douji", VoiceType: "OFFICIAL"},
	{Voice: "luodo", VoiceName: "动动动物圈 luodo", VoiceType: "OFFICIAL"},
}

// DefaultVoice 默认音色。
const DefaultVoice = "tongtong"

// VoiceClient 智谱音色管理客户端（列表/复刻/删除共用）。
type VoiceClient struct{ apiKey, baseURL string }

func NewVoiceClient(apiKey, baseURL string) *VoiceClient {
	return &VoiceClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// List 拉取音色列表。voiceType 空 = 全部（官方 + 复刻），可传 OFFICIAL / PRIVATE 过滤；
// name 为音色名称模糊搜索（可空）。voiceName 传入中文需 URL encode——http.NewRequestWithString
// 的 URL 解析不做查询参数编码，这里用 url.Values 组装。
func (c *VoiceClient) List(ctx context.Context, voiceType, name string) ([]VoiceEntry, error) {
	q := url.Values{}
	if voiceType != "" {
		q.Set("voiceType", voiceType)
	}
	if name != "" {
		q.Set("voiceName", name)
	}
	url := c.baseURL + pathVoiceList
	if len(q) > 0 {
		url += "?" + q.Encode()
	}
	var resp voiceListResponse
	if err := doJSON(ctx, http.MethodGet, url, c.apiKey, nil, &resp); err != nil {
		return nil, err
	}
	return resp.VoiceList, nil
}
