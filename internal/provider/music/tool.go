// Package music 音乐下载工具：qingmusic 移植链路的任务化封装。
// 搜索/试听/音源管理是模块同步端点（internal/modules/music），只有「下载落盘」
// 是长任务——复用任务系统拿进度广播、历史与产物深链。
package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/qingmusic"
)

// SourcesFile 音源配置在数据目录内的落点。设置页同步/导入后写这里，
// 工具每次 Run 现读——音源配置变更不需要热重注册。
func SourcesFile(dataDir string) string {
	return filepath.Join(dataDir, "music", "sources.json")
}

// LoadSources 读音源配置；未导入时给出可操作的指引文案。
func LoadSources(dataDir string) (*qingmusic.SourceConfig, error) {
	cfg, err := qingmusic.LoadSourceConfig(SourcesFile(dataDir))
	if errors.Is(err, qingmusic.ErrNoSourceConfig) {
		return nil, fmt.Errorf("尚未导入音源配置：到设置页同步音源或导入 music.json")
	}
	if err != nil {
		return nil, fmt.Errorf("音源配置不可用: %w", err)
	}
	return cfg, nil
}

// MusicTool 音乐下载：搜索结果中的单曲按档位降级链解析直链、落盘、写标签。
type MusicTool struct {
	dataDir string
}

func NewTool(dataDir string) *MusicTool { return &MusicTool{dataDir: dataDir} }

func (t *MusicTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "music",
		Name:     "music_download",
		Title:    "音乐下载",
		Description: "按音源配置解析歌曲直链下载入库（默认 standard 128kbps，自动按档位降级），" +
			"写入 ID3/FLAC 标签与封面，可选歌词 lrc",
		Group: "语音",
	}
}

func (t *MusicTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "source", Label: "音源", Type: provider.ParamString, Required: true,
			Placeholder: "音源 ID，如 kw / kg / wy / tx / mg", Group: "输入"},
		{Key: "rid", Label: "曲目 ID", Type: provider.ParamString, Required: true, Group: "输入"},
		{Key: "name", Label: "歌名", Type: provider.ParamString, Required: true, Group: "输入"},
		{Key: "artist", Label: "歌手", Type: provider.ParamString, Group: "输入",
			Placeholder: "用于命名与歌词匹配"},
		{Key: "duration_ms", Label: "时长（毫秒）", Type: provider.ParamInt, Group: "输入",
			Placeholder: "搜索结果带出，用于播放器展示"},
		{Key: "level", Label: "音质", Type: provider.ParamString, Group: "参数",
			Placeholder: "留空走默认 standard；如 exhigh / lossless / hires"},
		{Key: "lyrics", Label: "下载歌词 lrc", Type: provider.ParamBool, Default: true, Group: "参数"},
		{Key: "tag", Label: "写入标签与封面", Type: provider.ParamBool, Default: true, Group: "参数"},
	}
}

func (t *MusicTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	source := strings.TrimSpace(strParam(in.Params, "source"))
	rid := strings.TrimSpace(strParam(in.Params, "rid"))
	name := strings.TrimSpace(strParam(in.Params, "name"))
	artist := strings.TrimSpace(strParam(in.Params, "artist"))
	level := strings.TrimSpace(strParam(in.Params, "level"))
	wantLyrics := boolParam(in.Params, "lyrics", true)
	wantTag := boolParam(in.Params, "tag", true)
	durationMS := int64Param(in.Params, "duration_ms")

	if source == "" || rid == "" || name == "" {
		return provider.TaskOutput{}, fmt.Errorf("音源 / 曲目 ID / 歌名 均必填")
	}

	sources, err := LoadSources(t.dataDir)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	line, ok := sources.Line(source)
	if !ok || !line.Enabled {
		return provider.TaskOutput{}, fmt.Errorf("音源 %q 不存在或未启用（可在设置页检查音源配置）", source)
	}

	track := qingmusic.Track{
		Source: source, Rid: rid, Name: name, Artist: artist,
		Duration: durationMS,
	}

	// 歌词先行：失败不致命（lrc 只是附带），且直链解析耗时在此之前暴露更友好。
	lyricText := ""
	if wantLyrics {
		report(2, "获取歌词…", nil)
		if lrc, err := qingmusic.NewSearcher().Lyric(ctx, source, rid, name, artist, durationMS); err == nil {
			lyricText = lrc
		}
	}

	report(5, "解析直链…", nil)
	dir := filepath.Join(t.dataDir, "music", uuid.NewString())
	res, err := qingmusic.NewDownloader().Download(ctx, track, qingmusic.DownloadOptions{
		Dir:         dir,
		Level:       level,
		Sources:     sources,
		Format:      qingmusic.FormatSongArtist,
		Tag:         wantTag,
		Cover:       wantTag,
		Lyrics:      lyricText,
		MaxAttempts: 3,
		Timeout:     10 * time.Minute,
		Progress: func(p qingmusic.DownloadProgress) {
			// 库内已 300ms 节流；5→99 留给下载进度
			pct := 5 + int(p.Percent)*94/100
			if pct > 99 {
				pct = 99
			}
			note := "下载中…"
			if p.Total > 0 {
				note = fmt.Sprintf("下载中 %.1f MB / %.1f MB", float64(p.Written)/1e6, float64(p.Total)/1e6)
			}
			report(pct, note, nil)
		},
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}

	rel, err := filepath.Rel(t.dataDir, res.Path)
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("产物路径越界: %w", err)
	}
	format := res.Ext
	if res.RealExt != "" {
		format = res.RealExt
	}

	summary := map[string]any{"title": songTitle(name, artist)}
	report(100, "完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind:       "audio",
			Path:       rel,
			Format:     format,
			Size:       res.Size,
			DurationMS: durationMS,
			Meta: map[string]any{
				"source": source, "rid": rid, "level": res.Level,
				"artist": artist, "lyric": res.LyricPath != "", "attempts": res.Attempts,
			},
		}},
		Summary: summary,
	}, nil
}

// ResultTitle 任务标题「歌名 - 歌手」：与历史音乐下载命名惯例对齐，
// 全文搜索按标题即可命中歌曲。
func (t *MusicTool) ResultTitle(summaryJSON string) string {
	var s struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(summaryJSON), &s); err != nil {
		return ""
	}
	return s.Title
}

func songTitle(name, artist string) string {
	if artist == "" {
		return name
	}
	return name + " - " + artist
}

func strParam(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func boolParam(params map[string]any, key string, def bool) bool {
	switch v := params[key].(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func int64Param(params map[string]any, key string) int64 {
	switch v := params[key].(type) {
	case float64:
		return int64(v)
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n
		}
	}
	return 0
}
