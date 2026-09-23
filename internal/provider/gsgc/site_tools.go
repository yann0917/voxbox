// 站点功能族：格式工厂在线版（z.pcgeshi.com）的全部任务类功能，与 gsgc.separate
// 共用同一套协议（client.go：fetch_upload_url → 直传站点 TOS → create_task →
// batchGet → fetch_download_url），即「一套工具」。转换猫线路（zhuanhuanmao.com）
// 复用同一执行器，仅注册人声分离（zhmSeparateFunc，见 register.go）。
//
// 覆盖范围说明：站点网页端共 11 个功能（含图片），其中人声分离为独立的
// gsgc.separate（有 sep-cache 与 stem 归一等定制）；此处注册其余 10 个。
package gsgc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// siteLine 站点线路：转换猫/格式工厂同一后端的两块牌子，协议与接口路径完全一致，
// 仅 API host、TOS 上传前缀与分离任务的 stem 参数形态不同。
// name = 注册表 provider 名 = 产物子目录；title 用于进度/摘要文案。
type siteLine struct {
	name    string
	title   string
	baseURL string
}

var (
	lineGSGC = siteLine{name: "gsgc", title: "格式工厂", baseURL: DefaultBaseURL}
	lineZHM  = siteLine{name: "zhuanhuanmao", title: "转换猫", baseURL: ZHMBaseURL}
)

// siteFunc 站点功能定义：注册名/展示信息/task_type/参数表。
type siteFunc struct {
	Kind     string // 注册名（= 站点 URL slug）
	Title    string
	Group    string // 视频 | 音频 | 图片
	TaskType string // create_task 的 task_type
	Desc     string
	Specs    []provider.ParamSpec // 参数表：Key 即站点参数键，值原样透传
	// Defaults 参数缺省值：调用方未传的键由此补齐（如分离的 model:103——实测
	// 缺失会导致上游静默退化单轨）。用户显式传值时覆盖。
	Defaults map[string]any
	// Transform payload 后处理钩子（可选）：处理无法用「键=值」表达的参数形状，
	// 如分离的 stems 枚举 → stem 数组（官方顺序）。
	Transform func(payload map[string]any)
}

// enumSpec 站点枚举参数快捷构造。
func enumSpec(key, label string, values ...string) provider.ParamSpec {
	opts := make([]provider.ParamOption, 0, len(values))
	for _, v := range values {
		opts = append(opts, provider.ParamOption{Value: v, Label: v})
	}
	return provider.ParamSpec{Key: key, Label: label, Type: provider.ParamEnum, Options: opts, Group: "参数"}
}

func withDefault(sp provider.ParamSpec, def any) provider.ParamSpec {
	sp.Default = def
	return sp
}

func strSpec(key, label, placeholder string) provider.ParamSpec {
	return provider.ParamSpec{Key: key, Label: label, Type: provider.ParamString, Placeholder: placeholder, Group: "参数"}
}

func intSpec(key, label string) provider.ParamSpec {
	return provider.ParamSpec{Key: key, Label: label, Type: provider.ParamInt, Group: "参数"}
}

func floatSpec(key, label string) provider.ParamSpec {
	return provider.ParamSpec{Key: key, Label: label, Type: provider.ParamFloat, Group: "参数"}
}

// 站点枚举值（来自站点前端资源模块，与官网下拉一致）。
var (
	siteVideoContainers = []string{"mp4", "mov", "mkv", "webm", "avi", "flv", "m4v", "mpg", "mpeg", "wmv"}
	siteAudioFormats    = []string{"mp3", "wav", "flac", "m4a", "aac", "ogg", "wma"}
	siteImageFormats    = []string{"jpg", "png", "webp", "bmp", "gif", "tiff"}
)

// siteFuncs 站点功能表（11 项，人声分离与其它功能同表同工具，无特殊化）。
var siteFuncs = []siteFunc{
	{
		Kind: "separate", TaskType: "audio_separate",
		Title: "人声伴奏分离", Group: "音频",
		Desc:     "人声/伴奏分离（站点云端执行），产物标准 MP3",
		Defaults: map[string]any{"model": DefaultModel},
		Specs: []provider.ParamSpec{
			withDefault(enumSpec("stems", "提取轨道", "both", "vocals", "instrumental"), "both"),
			strSpec("model", "模型编号", "站点私有编号，默认 103"),
		},
		Transform: separateStemsTransform,
	},
	{
		Kind: "video-format-convert", TaskType: "video_converter",
		Title: "视频格式转换", Group: "视频",
		Desc: "视频容器/编码转换（站点云端执行），可选分辨率/帧率/码率",
		Specs: []provider.ParamSpec{
			enumSpec("output_format", "目标格式", siteVideoContainers...),
			strSpec("video_codec", "视频编码", "如 libx264；copy=直接复制"),
			strSpec("video_resolution", "分辨率", "如 1280x720"),
			strSpec("video_bitrate", "视频码率", "如 2M"),
			floatSpec("video_framerate", "帧率"),
			strSpec("audio_bitrate", "音频码率", "如 192k"),
		},
	},
	{
		Kind: "video-compression", TaskType: "video_compress",
		Title: "视频压缩", Group: "视频",
		Desc: "站点云端视频压缩，按压缩率（1-99）控制体积",
		Specs: []provider.ParamSpec{
			intSpec("compress_rate", "压缩率（1-99）"),
			strSpec("video_compress_mode", "压缩模式", "站点私有取值，留空走默认"),
		},
	},
	{
		Kind: "video-extract-audio", TaskType: "audio_converter",
		Title: "视频转音频", Group: "视频",
		Desc: "提取视频音轨为音频文件（站点云端执行）",
		Specs: []provider.ParamSpec{
			enumSpec("output_format", "音频格式", "mp3", "m4a", "aac", "wav", "flac", "ogg"),
			strSpec("audio_bitrate", "音频码率", "如 192k"),
			intSpec("audio_samplerate", "采样率"),
			intSpec("audio_channel", "声道数"),
		},
	},
	{
		Kind: "video-volume-adjust", TaskType: "video_process",
		Title: "视频音量调节", Group: "视频",
		Desc: "调整视频音量倍数（站点云端执行）",
		Specs: []provider.ParamSpec{
			floatSpec("volume", "音量倍数（如 2=放大一倍）"),
			enumSpec("output_format", "输出格式", "mp4", "mov", "mkv", "webm"),
		},
	},
	{
		Kind: "video-speed", TaskType: "video_process",
		Title: "视频变速", Group: "视频",
		Desc: "视频 0.1-10 倍速（站点云端执行）",
		Specs: []provider.ParamSpec{
			floatSpec("speed", "速度倍数（0.1-10）"),
			enumSpec("output_format", "输出格式", "mp4", "mov", "mkv", "webm"),
		},
	},
	{
		Kind: "audio-format-convert", TaskType: "audio_converter",
		Title: "音频格式转换", Group: "音频",
		Desc: "音频格式互转（站点云端执行），可选码率/采样率/声道",
		Specs: []provider.ParamSpec{
			enumSpec("output_format", "目标格式", siteAudioFormats...),
			strSpec("audio_bitrate", "音频码率", "如 192k"),
			intSpec("audio_samplerate", "采样率"),
			intSpec("audio_channel", "声道数"),
		},
	},
	{
		Kind: "audio-compression", TaskType: "audio_compress",
		Title: "音频压缩", Group: "音频",
		Desc: "站点云端音频压缩，按压缩率（1-99）控制体积",
		Specs: []provider.ParamSpec{
			intSpec("compress_rate", "压缩率（1-99）"),
		},
	},
	{
		Kind: "audio-denoise", TaskType: "audio_denoise",
		Title: "音频降噪", Group: "音频",
		Desc: "站点云端 RNNoise 降噪；model 留空走站点默认",
		Specs: []provider.ParamSpec{
			strSpec("model", "降噪模型", "站点私有取值，留空走默认"),
		},
	},
	{
		Kind: "image-format-convert", TaskType: "image_converter",
		Title: "图片格式转换", Group: "图片",
		Desc: "图片格式互转（站点云端执行），可选限长边",
		Specs: []provider.ParamSpec{
			enumSpec("output_format", "目标格式", siteImageFormats...),
			strSpec("image_resolution", "长边限制", "如 1920"),
			strSpec("image_resolution_mode", "分辨率模式", "站点私有取值，留空走默认"),
		},
	},
	{
		Kind: "image-compression", TaskType: "image_compress",
		Title: "图片压缩", Group: "图片",
		Desc: "站点云端图片压缩，按压缩率（1-99）控制体积",
		Specs: []provider.ParamSpec{
			intSpec("compress_rate", "压缩率（1-99）"),
			strSpec("image_compress_mode", "压缩模式", "站点私有取值，留空走默认"),
		},
	},
}

// separateStemsTransform 分离任务的 stems 枚举 → 站点 stem 数组：
// 双轨必须按官方顺序 ["instrumental","vocals"] 提交（实测乱序或缺 model 时上游
// 静默退化成只出人声单轨）。
func separateStemsTransform(payload map[string]any) {
	v, _ := payload["stems"].(string)
	var stem []string
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "vocals", "vocal":
		stem = []string{StemVocals}
	case "instrumental", "instrum", "accompaniment":
		stem = []string{StemInstrumental}
	default: // "" / both
		stem = []string{StemInstrumental, StemVocals}
	}
	delete(payload, "stems")
	payload["stem"] = stem
}

// zhmSeparateFunc 转换猫线路的分离定义：与其前端 chunk 一致——stem 为字符串
// （"instrumental_vocals"/"vocals"/"instrumental"）且**不带 model**，与格式工厂的
// 数组+model:"103" 形态不同（两线不可共用 Defaults/Transform，见 client.go 包注释）。
var zhmSeparateFunc = siteFunc{
	Kind: "separate", TaskType: "audio_separate",
	Title: "人声伴奏分离", Group: "音频",
	Desc: "人声/伴奏分离（转换猫线路，站点云端执行），产物标准 MP3",
	Specs: []provider.ParamSpec{
		withDefault(enumSpec("stems", "提取轨道", "both", "vocals", "instrumental"), "both"),
	},
	Transform: zhmStemsTransform,
}

// zhmStemsTransform 转换猫线路的 stems 枚举 → 站点 stem 字符串（官方前端取值原样）。
func zhmStemsTransform(payload map[string]any) {
	v, _ := payload["stems"].(string)
	var stem string
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "vocals", "vocal":
		stem = StemVocals
	case "instrumental", "instrum", "accompaniment":
		stem = StemInstrumental
	default: // "" / both
		stem = "instrumental_vocals"
	}
	delete(payload, "stems")
	payload["stem"] = stem
}

// 轮询节奏与未知态容忍（与 client.go 的云端协议共用）。
var (
	pollInterval    = 3 * time.Second
	pollMax         = 15 * time.Second
	maxUnknownPolls = 10
)

// uploadContentType 按扩展名给直传 Content-Type（上游未校验，常见音频给准确值）。
func uploadContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg":
		return "audio/ogg"
	}
	return "application/octet-stream"
}

// extFromName 从 URL 或路径提取小写扩展名（含点；剔除查询串与锚点）。
func extFromName(name string) string {
	name = name[strings.IndexByte(name, ':')+1:] // 防御 "https://" 冒号干扰
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(filepath.Ext(name))
}

func anyToString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	if v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

// ---- 通用站点工具 ----

// siteTool 站点功能的通用执行器（与 gsgc.separate 同协议：fetch_upload_url →
// 直传站点 TOS → create_task → batchGet → fetch_download_url），按线路（line）
// 区分转换猫/格式工厂。
type siteTool struct {
	fn     siteFunc
	client *Client
	outDir string
	line   siteLine
}

func newSiteTool(line siteLine, fn siteFunc, outDir string) *siteTool {
	return &siteTool{fn: fn, client: NewForSite(line.baseURL), outDir: outDir, line: line}
}

func (t *siteTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    t.line.name,
		Name:        t.fn.Kind,
		Title:       t.fn.Title,
		Description: t.fn.Desc,
		Group:       t.fn.Group,
	}
}

// ParamSpecs 键即站点参数键，Web 表单值原样进 payload。
func (t *siteTool) ParamSpecs() []provider.ParamSpec { return t.fn.Specs }

func (t *siteTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 输入二选一：本地上传（Files["audio"]）或 URL 参数（下载成临时文件再直传）
	src, cleanup, err := t.resolveInput(ctx, in, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	defer cleanup()

	payload, err := t.buildPayload(in.Params)
	if err != nil {
		return provider.TaskOutput{}, err
	}

	// 直传站点自己的 TOS（fetch_upload_url 预签名 PUT，与站点前端同一步骤）
	f, err := os.Open(src)
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("读取音频文件失败: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("读取音频文件信息失败: %w", err)
	}
	// 源文件基名（歌名/原文件名）：产物命名用，站点返回的直链名是无语义哈希
	srcBase := provider.SourceBase(src)
	report(3, fmt.Sprintf("正在直传到%s云端（%.1f MB）…", t.line.title, float64(fi.Size())/1024/1024), nil)
	uploadURL, inputPathID, err := t.client.FetchUploadURL(ctx, filepath.Base(src))
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if err := t.client.UploadFile(ctx, uploadURL, uploadContentType(filepath.Base(src)), f, fi.Size()); err != nil {
		return provider.TaskOutput{}, err
	}
	payload["input_path_id"] = []string{inputPathID}

	taskID, err := t.client.CreateTask(ctx, payload)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(20, "任务已提交，等待站点云端处理", map[string]any{"task_id": taskID})

	if err := t.poll(ctx, taskID, report); err != nil {
		return provider.TaskOutput{}, err
	}
	return t.saveOutputs(ctx, taskID, srcBase, report)
}

// resolveInput URL 输入先下载成临时文件（上游只收文件直传）。
func (t *siteTool) resolveInput(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (string, func(), error) {
	if src := in.Files["audio"]; src != "" {
		return src, func() {}, nil
	}
	rawURL := strings.TrimSpace(anyToString(in.Params["url"]))
	if rawURL == "" {
		return "", func() {}, fmt.Errorf("缺少输入：请上传本地文件，或提供公网可访问的 URL")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", func() {}, fmt.Errorf("URL 须以 http(s):// 开头")
	}
	tmp, err := os.CreateTemp("", "gsgc-site-*"+extFromName(rawURL))
	if err != nil {
		return "", func() {}, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp.Close()
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	report(3, "正在下载输入文件…", nil)
	if _, err := t.client.DownloadToFile(ctx, rawURL, tmp.Name()); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tmp.Name(), cleanup, nil
}

// buildPayload 表单值 → 站点 payload：键=站点参数键；跳过空值；output_format 统一小写；
// compress_rate 钳制 1-99（与站点前端一致）。
func (t *siteTool) buildPayload(params map[string]any) (map[string]any, error) {
	payload := map[string]any{"task_type": t.fn.TaskType}
	for k, v := range t.fn.Defaults { // 缺省兜底：用户显式传值在下方覆盖
		payload[k] = v
	}
	clean := CleanParams(params)
	delete(clean, "url")
	for _, sp := range t.fn.Specs {
		v, ok := clean[sp.Key]
		if !ok {
			continue
		}
		switch sp.Type {
		case provider.ParamInt:
			n, ok := toInt(v)
			if !ok {
				return nil, fmt.Errorf("参数 %s 须为整数", sp.Key)
			}
			if sp.Key == "compress_rate" {
				n = min(max(n, 1), 99)
			}
			payload[sp.Key] = n
		case provider.ParamFloat:
			f, ok := toFloat(v)
			if !ok {
				return nil, fmt.Errorf("参数 %s 须为数字", sp.Key)
			}
			payload[sp.Key] = f
		default:
			s := anyToString(v)
			if sp.Key == "output_format" {
				s = strings.ToLower(strings.TrimPrefix(s, "."))
			}
			payload[sp.Key] = s
		}
	}
	if t.fn.Transform != nil {
		t.fn.Transform(payload)
	}
	return payload, nil
}

// poll 轮询任务直到终态（与分离任务同词表：running → completed；未知态容忍）。
func (t *siteTool) poll(ctx context.Context, taskID string, report provider.ProgressReporter) error {
	interval, polls, unknown := pollInterval, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tasks, err := t.client.BatchGet(ctx, taskID)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			return fmt.Errorf("站点查询任务 %s 无返回记录", taskID)
		}
		status := strings.ToLower(strings.TrimSpace(tasks[0].Status))
		switch status {
		case "completed", "success", "done", "finished":
			return nil
		case "failed", "error", "canceled", "cancelled":
			return fmt.Errorf("站点任务失败（status=%s）", status)
		case "running", "pending", "queued", "waiting", "processing":
			unknown = 0
		default:
			unknown++
			if unknown > maxUnknownPolls {
				return fmt.Errorf("站点返回未知任务状态 %q", status)
			}
		}
		polls++
		report(min(85, 20+polls), "站点云端处理中…", map[string]any{"status": status})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		interval *= 2
		if interval > pollMax {
			interval = pollMax
		}
	}
}

// saveOutputs 取产物直链并下载落盘。非分离任务产物 info 可能为 null
// （实测 audio_converter 如此），轨道名从直链文件名兜底。
func (t *siteTool) saveOutputs(ctx context.Context, taskID, srcBase string, report provider.ProgressReporter) (provider.TaskOutput, error) {
	var warnings []string
	files, err := t.client.FetchDownloadURL(ctx, taskID)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	n := len(files)
	reqID := uuid.NewString()
	arts := make([]provider.Artifact, 0, n)
	tracks := make([]string, 0, n)
	for k, file := range files {
		// 直链带 TOS 签名 query：先剥掉再取文件名/扩展名（Ext 对裸 URL 会错抓 query 里的点）
		base := file.URL[strings.LastIndex(file.URL, "/")+1:]
		if i := strings.IndexAny(base, "?#"); i >= 0 {
			base = base[:i]
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(base), "."))
		if ext == "" {
			ext = "bin"
		}
		// 命名规则：<源文件基名>_<轨道>.<ext>（分离有 stem）；非分离直接用源基名。
		// 站点直链名是无语义哈希，必须在这里修复成可读名，下载/历史才不用手动改名。
		artName := srcBase
		if stem := strings.TrimSpace(file.Stem); stem != "" {
			artName += "_" + stem
		} else if len(files) > 1 {
			artName += fmt.Sprintf("_%d", k+1)
		}
		artName = sanitizeSrcBase(artName)
		artPath := filepath.Join(t.line.name, reqID, artName+"."+ext)
		absPath := filepath.Join(t.outDir, artPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
		}
		report(88+8*k/max(n, 1), fmt.Sprintf("下载产物 %d/%d", k+1, n), nil)
		size, err := t.client.DownloadToFile(ctx, file.URL, absPath)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		meta := map[string]any{"source": t.line.name + "-site", "task_type": t.fn.TaskType}
		if file.Stem != "" {
			// 分离任务产物带 stem（vocals/instrumental），与人声分离页轨道标签对齐
			meta["track"] = file.Stem
		}
		// 分离产物后处理：站点固定出 WAV，检查编码/码率后转成标准 MP3 128k
		//（ffmpeg 缺失或探测失败时保留 WAV 降级，警告进 summary）。
		if t.fn.TaskType == "audio_separate" {
			report(min(95, 88+8*(k+1)/max(n, 1)), fmt.Sprintf("检查 %s 并转码标准 MP3…", artName), nil)
			finalAbs, format, transcoded, srcBitrate, warn := ensureStemMP3(ctx, absPath)
			if warn != "" {
				warnings = append(warnings, artName+": "+warn)
			}
			if rel, rerr := filepath.Rel(t.outDir, finalAbs); rerr == nil {
				artPath = rel
			} else {
				artPath = finalAbs
			}
			absPath = finalAbs
			if fi, serr := os.Stat(absPath); serr == nil {
				size = fi.Size()
			}
			meta["transcoded"] = transcoded
			if srcBitrate > 0 {
				meta["source_bitrate"] = srcBitrate
			}
			ext = format
		}
		arts = append(arts, provider.Artifact{
			Kind:   ArtifactKindOf(file.URL),
			Path:   artPath,
			Format: ext,
			Size:   size,
			Meta:   meta,
		})
		tracks = append(tracks, artName)
	}
	report(100, "处理完成", nil)
	summary := map[string]any{
		"engine":    t.line.name + "-site",
		"task_type": t.fn.TaskType,
		"task_id":   taskID,
		"tracks":    tracks,
	}
	if len(warnings) > 0 {
		summary["warnings"] = warnings
	}
	if t.fn.TaskType == "audio_separate" {
		summary["post_process"] = "mp3 128k"
	}
	return provider.TaskOutput{
		Artifacts: arts,
		Summary:   summary,
	}, nil
}

func fileSizeOf(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// sanitizeSrcBase 源文件基名收敛为安全落盘名：保留中文等 Unicode 字母/数字与
// ._ -() 空格，其余折叠下划线（产物名要可读，不能像旧 sanitizeName 那样吃掉中文）。
func sanitizeSrcBase(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		keep := r == '.' || r == '-' || r == '_' || r == ' ' || r == '(' || r == ')' ||
			unicode.IsLetter(r) || unicode.IsNumber(r)
		if !keep {
			r = '_'
		}
		if r == '_' && lastUnderscore {
			continue
		}
		b.WriteRune(r)
		lastUnderscore = r == '_'
	}
	out := strings.Trim(b.String(), " ._")
	if out == "" {
		out = "audio"
	}
	return out
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// OrderedInputs 按约定键序收集上传文件：Files["audio"], ["audio2"], …。
// 站点功能全部单输入，多于一个直接报错。
func OrderedInputs(files map[string]string) ([]string, error) {
	first := files["audio"]
	if first == "" {
		return nil, fmt.Errorf("缺少输入：请上传要处理的文件")
	}
	out := []string{first}
	for i := 2; ; i++ {
		p, ok := files[fmt.Sprintf("audio%d", i)]
		if !ok || p == "" {
			break
		}
		out = append(out, p)
	}
	return out, nil
}

// CleanParams 剔除空串参数（Web 表单未填的字段按未设置处理，
// 否则空字符串会被上游参数解码拒绝或误当成显式值）。
func CleanParams(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// ArtifactKindOf 按产物扩展名归类（工具集页面按 kind 选渲染器：音频/视频/图片）。
func ArtifactKindOf(name string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "png", "jpg", "jpeg", "webp", "bmp", "gif", "tif", "tiff":
		return "image"
	case "mp4", "mkv", "webm", "mov", "avi", "flv", "ts":
		return "video"
	}
	return "audio"
}
