package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// ProviderCard 外部下载器卡：yt-dlp 与 douyin-downloader 的本地接入配置。
// 两个都是外部工具：缺一个不影响另一个使用。
func ProviderCard() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:        "downloader",
		Title:       "外部下载器",
		Description: "链接下载的执行端：yt-dlp 覆盖主流视频站（留空按 PATH 查找）；抖音链接走 douyin-downloader（外部 Python 项目，需填启动命令与其 config.yml 路径，cookie 由该配置自管）。",
		Kind:        provider.KindCloud,
		Order:       96,
		Fields: []provider.CredentialField{
			{Key: "ytdlp_path", Label: "yt-dlp 路径", Kind: provider.FieldText, ConfigKey: "downloader.ytdlp_path",
				Placeholder: "留空按 PATH 查找，或填完整路径"},
			{Key: "douyin_command", Label: "抖音下载器启动命令", Kind: provider.FieldText, ConfigKey: "downloader.douyin_command",
				Placeholder: "如 python3 /path/to/douyin-downloader/run.py"},
			{Key: "douyin_config", Label: "抖音下载器配置文件", Kind: provider.FieldText, ConfigKey: "downloader.douyin_config",
				Placeholder: "其 config.yml 绝对路径（cookie 在里面自管）"},
		},
		Values: func(cfg *config.Config) map[string]string {
			return map[string]string{
				"ytdlp_path":     cfg.Downloader.YtdlpPath,
				"douyin_command": cfg.Downloader.DouyinCommand,
				"douyin_config":  cfg.Downloader.DouyinConfig,
			}
		},
		Apply: func(nc *config.Config, fields map[string]string) {
			if v, ok := fields["ytdlp_path"]; ok {
				nc.Downloader.YtdlpPath = v
			}
			if v, ok := fields["douyin_command"]; ok {
				nc.Downloader.DouyinCommand = v
			}
			if v, ok := fields["douyin_config"]; ok {
				nc.Downloader.DouyinConfig = v
			}
		},
		ReRegister: ReRegisterAll,
		Sync:       func(dst, src *config.Config) { dst.Downloader = src.Downloader },
		Test: func(cfg *config.Config) (string, bool) {
			var parts []string
			ok := true
			ytdlp := strings.TrimSpace(cfg.Downloader.YtdlpPath)
			if ytdlp == "" {
				if _, err := exec.LookPath("yt-dlp"); err != nil {
					parts = append(parts, "yt-dlp 未找到（pip install yt-dlp 或填路径）")
					ok = false
				} else {
					parts = append(parts, "yt-dlp 就绪（PATH）")
				}
			} else if _, err := os.Stat(ytdlp); err != nil {
				parts = append(parts, "yt-dlp 路径不可用")
				ok = false
			} else {
				parts = append(parts, "yt-dlp 路径就绪")
			}
			if strings.TrimSpace(cfg.Downloader.DouyinCommand) == "" {
				parts = append(parts, "douyin-downloader 未配置（可选）")
			} else if strings.TrimSpace(cfg.Downloader.DouyinConfig) == "" {
				parts = append(parts, "douyin-downloader 缺 config.yml 路径")
				ok = false
			} else if _, err := os.Stat(cfg.Downloader.DouyinConfig); err != nil {
				parts = append(parts, "douyin-downloader config.yml 不可读")
				ok = false
			} else {
				parts = append(parts, "douyin-downloader 就绪")
			}
			return strings.Join(parts, "；"), ok
		},
	}
}

// probeTimeout 解析元数据的兜底时限：yt-dlp -J 对冷门站可能要解析全量格式。
const probeTimeout = 20 * time.Second

// Probe 解析链接元数据（标题/时长/合集条数/字幕语言），素材页链接输入的预览数据源。
// 抖音链接不走 yt-dlp 解析，返回 backend=douyin 交前端按抖音流程处理。
func Probe(ctx context.Context, cfg *config.Config, raw string) (map[string]any, error) {
	if isDouyinURL(raw) {
		return map[string]any{"backend": "douyin", "probe": "unsupported"}, nil
	}
	bin, err := (&Tool{ytdlpPath: cfg.Downloader.YtdlpPath}).resolveYtdlp()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-J", "--flat-playlist", "--no-warnings", raw).Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("解析超时（20s）：站点可能不支持或需要登录")
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("解析失败: %s", firstMeaningful(string(ee.Stderr), 300))
		}
		return nil, fmt.Errorf("解析失败: %v", err)
	}
	var meta struct {
		Title        string                     `json:"title"`
		Duration     float64                    `json:"duration"`
		Uploader     string                     `json:"uploader"`
		Extractor    string                     `json:"extractor_key"`
		Subtitles    map[string]json.RawMessage `json:"subtitles"`
		AutoCaptions map[string]json.RawMessage `json:"automatic_captions"`
		Entries      []struct {
			Title string `json:"title"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &meta); err != nil {
		return nil, fmt.Errorf("解析结果不可读: %w", err)
	}
	langs := map[string]bool{}
	for lang := range meta.Subtitles {
		langs[lang] = true
	}
	for lang := range meta.AutoCaptions {
		langs[lang] = true
	}
	res := map[string]any{
		"backend":   "ytdlp",
		"title":     meta.Title,
		"duration":  int64(meta.Duration),
		"uploader":  meta.Uploader,
		"site":      meta.Extractor,
		"playlist":  len(meta.Entries) > 0,
		"n_entries": len(meta.Entries),
	}
	if len(meta.Entries) > 0 && meta.Title == "" {
		res["title"] = meta.Entries[0].Title
	}
	subLangs := make([]string, 0, len(langs))
	for lang := range langs {
		subLangs = append(subLangs, lang)
	}
	res["subtitle_langs"] = subLangs
	return res, nil
}
