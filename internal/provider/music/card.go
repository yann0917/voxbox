package music

import (
	"context"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/qingmusic"
)

// ProviderCard 音乐音源卡：唯一配置是音源同步地址；音源配置文件本体在
// dataDir/music/sources.json，由设置页「同步/导入」维护，不在此直接编辑。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:  "music",
		Title: "音乐音源",
		Description: "多平台音乐搜索与下载（qingmusic 链路）：直链解析走内置解析网关（主/备线路），" +
			"音源配置决定可搜平台与档位，经设置页同步或导入；下载入库后可送人声分离、切高潮。",
		Kind:  provider.KindCloud,
		Order: 95,
		Fields: []provider.CredentialField{
			{Key: "sources_url", Label: "音源同步地址", Kind: provider.FieldText, ConfigKey: "music.sources_url",
				Placeholder: "music.json 的拉取 URL，留空则只能手动导入", Hint: "音源列表决定可搜平台与档位"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{"sources_url": cfg.Music.SourcesURL}
		},
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["sources_url"]; ok {
				nc.Music.SourcesURL = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.Music = src.Music },
		Test: func(cfg *config.Config) (string, bool) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			h, err := qingmusic.NewGateway().Health(ctx)
			if err != nil {
				return "解析网关不可达: " + err.Error(), false
			}
			if status, ok := h["status"].(string); ok && status != "" {
				return "解析网关正常（" + status + "）", true
			}
			return "解析网关可达", true
		},
	}
}
