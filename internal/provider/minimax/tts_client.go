package minimax

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// TTS 模型（speech-2.8 为当前计费代际）：hd 高清 / turbo 提速降本，同步异步同价。
const (
	ModelTTSHD    = "speech-2.8-hd"
	ModelTTSTurbo = "speech-2.8-turbo"
)

// TTSReq 同步合成请求。wire 结构（api-reference/speech-t2a-http，2026-10-05 校准）：
// POST /v1/t2a_v2，成功响应为 JSON——data.audio 是 **hex 编码**的音频字节（非 base64、
// 非二进制体）；业务错误回 HTTP 200 + base_resp.status_code≠0。
// format 固定 wav（可播、纯 Go 可拼接）；sample_rate 固定 32000、单声道（上游默认）。
type TTSReq struct {
	Text          string  // <10000 字符，官方建议 >3000 用流式——voxbox 以分段规避
	Voice         string  // 音色 ID（系统/复刻/文生）
	Model         string  // speech-2.8-hd | speech-2.8-turbo，空回落 hd
	Speed         float64 // [0.5,2]，0 = 上游默认 1.0
	Volume        float64 // (0,10]，0 = 上游默认 1.0
	Pitch         int     // [-12,12]，0 = 上游默认 0
	Emotion       string  // happy/sad/…，空 = 模型自动匹配
	LanguageBoost string  // auto/Chinese/Chinese,Yue/…，空 = 不传（粤语系音色需 Chinese,Yue）
	SoundEffects  string  // voice_modify.sound_effects：spacious_echo/auditorium_echo/lofi_telephone/robotic，空 = 不传
}

// t2aResp 同步合成响应：data.audio 为 hex 编码音频，status 2 = 合成结束。
type t2aResp struct {
	Data struct {
		Audio  string `json:"audio"`
		Status int    `json:"status"`
	} `json:"data"`
	BaseResp baseResp `json:"base_resp"`
}

// Synthesize 合成并返回音频字节（wav）。
func (c *TTSClient) Synthesize(ctx context.Context, req TTSReq) ([]byte, error) {
	if strings.TrimSpace(req.Voice) == "" {
		return nil, fmt.Errorf("MiniMax TTS 缺少必填参数: voice")
	}
	model := req.Model
	if model == "" {
		model = ModelTTSHD
	}
	voiceSetting := map[string]any{"voice_id": req.Voice}
	if req.Speed > 0 {
		voiceSetting["speed"] = req.Speed
	}
	if req.Volume > 0 {
		voiceSetting["vol"] = req.Volume
	}
	if req.Pitch != 0 {
		voiceSetting["pitch"] = req.Pitch
	}
	if req.Emotion != "" {
		voiceSetting["emotion"] = req.Emotion
	}
	body := map[string]any{
		"model":         model,
		"text":          req.Text,
		"stream":        false,
		"voice_setting": voiceSetting,
		"audio_setting": map[string]any{
			"sample_rate": 32000,
			"format":      "wav",
			"channel":     1,
		},
	}
	if req.LanguageBoost != "" {
		body["language_boost"] = req.LanguageBoost
	}
	// voice_modify 仅支持 mp3/wav/flac（voxbox 固定 wav）；单次仅能选一种特效
	if req.SoundEffects != "" {
		body["voice_modify"] = map[string]any{"sound_effects": req.SoundEffects}
	}
	raw, err := doBytes(ctx, c.baseURL+pathT2A, c.apiKey, body)
	if err != nil {
		return nil, err
	}
	var resp t2aResp
	if err := decodeT2AResp(raw, &resp); err != nil {
		return nil, err
	}
	audio, err := hex.DecodeString(resp.Data.Audio)
	if err != nil {
		return nil, fmt.Errorf("MiniMax TTS 音频数据解码失败: %w", err)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("MiniMax TTS 响应中未包含音频数据")
	}
	return audio, nil
}

// decodeT2AResp 解析合成响应：业务错误（base_resp.status_code≠0，HTTP 仍 200）在此转译。
func decodeT2AResp(raw []byte, out *t2aResp) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析 MiniMax TTS 响应失败: %w", err)
	}
	if msg := out.BaseResp.errText(); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// TTSClient MiniMax 同步 TTS 客户端。
type TTSClient struct{ apiKey, baseURL string }

func NewTTSClient(apiKey, baseURL string) *TTSClient {
	return &TTSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}
