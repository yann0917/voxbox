package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/qingmusic"
)

// mvMaxDownload 单条 MV 上限 1GB（蓝光画质量级，防异常 URL 拖爆磁盘）。
const mvMaxDownload = 1024 << 20

// MVTool MV 下载：按音源线路解析 MV 画质直链，流式落盘并校验 MP4 头
// （直链过期时 CDN 常回 HTTP 200 的 HTML 错误页，不能让它冒充成品）。
type MVTool struct {
	dataDir string
}

func NewMVTool(dataDir string) *MVTool { return &MVTool{dataDir: dataDir} }

func (t *MVTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "music",
		Name:        "music_mv_download",
		Title:       "MV 下载",
		Description: "下载歌曲 MV（多画质自动匹配），落盘为视频产物；直链过期自动重试换新",
		Group:       "语音",
	}
}

func (t *MVTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "source", Label: "音源", Type: provider.ParamString, Required: true,
			Placeholder: "音源 ID，如 kw / kg / wy / tx / mg", Group: "输入"},
		{Key: "vid", Label: "MV ID", Type: provider.ParamString, Required: true, Group: "输入"},
		{Key: "name", Label: "歌名", Type: provider.ParamString, Required: true, Group: "输入"},
		{Key: "artist", Label: "歌手", Type: provider.ParamString, Group: "输入"},
		{Key: "quality", Label: "画质", Type: provider.ParamString, Group: "参数",
			Placeholder: "留空取最高；如 1080p / 720p / 480p"},
	}
}

func (t *MVTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	source := strings.TrimSpace(strParam(in.Params, "source"))
	vid := strings.TrimSpace(strParam(in.Params, "vid"))
	name := strings.TrimSpace(strParam(in.Params, "name"))
	artist := strings.TrimSpace(strParam(in.Params, "artist"))
	quality := strings.TrimSpace(strParam(in.Params, "quality"))
	if source == "" || vid == "" || name == "" {
		return provider.TaskOutput{}, fmt.Errorf("音源 / MV ID / 歌名 均必填")
	}

	dir := filepath.Join(t.dataDir, "music", uuid.NewString())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建下载目录失败: %w", err)
	}

	client := qingmusic.NewMVClient()
	line := qingmusic.LineNameOf(source)
	if line == "" {
		line = qingmusic.LineTx
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		qs, err := client.PlaybackURLs(ctx, line, vid)
		if err != nil {
			lastErr = err
		} else {
			picked, perr := qingmusic.PickQuality(qs, quality)
			if perr != nil {
				return provider.TaskOutput{}, perr // 画质名不对是确定性错误，重试无意义
			}
			path, size, ferr := t.fetchMV(ctx, picked.URL, dir, report)
			if ferr == nil {
				return t.register(path, size, picked.Name, name, artist, report)
			}
			lastErr = ferr
		}
		if attempt < 3 && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return provider.TaskOutput{}, ctx.Err()
			case <-time.After(time.Duration(attempt*600) * time.Millisecond):
			}
		}
	}
	return provider.TaskOutput{}, lastErr
}

// fetchMV 流式拉取 MV 直链（带 UA 防空 UA 收紧），1GB 上限 + MP4 头校验。
func (t *MVTool) fetchMV(ctx context.Context, rawURL, dir string, report provider.ProgressReporter) (string, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", qingmusic.DefaultMobileUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("下载 MV 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("下载 MV 失败: HTTP %d", resp.StatusCode)
	}

	report(5, "下载中…", nil)
	part := filepath.Join(dir, "mv.mp4.part")
	f, err := os.Create(part)
	if err != nil {
		return "", 0, err
	}
	total := resp.ContentLength
	var written int64
	buf := make([]byte, 256<<10)
	lastPct := 0
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > mvMaxDownload {
				_ = f.Close()
				_ = os.Remove(part)
				return "", 0, fmt.Errorf("下载 MV 失败: 超过 1GB 上限")
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				_ = os.Remove(part)
				return "", 0, werr
			}
			if total > 0 {
				pct := 5 + int(written*94/total)
				if pct-lastPct >= 2 || written == total {
					lastPct = pct
					report(pct, fmt.Sprintf("下载中 %.1f / %.1f MB", float64(written)/1e6, float64(total)/1e6), nil)
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = f.Close()
			_ = os.Remove(part)
			return "", 0, fmt.Errorf("下载 MV 失败: %w", rerr)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(part)
		return "", 0, err
	}
	if written == 0 || !hasFTYPBox(part) {
		_ = os.Remove(part)
		return "", 0, fmt.Errorf("下载 MV 失败: 拿到的不是 MP4（多半是直链过期返回的错误页）")
	}
	return part, written, nil
}

// hasFTYPBox 读文件头找 MP4 的 ftyp 盒（正常在 b[4:8]；带前置盒的在前 32 字节内搜索兜底）。
func hasFTYPBox(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [32]byte
	n, _ := f.Read(b[:])
	return n >= 8 && strings.Contains(string(b[:n]), "ftyp")
}

func (t *MVTool) register(part string, size int64, qualityName, name, artist string, report provider.ProgressReporter) (provider.TaskOutput, error) {
	final := filepath.Join(filepath.Dir(part), "【MV】"+songTitle(name, artist)+".mp4")
	if err := os.Rename(part, final); err != nil {
		_ = os.Remove(part)
		return provider.TaskOutput{}, err
	}
	rel, err := filepath.Rel(t.dataDir, final)
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("产物路径越界: %w", err)
	}
	report(100, "完成", nil)
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "video", Path: rel, Format: "mp4", Size: size,
			Meta: map[string]any{"mv": true, "quality": qualityName, "artist": artist},
		}},
		Summary: map[string]any{"title": "【MV】" + songTitle(name, artist)},
	}, nil
}

// ResultTitle 任务标题「【MV】歌名 - 歌手」。
func (t *MVTool) ResultTitle(summaryJSON string) string {
	var s struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(summaryJSON), &s); err != nil {
		return ""
	}
	return s.Title
}
