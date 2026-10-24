// Package downloader 链接下载工具：把外部下载器接成任务。
// yt-dlp 直调（PATH 或设置页指定路径）；douyin-downloader 是外部 Python 项目，
// 以「用户 config.yml + 追加覆盖段」的方式生成临时配置调用，cookie 由用户配置自管。
package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/yann0917/voxbox/internal/provider"
)

// Tool 链接下载工具。路径类配置在构造时注入（凭证卡保存后热重注册生效）。
type Tool struct {
	ytdlpPath     string
	douyinCommand string
	douyinConfig  string
	dataDir       string
}

func NewTool(ytdlpPath, douyinCommand, douyinConfig, dataDir string) *Tool {
	return &Tool{
		ytdlpPath:     ytdlpPath,
		douyinCommand: douyinCommand,
		douyinConfig:  douyinConfig,
		dataDir:       dataDir,
	}
}

func (t *Tool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider: "downloader",
		Name:     "download",
		Title:    "链接下载",
		Description: "解析视频/短视频链接下载音视频与字幕（yt-dlp；抖音链接走 douyin-downloader），" +
			"产物入库后可送识别、去字幕工坊",
		Group: "语音",
	}
}

func (t *Tool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "链接", Type: provider.ParamString, Required: true,
			Placeholder: "视频 / 合集 / 抖音分享链接", Group: "输入"},
		{Key: "backend", Label: "下载器", Type: provider.ParamEnum, Default: "auto", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "auto", Label: "自动（按链接域名）"},
				{Value: "ytdlp", Label: "yt-dlp"},
				{Value: "douyin", Label: "douyin-downloader"},
			}},
		{Key: "mode", Label: "产物", Type: provider.ParamEnum, Default: "audio", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "audio", Label: "仅音频"},
				{Value: "video", Label: "音视频"},
			}},
		{Key: "max_items", Label: "合集条数上限", Type: provider.ParamInt, Default: 1, Group: "参数",
			Placeholder: "0 = 不限；仅对合集/主页链接生效"},
		{Key: "subtitles", Label: "下载字幕（srt）", Type: provider.ParamBool, Default: false, Group: "参数"},
		{Key: "douyin_mode", Label: "抖音下载对象", Type: provider.ParamEnum, Default: "post", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "post", Label: "作品"},
				{Value: "mix", Label: "合集"},
				{Value: "music", Label: "原声"},
				{Value: "like", Label: "喜欢"},
				{Value: "collect", Label: "收藏"},
			}},
	}
}

func (t *Tool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	link := strings.TrimSpace(strParam(in.Params, "url"))
	if link == "" {
		return provider.TaskOutput{}, fmt.Errorf("链接必填")
	}
	backend := strParam(in.Params, "backend")
	if backend == "" {
		backend = "auto"
	}
	mode := strParam(in.Params, "mode")
	if mode == "" {
		mode = "audio"
	}
	maxItems := intParam(in.Params, "max_items")
	wantSubs := boolParam(in.Params, "subtitles", false)
	douyinMode := strParam(in.Params, "douyin_mode")
	if douyinMode == "" {
		douyinMode = "post"
	}

	if backend == "auto" {
		if isDouyinURL(link) {
			backend = "douyin"
		} else {
			backend = "ytdlp"
		}
	}

	dir := filepath.Join(t.dataDir, "downloads", uuid.NewString())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建下载目录失败: %w", err)
	}

	switch backend {
	case "ytdlp":
		return t.runYtdlp(ctx, link, mode, maxItems, wantSubs, dir, report)
	case "douyin":
		return t.runDouyin(ctx, link, douyinMode, maxItems, dir, report)
	default:
		return provider.TaskOutput{}, fmt.Errorf("未知下载器 %q", backend)
	}
}

// ---------------------------------------------------------------- yt-dlp

var ytdlpProgressRe = regexp.MustCompile(`\[download\]\s+(\d+(?:\.\d+)?)%`)

// mediaExtKinds 产物扩展名 → artifact kind；下载目录里只有命中的文件才登记。
var mediaExtKinds = map[string]string{
	".mp3": "audio", ".m4a": "audio", ".aac": "audio", ".flac": "audio",
	".wav": "audio", ".ogg": "audio", ".opus": "audio",
	".mp4": "video", ".mkv": "video", ".webm": "video", ".mov": "video",
	".srt": "subtitle", ".vtt": "subtitle", ".ass": "subtitle",
}

func (t *Tool) runYtdlp(ctx context.Context, link, mode string, maxItems int, wantSubs bool, dir string, report provider.ProgressReporter) (provider.TaskOutput, error) {
	bin, err := t.resolveYtdlp()
	if err != nil {
		return provider.TaskOutput{}, err
	}

	args := []string{
		"--newline", "--no-warnings", "--no-playlist", "--write-info-json",
		"-o", filepath.Join(dir, "%(title)s [%(id)s].%(ext)s"),
	}
	if mode == "audio" {
		args = append(args, "-f", "bestaudio/best")
	} else {
		args = append(args, "-f", "bv*+ba/b")
	}
	if maxItems > 0 {
		args = append(args, "--playlist-items", fmt.Sprintf("1:%d", maxItems))
	}
	if wantSubs {
		// 语言给具体枚举：通配（zh.*,en.*）会命中自动字幕的全部翻译组合轨道，
		// YouTube 字幕端点直接 429 限流。--ignore-errors 让单条字幕 429 不拖垮
		// 整体（主媒体缺失时 collectArtifacts 会兜底报错）。
		args = append(args, "--write-subs", "--write-auto-subs", "--sub-langs", "zh-Hans,zh-Hant,en", "--convert-subs", "srt")
	}
	// ffmpeg 缺失时字幕转 srt 之类的后处理会挂；找得到就显式指路（桌面版 PATH 已含）。
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		args = append(args, "--ffmpeg-location", ff)
	}
	args = append(args, "--ignore-errors")
	args = append(args, link)

	report(3, "启动 yt-dlp…", nil)
	outTail, errTail, runErr := streamProcess(ctx, bin, args, func(line string) {
		if m := ytdlpProgressRe.FindStringSubmatch(line); m != nil {
			pct, _ := strconv.ParseFloat(m[1], 64)
			p := 3 + int(pct)*95/100
			if p > 98 {
				p = 98
			}
			report(p, "下载中 "+m[1]+"%", nil)
		}
	})
	if runErr != nil {
		return provider.TaskOutput{}, fmt.Errorf("yt-dlp 下载失败: %s", ytdlpErrMessage(outTail, errTail, runErr))
	}
	report(99, "登记产物…", nil)
	return collectArtifacts(t.dataDir, dir)
}

func (t *Tool) resolveYtdlp() (string, error) {
	if p := strings.TrimSpace(t.ytdlpPath); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("设置的 yt-dlp 路径不可用: %w", err)
		}
		return p, nil
	}
	p, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", fmt.Errorf("未找到 yt-dlp：请安装（pip install yt-dlp）或在设置页「外部下载器」填写完整路径")
	}
	return p, nil
}

// ---------------------------------------------------------------- douyin-downloader

// isDouyinURL 按 host 判断是否抖音链接（分享短链 v.douyin.com 一并覆盖）。
func isDouyinURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, "douyin.com") || strings.HasSuffix(host, "iesdouyin.com")
}

func (t *Tool) runDouyin(ctx context.Context, link, douyinMode string, maxItems int, dir string, report provider.ProgressReporter) (provider.TaskOutput, error) {
	if strings.TrimSpace(t.douyinCommand) == "" || strings.TrimSpace(t.douyinConfig) == "" {
		return provider.TaskOutput{}, fmt.Errorf("未配置 douyin-downloader：请在设置页「外部下载器」填写启动命令与其 config.yml 路径（cookie 由该配置自管）")
	}
	userCfg, err := os.ReadFile(t.douyinConfig)
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("读取抖音下载器配置失败: %w", err)
	}

	tmpCfg := filepath.Join(dir, "voxbox-douyin.yml")
	var b strings.Builder
	b.Write(userCfg)
	if !strings.HasSuffix(string(userCfg), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# ---- voxbox 追加覆盖段（YAML 后键覆盖前键）----\n")
	fmt.Fprintf(&b, "link:\n- %s\n", link)
	fmt.Fprintf(&b, "path: %s\n", dir)
	b.WriteString("folderstyle: false\n")
	b.WriteString("database: false\n")
	fmt.Fprintf(&b, "mode: %s\n", douyinMode)
	if maxItems > 0 {
		fmt.Fprintf(&b, "number:\n  %s: %d\n", douyinMode, maxItems)
	}
	if err := os.WriteFile(tmpCfg, []byte(b.String()), 0o600); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入临时配置失败: %w", err)
	}

	args := append(strings.Fields(t.douyinCommand), "-c", tmpCfg)
	bin := args[0]
	args = args[1:]

	report(5, "启动 douyin-downloader…", nil)
	_, _, runErr := streamProcess(ctx, bin, args, nil)
	if runErr != nil {
		if ctx.Err() != nil {
			return provider.TaskOutput{}, ctx.Err()
		}
		return provider.TaskOutput{}, fmt.Errorf("douyin-downloader 执行失败: %v（注意：上游对单条/合集下载可能被抖音验证拦截，主页作品可用性以实际输出为准）", runErr)
	}
	report(99, "登记产物…", nil)
	return collectArtifacts(t.dataDir, dir)
}

// ---------------------------------------------------------------- 产物登记

// collectArtifacts 扫描下载目录登记产物：媒体/字幕文件 → artifacts；
// info.json 提供标题与时长（登记后删除，不进产物列表）。
func collectArtifacts(dataDir, dir string) (provider.TaskOutput, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("读取下载目录失败: %w", err)
	}

	// info.json 索引：yt-dlp 命名 <title> [<id>].<ext>.info.json，前缀与媒体文件一致。
	info := map[string]ytdlpInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".info.json") {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			var meta ytdlpInfo
			if json.Unmarshal(raw, &meta) == nil {
				info[strings.TrimSuffix(e.Name(), ".info.json")] = meta
			}
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	titles := []string{}
	arts := []provider.Artifact{}
	for _, name := range names {
		ext := strings.ToLower(filepath.Ext(name))
		kind, ok := mediaExtKinds[ext]
		if !ok {
			continue
		}
		abs := filepath.Join(dir, name)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			continue
		}
		rel, err := filepath.Rel(dataDir, abs)
		if err != nil {
			continue
		}
		art := provider.Artifact{
			Kind: kind, Path: rel, Format: strings.TrimPrefix(ext, "."), Size: st.Size(),
		}
		// info.json 命名 = 输出模板的 ext 槽位替换为 info.json（<base>.info.json），
		// 与媒体文件去掉扩展名后同名，按 base 匹配
		if meta, ok := info[strings.TrimSuffix(name, ext)]; ok {
			art.DurationMS = int64(meta.Duration * 1000)
			if meta.Title != "" && kind != "subtitle" && !contains(titles, meta.Title) {
				titles = append(titles, meta.Title)
			}
		}
		arts = append(arts, art)
	}
	if len(arts) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("下载目录没有可用产物（视频可能被上游拦截或格式不受支持）")
	}
	// --ignore-errors 下主媒体失败、仅字幕落盘也会 exit 0：只捞到字幕视为主媒体失败，
	// 不能把「没下到视频/音频」标成功
	hasMedia := false
	for _, a := range arts {
		if a.Kind == "audio" || a.Kind == "video" {
			hasMedia = true
			break
		}
	}
	if !hasMedia {
		return provider.TaskOutput{}, fmt.Errorf("主媒体下载失败（只落了 %d 条字幕）：站点限流或视频被拦截，可稍后重试", len(arts))
	}

	summary := map[string]any{}
	if len(titles) > 0 {
		summary["title"] = strings.Join(titles, " / ")
	}
	return provider.TaskOutput{Artifacts: arts, Summary: summary}, nil
}

type ytdlpInfo struct {
	Title    string `json:"title"`
	Duration int64  `json:"duration"` // 秒
	Uploader string `json:"uploader"`
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ResultTitle 从 info.json 采集的标题派生任务标题（合集取「 / 」拼接的首段）。
func (t *Tool) ResultTitle(summaryJSON string) string {
	var s struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(summaryJSON), &s); err != nil {
		return ""
	}
	if i := strings.Index(s.Title, " / "); i > 0 {
		return s.Title[:i]
	}
	return s.Title
}

// ---------------------------------------------------------------- 参数与子进程工具

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

func intParam(params map[string]any, key string) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return 0
}

// streamProcess 流式跑外部命令：onLine 逐行消费 stdout（进度解析），
// 返回 stdout/stderr 各自的尾部若干行（错误信息提取用）。命令随 ctx 取消。
func streamProcess(ctx context.Context, bin string, args []string, onLine func(string)) (outTail []string, errTail []string, err error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("启动 %s 失败: %w", bin, err)
	}

	var outMu, errMu sync.Mutex
	scan := func(r io.Reader, stdout bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if stdout {
				if onLine != nil {
					onLine(line)
				}
				outMu.Lock()
				outTail = append(outTail, line)
				if len(outTail) > 30 {
					outTail = outTail[len(outTail)-30:]
				}
				outMu.Unlock()
			} else {
				errMu.Lock()
				errTail = append(errTail, line)
				if len(errTail) > 30 {
					errTail = errTail[len(errTail)-30:]
				}
				errMu.Unlock()
			}
		}
	}
	done := make(chan struct{}, 2)
	go func() { scan(stdout, true); done <- struct{}{} }()
	go func() { scan(stderr, false); done <- struct{}{} }()
	<-done
	<-done
	runErr := cmd.Wait()
	return outTail, errTail, runErr
}

// ytdlpErrMessage 从 stdout/stderr 尾部提取错误信息：过滤下载进度噪声行、
// 单行截断（长路径会吃掉预算把真正的 ERROR 行挤没），优先带 ERROR 的行。
func ytdlpErrMessage(outTail, errTail []string, err error) string {
	clip := func(l string) string {
		l = strings.TrimSpace(l)
		if len([]rune(l)) > 120 {
			l = string([]rune(l)[:120]) + "…"
		}
		return l
	}
	var meaningful, errLines []string
	for _, l := range append(append([]string{}, errTail...), outTail...) {
		l = clip(l)
		if l == "" || l == "…" {
			continue
		}
		// 进度/速率行纯噪声（[download] 42.3% …）；[info] 落盘路径行同理
		if strings.HasPrefix(l, "[download]") && strings.Contains(l, "%") {
			continue
		}
		if strings.HasPrefix(l, "[info]") && strings.Contains(l, "Destination") {
			continue
		}
		meaningful = append(meaningful, l)
		if strings.Contains(l, "ERROR") || strings.Contains(l, "error:") {
			errLines = append(errLines, l)
		}
	}
	pick := meaningful
	if len(errLines) > 0 {
		pick = errLines
	}
	if len(pick) > 4 {
		pick = pick[len(pick)-4:]
	}
	out := strings.Join(pick, " | ")
	if out == "" {
		out = err.Error()
	}
	if len([]rune(out)) > 500 {
		out = string([]rune(out)[:500]) + "…"
	}
	return out
}

func firstMeaningful(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return string([]rune(s)[:max]) + "…"
	}
	return s
}
