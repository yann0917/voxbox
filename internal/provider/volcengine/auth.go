// Package volcengine 封装火山引擎各服务的客户端与工具实现。
package volcengine

import (
	"errors"
	"fmt"
)

var (
	ErrNoCred = errors.New("凭证未配置")
	ErrAuth   = errors.New("凭证无效")
	// ErrNotGranted 凭证有效但目标资源未开通（如机器翻译缺 volc.speech.mt）。
	// 属「需去控制台开通」的配置类问题，与任务失败区分，避免上游重试。
	ErrNotGranted = errors.New("服务未开通")
)

// SpeechCred 语音三件套（TTS/ASR/播客）共用凭证：新版 API Key 或旧版 AppID+AccessToken。
type SpeechCred struct {
	AppID       string
	AccessToken string
	APIKey      string
}

func (c SpeechCred) Validate() error {
	if c.APIKey != "" {
		return nil
	}
	if c.AppID != "" && c.AccessToken != "" {
		return nil
	}
	return fmt.Errorf("%w: 请先执行 voxbox config set volc.speech.app_id <APP ID> 与 volc.speech.access_token <Token>（或设置 volc.speech.api_key）", ErrNoCred)
}

// ValidatePodcast 播客专用校验：播客 WS 协议只认 APP ID + Access Token
// （X-Api-App-Id / X-Api-Access-Key），新版 X-Api-Key 单键鉴权不适用，
// 仅配 API Key 时必须在此拦下而非等握手失败。
func (c SpeechCred) ValidatePodcast() error {
	if c.AppID != "" && c.AccessToken != "" {
		return nil
	}
	return fmt.Errorf("%w: 播客需要 APP ID 与 Access Token（新版 API Key 不适用于播客协议），请执行 voxbox config set volc.speech.app_id <APP ID> 与 volc.speech.access_token <Token>", ErrNoCred)
}
