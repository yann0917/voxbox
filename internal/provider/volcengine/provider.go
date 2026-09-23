package volcengine

import (
	"errors"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 将火山引擎的全部工具注册进 registry（启动路径，同 key 重复注册报错）。
// 凭证缺失时 TTS/ASR/播客/翻译/妙记仍注册（Run 时再报凭证错误）；分离工具用 MediaKit apiKey
// （与语音三件套凭证体系独立），同样缺失仍注册（Run 时再报错）。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	var errs []error
	for _, t := range allTools(cfg, dataDir) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReRegisterAll 以最新凭证覆盖重注册全部工具（Web 设置保存后热加载，无需重启服务）。
// 工具集与启动时一致，Replace 不会失败；进行中的任务持有旧实例不受影响。
func ReRegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) {
	for _, t := range allTools(cfg, dataDir) {
		reg.Replace(t)
	}
}

func allTools(cfg config.Config, dataDir string) []provider.Tool {
	cred := SpeechCred{
		AppID:       cfg.Volc.Speech.AppID,
		AccessToken: cfg.Volc.Speech.AccessToken,
		APIKey:      cfg.Volc.Speech.APIKey,
	}
	return []provider.Tool{
		NewTTSTool(cred, dataDir),
		NewTTSLongTool(cred, dataDir),
		NewTTSStreamTool(cred, dataDir),
		NewASRTool(cred, dataDir),
		NewPodcastTool(cred, dataDir),
		NewTranslateTool(cred, dataDir),
		NewMinutesTool(cred, dataDir),
		NewSeparateTool(cfg.Volc.MediaKit.APIKey, dataDir),
	}
}
