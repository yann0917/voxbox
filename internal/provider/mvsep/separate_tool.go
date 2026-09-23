// MVSep 音频分离工具：本地文件（引擎上传通道）或公网 URL → MVSep 分离任务
// （上传/轮询）→ 多轨产物落盘。与火山分离工具（MediaKit）的关键差异：上游收
// multipart 文件上传而非 URL，本地文件无需对象存储中转；产物直链公开可下载。
package mvsep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// stagedStem 已落盘音轨的中转记录（saveOutputs 内部：.part → final 改名前持有元数据）。
type stagedStem struct {
	final, part string
	art         provider.Artifact
}

// 轮询节奏与总超时（var 便于测试注入）：MVSep 免费队列排队较深（高峰几十单），
// 总超时 30 分钟；超时/取消时上游任务仍在继续，额度不退（等待中的任务取消才退）。
var (
	pollInterval = 3 * time.Second
	pollMax      = 15 * time.Second
	toolTimeout  = 30 * time.Minute
)

// outputFormats separation/create 的 output_format 枚举（上游定义，非文件扩展名直译）。
var outputFormats = []struct {
	Value string
	Label string
}{
	{"0", "MP3"},
	{"1", "WAV (16bit)"},
	{"2", "WAV (24bit)"},
	{"3", "WAV (32bit float)"},
	{"4", "WAV (32bit)"},
	{"5", "FLAC"},
}

// SeparateTool MVSep 音频分离工具。
type SeparateTool struct {
	client *Client
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewSeparateTool(token, baseURL, outDir string) *SeparateTool {
	return &SeparateTool{client: New(token, baseURL), outDir: outDir}
}

func (t *SeparateTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "mvsep",
		Name:        "separate",
		Title:       "音频分离（MVSep）",
		Description: "MVSep 音乐源分离（人声/伴奏/鼓/贝斯等 120+ 算法，每日 50 次免费）：本地文件直传或公网 URL",
		Group:       "语音",
	}
}

func (t *SeparateTool) ParamSpecs() []provider.ParamSpec {
	// 算法列表是动态数据（120+ 且随上游更新），静态 ParamSpecs 无法穷举：
	// sep_type/add_opt1..3 走通用描述，Web 分离页经 /api/mvsep/algorithms 动态渲染表单。
	specs := []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString,
			Placeholder: "公网可访问的音频 URL；留空则使用上传的本地文件", Group: "输入"},
		{Key: "sep_type", Label: "分离类型", Type: provider.ParamString, Required: true, Group: "参数",
			Placeholder: "算法 render_id（GET /api/mvsep/algorithms 获取；如 26=Ensemble 人声/伴奏）"},
	}
	for i := 1; i <= 3; i++ {
		specs = append(specs, provider.ParamSpec{
			Key: fmt.Sprintf("add_opt%d", i), Label: fmt.Sprintf("附加选项 %d", i),
			Type: provider.ParamString, Group: "参数",
			Placeholder: "所选算法 algorithm_fields 定义，不需要则留空",
		})
	}
	formatOpts := make([]provider.ParamOption, 0, len(outputFormats))
	for _, f := range outputFormats {
		formatOpts = append(formatOpts, provider.ParamOption{Value: f.Value, Label: f.Label})
	}
	specs = append(specs, provider.ParamSpec{Key: "output_format", Label: "输出格式",
		Type: provider.ParamEnum, Default: "0", Group: "参数", Options: formatOpts})
	return specs
}

func (t *SeparateTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（凭证校验之前，与火山分离工具同序：参数错误不被凭证错误掩盖）。
	sepType, err := parseSepType(in.Params["sep_type"])
	if err != nil {
		return provider.TaskOutput{}, err
	}
	format, err := parseOutputFormat(in.Params["output_format"])
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if t.client == nil || t.client.token == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w: 未配置 MVSep API Token：请执行 voxbox config set mvsep.api_token 或在 Web 设置页配置", ErrNoCred)
	}

	src, cleanup, err := t.resolveInput(ctx, in, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	defer cleanup()

	// 分离结果缓存（对象存储可用时启用）：同曲同参数直接取回产物，不占每日免费额度。
	// URL 输入也先下载成临时文件（resolveInput），同样可哈希命中。缓存异常按未命中继续。
	addOpts := map[string]string{}
	for i := 1; i <= 3; i++ {
		if v := paramString(in.Params, fmt.Sprintf("add_opt%d", i)); v != "" {
			addOpts[fmt.Sprintf("add_opt%d", i)] = v
		}
	}
	srcBase := provider.SourceBase(src)
	hash, err := t.submit(ctx, src, sepType, format, in.Params, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(15, "分离任务已提交，等待云端处理", map[string]any{"hash": hash})

	res, err := t.poll(ctx, hash, report)
	if err != nil {
		// 取消/超时不撤销上游：等待中任务才可退额度，已开算的照常完成，结果经历史页可取。
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			bctx, bcancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer bcancel()
			_ = t.client.Cancel(bctx, hash)
		}
		return provider.TaskOutput{}, err
	}
	return t.saveOutputs(ctx, in, srcBase, res, report)
}

// resolveInput 归一输入为本地文件：本地上传通道（Files["audio"]）直接用；URL 先下载
// 到临时文件（MVSep 只收 multipart 上传）。返回 cleanup 关闭/删除临时资源。
func (t *SeparateTool) resolveInput(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (srcPath string, cleanup func(), err error) {
	if src := in.Files["audio"]; src != "" {
		return src, func() {}, nil
	}
	rawURL := strings.TrimSpace(paramString(in.Params, "url"))
	if rawURL == "" {
		return "", func() {}, fmt.Errorf("缺少输入：请上传本地音频文件，或提供公网可访问的音频 URL")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return "", func() {}, fmt.Errorf("音频 URL 须以 http(s):// 开头的公网可访问地址")
	}
	tmp, err := os.CreateTemp("", "mvsep-in-*"+extFromName(rawURL))
	if err != nil {
		return "", func() {}, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmp.Close()
	cleanup = func() { _ = os.Remove(tmp.Name()) }
	report(3, "正在下载输入音频…", nil)
	if _, err := t.client.DownloadToFile(ctx, rawURL, tmp.Name()); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tmp.Name(), cleanup, nil
}

// submit 上传并创建任务：上传期间每 5s 心跳（大文件提交进度不冻结）。
func (t *SeparateTool) submit(ctx context.Context, srcPath string, sepType, format int, params map[string]any, report provider.ProgressReporter) (string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("读取音频文件失败: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("读取音频文件信息失败: %w", err)
	}
	name := filepath.Base(srcPath)
	addOpts := map[string]string{}
	for i := 1; i <= 3; i++ {
		if v := paramString(params, fmt.Sprintf("add_opt%d", i)); v != "" {
			addOpts[fmt.Sprintf("add_opt%d", i)] = v
		}
	}
	report(3, fmt.Sprintf("正在上传音频到 MVSep（%.1f MB）…", float64(fi.Size())/1024/1024), nil)
	type submitResult struct {
		hash string
		err  error
	}
	done := make(chan submitResult, 1)
	go func() {
		hash, herr := t.client.Create(ctx, CreateInput{
			Filename: name, Reader: f, Size: fi.Size(), SepType: sepType,
			AddOpts: addOpts, OutputFormat: format,
		})
		done <- submitResult{hash: hash, err: herr}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case r := <-done:
			if r.err != nil {
				return "", r.err
			}
			if r.hash == "" {
				return "", fmt.Errorf("MVSep 提交响应缺少 hash")
			}
			return r.hash, nil
		case <-ctx.Done():
			return "", fmt.Errorf("提交已取消: %w", ctx.Err())
		case <-ticker.C:
			report(8, "仍在上传音频…", nil)
		}
	}
}

// poll 轮询任务直到终态：waiting/converting/processing/distributing/merging 继续
// （converting=服务端格式转换/解压/音轨初始化，正常过渡态，通常很快转入 queue 或
// processing）；done → 结果；failed/not_found → 终态错误。
func (t *SeparateTool) poll(ctx context.Context, hash string, report provider.ProgressReporter) (*SeparationResult, error) {
	interval := pollInterval
	polls := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, res, err := t.client.Get(ctx, hash)
		if err != nil {
			return nil, err
		}
		switch status {
		case "done":
			return res, nil
		case "waiting", "converting", "processing", "distributing", "merging":
		default:
			return nil, fmt.Errorf("MVSep 返回未知任务状态 %q", status)
		}
		polls++
		note := "云端处理中（" + status + "）…"
		if status == "waiting" {
			note = "云端排队中（免费队列高峰较慢）…"
		}
		if status == "converting" {
			// 官方语义：服务端正在格式转换/解压/音轨初始化，正常过渡态，通常很快转入 queue/processing
			note = "服务端格式转换/音轨准备中（converting）…"
		}
		report(min(90, 15+polls), note, map[string]any{"status": status})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		interval *= 2
		if interval > pollMax {
			interval = pollMax
		}
	}
}

// saveOutputs 逐轨下载落盘：先全部下成 <name>.part 再统一改名——单轨失败不落
// 半截产物（引擎承重契约），且大文件不全量驻留内存。
func (t *SeparateTool) saveOutputs(ctx context.Context, in provider.TaskInput, srcBase string, res *SeparationResult, report provider.ProgressReporter) (provider.TaskOutput, error) {
	n := len(res.Files)
	reqID := uuid.NewString()
	stagedPaths := make([]stagedStem, 0, n)
	cleanupAll := func() {
		for _, s := range stagedPaths {
			_ = os.Remove(s.part)
		}
	}
	for k, file := range res.Files {
		base := sanitizeName(file.Name)
		// 轨道名（type: Vocals/Instrum…）不含扩展名：格式从直链推断，产物名补全
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(base), "."))
		if ext == "" {
			ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(file.Link), "."))
		}
		if ext != "" && !strings.HasSuffix(base, "."+ext) {
			base += "." + ext
		}
		// 命名规则：separate/<reqID>/<源基名>_<上游名>——每任务独立子目录防碰撞，
		// 文件名带源信息（歌名），下载/历史不再出现裸 uuid 前缀
		artPath := filepath.Join("separate", reqID, srcBase+"_"+base)
		absPath := filepath.Join(t.outDir, artPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			cleanupAll()
			return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
		}
		part := absPath + ".part"
		report(90+5*k/n, fmt.Sprintf("下载产物 %d/%d：%s", k+1, n, file.Name), nil)
		if _, err := t.client.DownloadToFile(ctx, file.Link, part); err != nil {
			cleanupAll()
			return provider.TaskOutput{}, fmt.Errorf("下载产物 %s 失败: %w", file.Name, err)
		}
		fi, err := os.Stat(part)
		if err != nil {
			cleanupAll()
			return provider.TaskOutput{}, fmt.Errorf("读取产物 %s 失败: %w", file.Name, err)
		}
		stagedPaths = append(stagedPaths, stagedStem{
			final: absPath, part: part,
			art: provider.Artifact{
				Kind: "audio", Path: artPath, Format: ext,
				Size: fi.Size(), Meta: map[string]any{
					// 小写轨道键，与前端 TRACK_LABELS（vocals/instrum/other…）对齐
					"track": strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base))),
					// url 为上游产物直链（公开链接）：本地 path 是长期那份，公网分享用上游链接
					"url": file.Link,
				},
			},
		})
	}
	arts := make([]provider.Artifact, 0, n)
	tracks := make([]string, 0, n)
	for _, s := range stagedPaths {
		if err := os.Rename(s.part, s.final); err != nil {
			cleanupAll()
			return provider.TaskOutput{}, fmt.Errorf("写入产物失败: %w", err)
		}
		arts = append(arts, s.art)
		tracks = append(tracks, fmt.Sprint(s.art.Meta["track"]))
	}
	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"algorithm":     res.Algorithm,
			"output_format": res.OutputFormat,
			"tracks":        tracks,
		},
	}, nil
}

func parseSepType(v any) (int, error) {
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		s = ""
	}
	if s == "" {
		return 0, fmt.Errorf("缺少必填参数: sep_type")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("sep_type 须为正整数（算法 render_id），得到 %q", s)
	}
	return n, nil
}

func parseOutputFormat(v any) (int, error) {
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		s = ""
	}
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 5 {
		return 0, fmt.Errorf("output_format 仅支持 0-5（0=MP3 1/2/3/4=WAV 各位深 5=FLAC），得到 %q", s)
	}
	return n, nil
}

// extFromName 从 URL 或路径提取小写扩展名（含点；剔除查询串与锚点），无扩展名返回空串。
func extFromName(name string) string {
	name = name[strings.IndexByte(name, ':')+1:] // 防御 "https://" 冒号干扰
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(filepath.Ext(name))
}

// sanitizeName 上游文件名收敛为安全落盘名：保留字母数字与常用符号，其余折叠为下划线
// （filepath.Base 先剥目录，../ 逃逸天然失效）。
func sanitizeName(name string) string {
	base := filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			unicode.IsLetter(r), unicode.IsNumber(r), // 保留中文等（歌名命名必须可读）
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), " ._")
	if out == "" || out == "." || out == ".." {
		return "output"
	}
	return out
}

func paramString(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := params[key]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}
