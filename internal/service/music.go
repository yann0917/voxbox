// 音乐与链接下载的服务层：qingmusic 搜索/音源/歌词/直链解析与 yt-dlp 链接预览。
// HTTP 面在 internal/modules/music 与 internal/modules/downloader，此处只收业务。
package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/provider/downloader"
	"github.com/yann0917/voxbox/internal/provider/music"
	"github.com/yann0917/voxbox/internal/qingmusic"
)

// musicSearchTimeout 单次搜索的整体时限（各源内部另有独立重试与超时）。
const musicSearchTimeout = 20 * time.Second

// MusicSources 当前音源配置的只读视图（线路 / 启用态 / 支持档位 / 搜索实现名）。
// 未导入音源配置时返回空切片（前端展示「去设置页同步或导入」引导）。
func (s *Service) MusicSources() []qingmusic.SourceLine {
	cfg, err := music.LoadSources(s.dataDir)
	if err != nil {
		return nil
	}
	return cfg.Lines
}

// MusicConfigured 音源配置是否已导入。
func (s *Service) MusicConfigured() bool {
	_, err := os.Stat(music.SourcesFile(s.dataDir))
	return err == nil
}

// MusicSearch 单音源搜索。page 从 1 起。
func (s *Service) MusicSearch(ctx context.Context, source, keyword string, page int) ([]qingmusic.Track, error) {
	if keyword == "" {
		return nil, fmt.Errorf("搜索词不能为空")
	}
	if page < 1 {
		page = 1
	}
	if _, err := music.LoadSources(s.dataDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, musicSearchTimeout)
	defer cancel()
	return qingmusic.NewSearcher().Search(ctx, source, keyword, page)
}

// sanitizeSourceConfig 剥掉音源配置头的 JS 注释 banner（社区配置普遍带
// `/** @name … */` 头与 // 行注释，非合法 JSON）：_PARSE 前净化，落盘文件即纯 JSON。
func sanitizeSourceConfig(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	for strings.HasPrefix(s, "/*") {
		if i := strings.Index(s, "*/"); i >= 0 {
			s = strings.TrimSpace(s[i+2:])
		} else {
			break
		}
	}
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		kept = append(kept, l)
	}
	return []byte(strings.Join(kept, "\n"))
}

// MusicSyncSources 从配置的同步地址拉取音源配置落盘（设置页「同步音源」）。
// 返回导入的音源数。
func (s *Service) MusicSyncSources(ctx context.Context) (int, error) {
	url := s.Config().Music.SourcesURL
	if url == "" {
		return 0, fmt.Errorf("未配置音源同步地址：设置页「音乐音源」填写 music.json 的拉取 URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("同步地址不合法: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("拉取音源配置失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("拉取音源配置失败：HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, fmt.Errorf("读取音源配置失败: %w", err)
	}
	return s.MusicImportSources(raw)
}

// MusicImportSources 校验并导入一份音源配置（music.json），成功即落盘生效。
func (s *Service) MusicImportSources(raw []byte) (int, error) {
	clean := sanitizeSourceConfig(raw)
	parsed, err := qingmusic.ParseSourceConfig(clean)
	if err != nil {
		return 0, fmt.Errorf("音源配置解析失败: %w", err)
	}
	if len(parsed.Lines) == 0 {
		return 0, fmt.Errorf("音源配置里没有任何线路")
	}
	path := music.SourcesFile(s.dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, clean, 0o600); err != nil {
		return 0, fmt.Errorf("音源配置写入失败: %w", err)
	}
	return len(parsed.Lines), nil
}

// MusicLyric 取歌词（lrc 文本）。不支持或拿不到时返回错误（调用方按可选处理）。
func (s *Service) MusicLyric(ctx context.Context, source, rid, name, artist string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return qingmusic.NewSearcher().Lyric(ctx, source, rid, name, artist, 0)
}

// MusicResolve 解析试听直链：按音源配置定档（默认 standard），返回带请求头的直链，
// 由模块端点代理转发（直链有时效与防盗链头，浏览器直连不可靠）。
func (s *Service) MusicResolve(ctx context.Context, source, rid, level string) (*qingmusic.Resolved, error) {
	sources, err := music.LoadSources(s.dataDir)
	if err != nil {
		return nil, err
	}
	if lv, ok := sources.ResolveLevel(source, level); ok {
		level = lv
	} else if level == "" {
		level = qingmusic.LevelStandard
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return qingmusic.NewGateway().ResolveURL(ctx, source, rid, level)
}

// DownloaderProbe 链接元数据预览（yt-dlp -J；抖音链接标记交前端按抖音流程走）。
func (s *Service) DownloaderProbe(ctx context.Context, raw string) (map[string]any, error) {
	if raw == "" {
		return nil, fmt.Errorf("链接不能为空")
	}
	return downloader.Probe(ctx, s.Config(), raw)
}
