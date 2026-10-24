package qingmusic

// 下载模块：复刻 QingMusic v1.3.9 的 downloadSongWithMetadata 流水线。
//
// 逆向依据（见 DOWNLOAD.md）：
//   - 编排（取直链 → 拼名 → 下载 → 写封面/歌词/标签）全在 JS 侧，原生只提供
//     RNFSManager（按 URL 存文件）与 LocalMediaMetadata（读写音频标签）。
//   - 扩展名推断     Hermes #12512 偏移 0x2e7~0x3a9
//   - 不覆盖命名     Hermes #12504（dir/name (N).ext，N 1..10000）
//   - 命名格式取值   字符串表 431=artist-song / 432=song-artist（默认），
//                     UI 文案 17595=歌手-歌名 / 17597=歌名-歌手
//   - 非法字符清洗   Hermes #12512 偏移 0x3b1（[<>:"/\|?*] → _）+ 名字截断 80
//
// 与原版的差异（有意为之，已在各处注释标注）：
//   - 原版进度靠 DeviceEventEmitter 事件回流；这里用回调。
//   - 原版没有断点续传（只靠 RNFS 的 background:true）；这里同样不续传。
//   - 原版下载记录存进 MMKV 的 'downloads' 键；voxbox 由任务系统（Task+Artifact）
//     记账，DownloadOptions.Store 通道整体移除（原 downloads.go 不随包迁移）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- 命名格式

// 下载文件命名格式。App 的 downloadFormat 只有这两个取值。
const (
	FormatSongArtist = "song-artist" // 歌名 - 歌手（默认）
	FormatArtistSong = "artist-song" // 歌手 - 歌名
)

// 各字段缺失时的占位（对应字符串表 19841 / 19839 / 12323）。
const (
	DefaultSongName   = "未知歌曲"
	DefaultArtistName = "未知歌手"
	DefaultAlbumName  = "未知专辑"
)

// NameMaxLen 文件名长度上限（App 用 substring(0, 80) 截断）。
const NameMaxLen = 80

// 非法字符替换规则，原样取自字节码：replace(/[<>:"/\\|?*]/g, '_')
var illegalNameRe = regexp.MustCompile(`[<>:"/\\|?*]`)

// 酷我 kwstream 直链的音质标记。
var (
	kwLevelRe = regexp.MustCompile(`(?i)level=([^&]+)`)
	// level 落在这三个档位时，酷我给的是 flac 容器。
	kwFlacLevels = map[string]bool{"atmos": true, "atmos_plus": true, "master": true}
)

// 内嵌标签只支持这两种容器（App 的 FAQ 原文：目前仅支持 mp3 和 flac 格式的
// 音频嵌入歌词和封面）。其余一律跳过，且**不影响下载成功**。
const (
	extMp3  = "mp3"
	extFlac = "flac"
)

// ---------------------------------------------------------------- 选项

// DownloadOptions 下载行为开关。零值即「默认目录 + lossless 起降级 + 歌名-歌手 + 写标签」。
type DownloadOptions struct {
	Dir   string // 目标目录，空 → DefaultDownloadDir()
	Level string // 指定音质；空 → 依次尝试 lossless / exhigh / standard

	// Levels 显式指定降级链（优先级最高，给了就忽略 Level 与 Sources）。
	// 一般不用手填 —— 交给 Sources 按音源配置推导即可。
	Levels []string

	// Sources 音源配置（见 sourceconfig.go）。给了就按它来决定降级链：
	// 只用该音源真正声明支持的档位，从 Level 起往下退。
	// 同时如果该音源在配置里被 enabled=false，Download 会直接拒绝（连解析都不发），
	// 对应 App 的「关闭的音源不会在搜索时显示」。
	Sources *SourceConfig

	// Format 取值 FormatSongArtist（默认）或 FormatArtistSong。
	Format string

	Overwrite bool   // 同名是否覆盖；false → dir/name (N).ext
	Tag       bool   // 写音频标签（title/artist/album）
	Cover     bool   // 额外把封面写进标签（需 Tag 为真且曲目带 Pic）
	Lyrics    string // .lrc 文本；空则不写。Go 侧暂无歌词接口，由调用方注入

	ForceExt string // 强制扩展名，等价于 App 的 forceExtension（bili → "m4a"）

	// FixExt 落盘后按**实际容器**修正扩展名。
	// 默认 false —— 沿用 App 的 URL 推断，实测酷我 lossless 会被命名成 .mp3
	// （内容其实是 m4a/aac）。需要后缀与内容一致时打开。
	FixExt bool

	// MaxAttempts 失败重试次数（默认 3，<1 视为 3）。
	//
	// 重试时会**重新解析一次直链**，拿新的签名 URL 再打一次 —— 实测部分
	// 酷狗曲目的 CDN 会对某个 flac 资源直接返 403（同曲三个音质档全 403，
	// 换个 rid 就正常），重新解析换到另一个节点常能过。
	MaxAttempts int

	Timeout time.Duration

	Progress func(DownloadProgress) // 进度回调，可空
}

// DownloadProgress 一次进度上报。
type DownloadProgress struct {
	Written int64
	Total   int64 // -1 表示对端未给 Content-Length
	Percent float64
	Speed   int64 // 字节/秒
}

// DownloadResult 下载结果。
type DownloadResult struct {
	Track   Track
	Path    string
	Ext     string
	Size    int64
	Level   string
	Elapsed time.Duration

	// RealExt 是嗅探文件头得到的真实容器。Ext 与它不一致时说明 App 的
	// URL 推断把这个文件命名错了（酷我 lossless 就是这样）。
	RealExt string

	// Tagged 标签已写入；TagSkipped 非空表示按 App 行为跳过并给出原因；
	// TagWarning 非空表示写了但失败，音频文件已保留（App 的行为）。
	Tagged     bool
	TagSkipped string
	TagWarning string

	LyricPath string
	CoverPath string
	Existed   bool // 目标已存在且未覆盖时，Path 指向的是新的 (N) 文件

	// Attempts 实际尝试次数（1 = 一次成功）。
	Attempts int
}

// Downloader 下载器。取直链复用 Gateway，落盘走自己的 HTTP 客户端。
type Downloader struct {
	Gateway *Gateway
	HTTP    *http.Client
}

// newMediaTransport 专供音频下载的 Transport。
//
// 必须 DisableKeepAlives：下载走的是**明文 HTTP**（酷我/酷狗的直链都是 http），
// 而下载前刚对网关发过 HTTPS 请求。实测在设了 HTTP_PROXY 的环境里，
// 同进程内复用连接会让明文 HTTP 请求打到错误的 target —— 表现就是
// 「curl 连取 8 次都 200，Go 客户端恒定 403」（返回的是别的站点的 nginx 403 页）。
// 不复用连接 = 每次下载新开一条，彻底绕开这一类串线。
func newMediaTransport() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{DisableKeepAlives: true}
	}
	clone := t.Clone()
	clone.DisableKeepAlives = true
	return clone
}

func newMediaClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: newMediaTransport()}
}

// NewDownloader 默认主线路 + 10 分钟媒体超时。
func NewDownloader() *Downloader {
	return &Downloader{
		Gateway: NewGateway(),
		HTTP:    newMediaClient(10 * time.Minute),
	}
}

// NewDownloaderWith 指定线路（传 ServiceLines[i].BaseURL）。
func NewDownloaderWith(baseURL string) *Downloader {
	return &Downloader{
		Gateway: NewGatewayWith(baseURL),
		HTTP:    newMediaClient(10 * time.Minute),
	}
}

// DefaultDownloadDir 默认落盘目录。
//
// App 的默认是 <外部存储>/Download/青听音乐（getDownloadPath 在 storage 无
// downloadPath 时回退到这里）。桌面端取同样的相对结构。
func DefaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Downloads", "青听音乐")
}

// ---------------------------------------------------------------- 命名规则

// InferExtension 复刻 App 的扩展名推断，顺序即优先级：
//
//	forceExtension → url 含 .flac → url 含 .aac
//	→ url 含 kwstream 且 level ∈ {atmos,atmos_plus,master} → flac
//	→ 兜底 mp3
//
// 注意 App 只按 URL 字面量判断，不看实际容器。实测酷我 lossless 给的是
// `.../trackmedia/xxx.m4a?bitrate$48&format$aac` —— 这种 URL 三个条件都不命中，
// App 会存成 .mp3（内容其实是 m4a/aac）。这里保持同一行为，不做纠正，
// 需要正确后缀时用 ForceExt 显式指定。
func InferExtension(rawURL, force string) string {
	if force != "" {
		return normalizeExt(force)
	}
	lower := strings.ToLower(rawURL)
	switch {
	case strings.Contains(lower, ".flac"):
		return extFlac
	case strings.Contains(lower, ".aac"):
		return "aac"
	case strings.Contains(lower, "kwstream"):
		if m := kwLevelRe.FindStringSubmatch(rawURL); m != nil && kwFlacLevels[strings.ToLower(m[1])] {
			return extFlac
		}
	}
	return extMp3
}

// NormalizeExtension 扩展名归一化：去前导点、转小写；空或含路径/非法字符时返回空串。
//
// 导出是为了让「预览」与「真下」用同一套规则 —— CLI 的 -dry 要先把目标文件名
// 算出来给用户看，若预览按一套规则、实际落盘按另一套，预览就是在骗人
// （实测踩过：-ext .MKV 预览出 .MKV，真下用的是 .mkv）。
func NormalizeExtension(s string) string {
	s = strings.TrimLeft(strings.ToLower(strings.TrimSpace(s)), ".")
	if s == "" || strings.ContainsAny(s, `/\:*?"<>|`) {
		return ""
	}
	return s
}

// normalizeExt 去掉前导点并转小写，空值回落到 mp3（歌曲侧的默认容器）。
//
// App: String(x).replace(/^\./,”).toLowerCase()。
func normalizeExt(s string) string {
	if v := NormalizeExtension(s); v != "" {
		return v
	}
	return extMp3
}

// BuildFileName 拼文件名主体（不含扩展名）。
//
// song-artist（默认）→ "歌名 - 歌手"；artist-song → "歌手 - 歌名"。
func BuildFileName(tr Track, format string) string {
	song := sanitizeFileName(orDefault(tr.Name, DefaultSongName))
	artist := sanitizeFileName(orDefault(tr.Artist, DefaultArtistName))

	var name string
	if format == FormatArtistSong {
		name = artist + " - " + song
	} else {
		name = song + " - " + artist
	}
	return truncateRunes(name, NameMaxLen)
}

// sanitizeFileName 把非法字符换成下划线。
func sanitizeFileName(s string) string {
	return illegalNameRe.ReplaceAllString(s, "_")
}

// orDefault 空（含纯空白）时取默认值。
func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// truncateRunes 按字符（不是字节）截断，避免切坏中文。
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// nonOverwritingPath 复刻 getNonOverwritingFilePath：
// 依次试 "name (1).ext"、"name (2).ext"…最多 10000 次，仍冲突则用毫秒时间戳。
// extension 需带前导点。
func nonOverwritingPath(dir, name, extension string) string {
	for i := 1; i < 10000; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", name, i, extension))
		if !fileExists(p) {
			return p
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", name, time.Now().UnixMilli(), extension))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---------------------------------------------------------------- 下载主体

// Download 下一首歌：解析直链 → 落盘 → 写标签/歌词/封面。
//
// 任何一步失败都返回 error；标签阶段失败**不**影响音频文件（见 §6.4 的兜底）。
func (d *Downloader) Download(ctx context.Context, tr Track, opt DownloadOptions) (*DownloadResult, error) {
	start := time.Now()
	opt = opt.withDefaults()

	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("qingmusic: 创建下载目录失败: %w", err)
	}

	var (
		level    string
		ext      string
		base     = BuildFileName(tr, opt.Format)
		target   string
		size     int64
		existed  bool
		attempts int
	)
	for attempts = 1; attempts <= opt.MaxAttempts; attempts++ {
		resolved, lv, err := d.resolveWithFallback(ctx, tr.Source, tr.Rid, opt.Level, opt)
		if err != nil {
			if attempts < opt.MaxAttempts && ctx.Err() == nil {
				time.Sleep(retryBackoff(attempts))
				continue
			}
			return nil, err
		}
		level = lv

		// 每次重试都可能换到别的音质档，扩展名与文件名要跟着重算
		// （lossless 是 flac、降级到 standard 就是 mp3）。
		ext = InferExtension(resolved.URL, opt.ForceExt)
		target = filepath.Join(opt.Dir, base+"."+ext)
		existed = fileExists(target)
		if existed && !opt.Overwrite {
			target = nonOverwritingPath(opt.Dir, base, "."+ext)
		}

		size, err = d.fetchToFile(ctx, resolved, target, opt)
		if err == nil {
			break
		}
		if !isRetryable(err) || attempts >= opt.MaxAttempts || ctx.Err() != nil {
			return nil, err
		}
		time.Sleep(retryBackoff(attempts))
	}

	out := &DownloadResult{
		Track:    tr,
		Path:     target,
		Ext:      ext,
		Size:     size,
		Level:    level,
		Elapsed:  time.Since(start),
		Existed:  existed,
		Attempts: attempts,
	}

	// 嗅探真实容器。Ext 只来自 URL 推断，实测酷我 lossless 会推成 mp3 而
	// 实际是 m4a/aac；这一步同时兜住了「按扩展名写标签会把文件写坏」。
	real := SniffContainer(target)
	out.RealExt = string(real)

	if opt.FixExt && real != ContainerUnknown && string(real) != ext {
		fixed := filepath.Join(opt.Dir, base+"."+string(real))
		if fileExists(fixed) && !opt.Overwrite {
			fixed = nonOverwritingPath(opt.Dir, base, "."+string(real))
		}
		if err := os.Rename(target, fixed); err != nil {
			out.TagWarning = "按实际容器改名失败: " + err.Error()
		} else {
			target = fixed
			ext = string(real)
			out.Path = fixed
			out.Ext = ext
		}
	}

	// 歌词：App 会在标签里写一份、旁边再落一个 .lrc。
	if opt.Lyrics != "" {
		lrcPath := target + ".lrc"
		if err := os.WriteFile(lrcPath, []byte(opt.Lyrics), 0o644); err != nil {
			out.TagWarning = "LRC 写入失败: " + err.Error()
		} else {
			out.LyricPath = lrcPath
		}
	}

	// 标签 + 封面。AAC/M4A 按 App 行为直接跳过。
	if opt.Tag {
		meta := TagMeta{
			Title:  orDefault(tr.Name, DefaultSongName),
			Artist: orDefault(tr.Artist, DefaultArtistName),
			Album:  DefaultAlbumName,
			Lyrics: opt.Lyrics,
		}
		if tr.Album != "" {
			meta.Album = tr.Album
		}

		if opt.Cover && tr.Pic != "" {
			data, mime, err := d.fetchBytes(ctx, tr.Pic)
			if err != nil {
				out.TagWarning = "封面下载失败: " + err.Error()
			} else {
				meta.CoverData = data
				meta.CoverMIME = mime
			}
		}

		skipped, err := WriteTags(target, meta)
		switch {
		case err != nil:
			// App 的行为：保留已下载的音频文件，只打警告。
			out.TagWarning = err.Error()
		case skipped != "":
			out.TagSkipped = skipped
		default:
			out.Tagged = true
		}
	}

	// 写标签/封面会改变文件长度，落盘记录按最终文件大小算，
	// 免得列表里的数字和 Finder 里看到的不一致。
	if st, err := os.Stat(target); err == nil {
		size = st.Size()
		out.Size = size
	}

	return out, nil
}

// DownloadMany 顺序下载多首，对应 App 的「歌单/批量下载」。
//
// 单首失败只记该首，不中断整体（App 的 catch 在循环体内）。
func (d *Downloader) DownloadMany(ctx context.Context, tracks []Track, opt DownloadOptions) ([]*DownloadResult, []error) {
	results := make([]*DownloadResult, 0, len(tracks))
	errs := make([]error, 0)
	for _, tr := range tracks {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		r, err := d.Download(ctx, tr, opt)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s - %s: %w", tr.Name, tr.Artist, err))
			continue
		}
		results = append(results, r)
	}
	return results, errs
}

func (o DownloadOptions) withDefaults() DownloadOptions {
	if o.Dir == "" {
		o.Dir = DefaultDownloadDir()
	}
	if o.Format == "" {
		o.Format = FormatSongArtist
	}
	if o.MaxAttempts < 1 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Minute
	}
	return o
}

// DefaultMaxAttempts 默认重试次数。
const DefaultMaxAttempts = 3

// retryBackoff 退避：600ms / 1200ms / …（对齐 App 侧 doRetry 的 400·n 节奏）。
func retryBackoff(attempt int) time.Duration {
	return time.Duration(600*attempt) * time.Millisecond
}

// fetchError 带「是否值得重试」标记的下载错误。
type fetchError struct {
	msg       string
	retryable bool
}

func (e *fetchError) Error() string { return e.msg }

// isRetryable 只有 CDN 侧的拒绝（4xx/5xx）与网络错误才重试；
// 本地写盘失败、响应被截断这类重试也没用。
func isRetryable(err error) bool {
	var fe *fetchError
	if errors.As(err, &fe) {
		return fe.retryable
	}
	return false
}

// resolveWithFallback 解析直链。档位链的优先级：
//
//	opt.Levels → opt.Sources 按配置推导 → 写死的 lossless → exhigh → standard
//
// 返回实际拿到的档位。
func (d *Downloader) resolveWithFallback(ctx context.Context, source, rid, level string, opt DownloadOptions) (*Resolved, string, error) {
	levels := opt.Levels
	if len(levels) == 0 && opt.Sources != nil {
		// 配置里禁用的音源不发请求 —— 省一次必然失败的往返。
		if _, ok := opt.Sources.Line(source); ok && !opt.Sources.Enabled(source) {
			return nil, "", fmt.Errorf("qingmusic: 音源 %s 在配置里已关闭", source)
		}
		levels = opt.Sources.LevelsFor(source, level)
	}
	if len(levels) == 0 {
		levels = []string{level}
		if level == "" {
			levels = []string{LevelLossless, LevelExhigh, LevelStandard}
		}
	}

	var lastErr error
	for _, lv := range levels {
		r, err := d.Gateway.ResolveURL(ctx, source, rid, lv)
		if err == nil && r.URL != "" {
			return r, lv, nil
		}
		if err == nil {
			err = errors.New("解析服务未返回播放地址")
		}
		lastErr = fmt.Errorf("[%s] %w", lv, err)
	}
	return nil, "", fmt.Errorf("qingmusic: 所有音质都没拿到直链: %w", lastErr)
}

// mediaHeaders 合并播放所需请求头（App 取 playbackHeaders，回退 headers）。
func mediaHeaders(r *Resolved) map[string]string {
	if len(r.PlaybackHeaders) > 0 {
		return r.PlaybackHeaders
	}
	return r.Headers
}

// fetchToFile 落盘：先写 .part 临时文件，成功后再改名，避免半截文件冒充成品。
// 返回实际写入字节数。
func (d *Downloader) fetchToFile(ctx context.Context, r *Resolved, target string, opt DownloadOptions) (int64, error) {
	// 复制客户端只改超时，Transport 仍共用那一条（DisableKeepAlives）。
	client := *d.HTTP
	if opt.Timeout > 0 {
		client.Timeout = opt.Timeout
	}
	return fetchURLToFile(ctx, &client, fetchSpec{
		URL:      r.URL,
		Headers:  mediaHeaders(r),
		Target:   target,
		What:     "音频",
		Progress: opt.Progress,
	})
}

// fetchSpec 一次「把 URL 落成文件」的全部入参。歌曲与 MV 共用下面这个函数，
// 差别只有 URL 从哪来、要不要带请求头、以及错误文案里的名词。
type fetchSpec struct {
	URL     string
	Headers map[string]string
	Target  string // 最终路径；内部先写 <target>.part 再改名
	What    string // 错误文案里的名词："音频" / "MV"
	// Timeout 由调用方在 client 上设好，这里不再单独处理。
	Progress func(DownloadProgress)
}

// fetchURLToFile 落盘：先写 .part 临时文件，成功后再改名，避免半截文件冒充成品。
//
// 返回实际写入字节数。失败一律是 *fetchError，由 isRetryable 决定值不值得重来。
func fetchURLToFile(ctx context.Context, client *http.Client, spec fetchSpec) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range spec.Headers {
		req.Header.Set(k, v)
	}

	res, err := client.Do(req)
	if err != nil {
		return 0, &fetchError{
			msg:       fmt.Sprintf("qingmusic: %s下载请求失败: %s", spec.What, err),
			retryable: ctx.Err() == nil,
		}
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return 0, &fetchError{
			msg: fmt.Sprintf("qingmusic: %s下载 HTTP %d: %s", spec.What, res.StatusCode, strings.TrimSpace(string(snippet))),
			// 403/404 常见于 CDN 侧缺这个资源（换节点/换音质可能就有），
			// 5xx 与 429 属于服务端临时状态，都值得重新解析再打一次。
			retryable: res.StatusCode == http.StatusForbidden ||
				res.StatusCode == http.StatusNotFound ||
				res.StatusCode == http.StatusTooManyRequests ||
				res.StatusCode >= 500,
		}
	}

	// 对端大小，用于和实际写入量对账（对应 App 的 [下载文件校验] 日志）。
	expect := res.ContentLength

	tmp := spec.Target + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}

	var written int64
	pr := &progressReader{
		r:       res.Body,
		total:   expect,
		opt:     spec.Progress,
		started: time.Now(),
		report:  time.Now(),
	}
	written, err = io.Copy(f, pr)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		// 绝大多数是读流中断（连接被掐/超时），重来一次往往就好。
		return written, &fetchError{msg: "qingmusic: 写入失败: " + err.Error(), retryable: ctx.Err() == nil}
	}

	if expect > 0 && written != expect {
		os.Remove(tmp)
		return written, &fetchError{
			msg:       fmt.Sprintf("qingmusic: 文件不完整，预期 %d 字节，实际 %d 字节", expect, written),
			retryable: true,
		}
	}
	if written == 0 {
		os.Remove(tmp)
		return 0, &fetchError{msg: fmt.Sprintf("qingmusic: %s下载为空（0 字节）", spec.What), retryable: true}
	}
	if err := os.Rename(tmp, spec.Target); err != nil {
		os.Remove(tmp)
		return written, err
	}

	// 补一次 100% 上报，避免最后一跳被节流吞掉。
	if spec.Progress != nil {
		pr.emit(written, true)
	}
	return written, nil
}

// fetchBytes 取二进制内容（当前只用于封面图片）。自动嗅探 MIME 与实际类型。
func (d *Downloader) fetchBytes(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	res, err := d.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 20<<20)) // 封面最多 20MB
	if err != nil {
		return nil, "", err
	}
	mime := res.Header.Get("Content-Type")
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	mime = strings.TrimSpace(mime)
	switch {
	case strings.HasPrefix(mime, "image/"):
	case len(data) > 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		mime = "image/png"
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
		mime = "image/jpeg"
	default:
		mime = "image/jpeg"
	}
	if len(data) == 0 {
		return nil, "", errors.New("封面内容为空")
	}
	return data, mime, nil
}

// progressReader 统计已写字节并按 300ms 节流上报
// （App 的 progressInterval 是 100ms，事件走 DeviceEventEmitter）。
type progressReader struct {
	r       io.Reader
	total   int64
	opt     func(DownloadProgress) // 进度回调，可空
	started time.Time

	mu       sync.Mutex
	written  int64
	report   time.Time
	lastByte int64
	lastTick time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.mu.Lock()
		p.written += int64(n)
		w := p.written
		due := time.Since(p.report) >= 300*time.Millisecond
		if due {
			p.report = time.Now()
		}
		p.mu.Unlock()
		if due {
			p.emit(w, false)
		}
	}
	return n, err
}

func (p *progressReader) emit(written int64, final bool) {
	if p.opt == nil {
		return
	}
	p.mu.Lock()
	now := time.Now()
	var speed int64
	if !p.lastTick.IsZero() {
		dt := now.Sub(p.lastTick).Seconds()
		if dt > 0 {
			speed = int64(float64(written-p.lastByte) / dt)
		}
	} else {
		if dt := now.Sub(p.started).Seconds(); dt > 0 {
			speed = int64(float64(written) / dt)
		}
	}
	p.lastByte, p.lastTick = written, now
	p.mu.Unlock()

	prog := DownloadProgress{Written: written, Total: p.total, Speed: speed}
	if p.total > 0 {
		prog.Percent = float64(written) / float64(p.total) * 100
		if prog.Percent > 100 {
			prog.Percent = 100
		}
	} else if final {
		prog.Percent = 100
	}
	p.opt(prog)
}
