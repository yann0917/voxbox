package volcengine

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/yann0917/voxbox/internal/config"
)

// ProbeSpeech 火山语音连通性检测：构造 TTS 客户端发 1 字合成请求（volcengine 卡 Test）。
// 注意：真实调用会消耗少量合成配额，可接受。
func ProbeSpeech(cfg *config.Config) (string, bool) {
	cred := SpeechCred{
		AppID: cfg.Volc.Speech.AppID, AccessToken: cfg.Volc.Speech.AccessToken, APIKey: cfg.Volc.Speech.APIKey,
	}
	if err := cred.Validate(); err != nil {
		return err.Error(), false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := NewTTSClient(cred)
	_, err := client.Synthesize(ctx, TTSSynthesizeReq{Text: "测", VoiceType: "zh_female_cancan_mars_bigtts", Format: "mp3"})
	if err != nil {
		return err.Error(), false
	}
	return "连接成功", true
}

// ProbeMediaKit MediaKit 连通性探测（与人声分离工具同域、同 Bearer 鉴权头，mediakit 卡 Test）：
// GET 一个必然不存在的任务 ID——404/400 表示鉴权通过（任务不存在属预期）→ 连接成功；
// 401/403 → 凭证无效；网络错误透传错误信息。未配置 apiKey 时直接报未配置，不发起请求。
func ProbeMediaKit(cfg *config.Config) (string, bool) {
	if cfg.Volc.MediaKit.APIKey == "" {
		return "未配置 AI MediaKit API Key：请执行 voxbox config set volc.mediakit.api_key 或在 Web 设置页配置", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		MediaKitBaseURL+fmt.Sprintf(MediaKitQueryPathFmt, "nonexistent-connectivity-probe"), nil)
	if err != nil {
		return err.Error(), false
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Volc.MediaKit.APIKey)
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
