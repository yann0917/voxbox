package minimax

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Voice 音色条目：静态表来自官方系统音色列表快照（faq/system-voice-id，2026-10-05，
// 全量 327 个）；运行时列表来自 /v1/get_voice（额外含复刻音色与文生音色）。
// Label 为显示名（主显官方名、无名回落 ID，语种附注），静态与运行时同构。
type Voice struct {
	ID    string `json:"id"`             // 音色 ID（合成 voice_id 参数值）
	Name  string `json:"name"`           // 官方音色名（复刻/文生为描述，可为空）
	Lang  string `json:"lang,omitempty"` // 语种（静态表快照字段；运行时接口不返回）
	Label string `json:"label"`          // 下拉显示名
}

//go:embed voices.json
var systemVoicesJSON []byte

// systemVoices 官方系统音色静态表（配置凭证前的兜底数据源）。
var systemVoices = func() []Voice {
	var vs []Voice
	if err := json.Unmarshal(systemVoicesJSON, &vs); err != nil {
		panic(fmt.Sprintf("minimax: 内置音色表损坏: %v", err))
	}
	for i := range vs {
		vs[i].Label = voiceLabel(vs[i].Name, vs[i].ID, vs[i].Lang)
	}
	return vs
}()

// DefaultVoice 默认音色（官方文档示例同款，青涩青年音色）。
const DefaultVoice = "male-qn-qingse"

// Voices 返回官方系统音色静态表。
func Voices() []Voice { return systemVoices }

// voiceLabel 显示名：主显官方音色名（无名的复刻/文生音色主显 ID），
// 静态表带语种时附注（运行时接口无语种字段则不加）。
func voiceLabel(name, id, lang string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = id
	}
	if lang != "" && lang != "中文" {
		label += " · " + lang
	}
	return label
}

// VoiceClient MiniMax 音色管理客户端（查询可用音色）。
type VoiceClient struct{ apiKey, baseURL string }

func NewVoiceClient(apiKey, baseURL string) *VoiceClient {
	return &VoiceClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// getVoiceResp /v1/get_voice 响应：系统音色带官方名，复刻/文生音色仅 ID 与描述。
type getVoiceResp struct {
	SystemVoice []struct {
		VoiceID     string   `json:"voice_id"`
		VoiceName   string   `json:"voice_name"`
		Description []string `json:"description"`
	} `json:"system_voice"`
	VoiceCloning []struct {
		VoiceID     string   `json:"voice_id"`
		Description []string `json:"description"`
	} `json:"voice_cloning"`
	VoiceGeneration []struct {
		VoiceID     string   `json:"voice_id"`
		Description []string `json:"description"`
	} `json:"voice_generation"`
	BaseResp baseResp `json:"base_resp"`
}

// List 拉取当前账号可用音色：系统 + 复刻 + 文生（合成时实际可用的全部音色）。
// 返回形态与静态表同构（前端 /api/voices 契约一致）。
func (c *VoiceClient) List(ctx context.Context) ([]Voice, error) {
	var resp getVoiceResp
	if err := doJSON(ctx, pollClient, "POST", c.baseURL+pathGetVoice, c.apiKey, map[string]string{"voice_type": "all"}, &resp); err != nil {
		return nil, err
	}
	voices := make([]Voice, 0,
		len(resp.SystemVoice)+len(resp.VoiceCloning)+len(resp.VoiceGeneration))
	for _, v := range resp.SystemVoice {
		name := firstNonEmpty(v.VoiceName, firstDesc(v.Description))
		voices = append(voices, Voice{ID: v.VoiceID, Name: name, Label: voiceLabel(name, v.VoiceID, "")})
	}
	for _, v := range resp.VoiceCloning {
		name := firstDesc(v.Description)
		voices = append(voices, Voice{ID: v.VoiceID, Name: name, Label: voiceLabel(name, v.VoiceID, "")})
	}
	for _, v := range resp.VoiceGeneration {
		name := firstDesc(v.Description)
		voices = append(voices, Voice{ID: v.VoiceID, Name: name, Label: voiceLabel(name, v.VoiceID, "")})
	}
	if len(voices) == 0 {
		return nil, fmt.Errorf("MiniMax 音色列表为空")
	}
	return voices, nil
}

// firstDesc 复刻/文生音色的显示名：取描述首条，空描述回落 ID。
func firstDesc(desc []string) string {
	for _, d := range desc {
		if d = strings.TrimSpace(d); d != "" {
			return d
		}
	}
	return ""
}

// firstNonEmpty 取第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
