package zhipu

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/provider/audiotool"
)

// zhipuASRLimits 官方单次请求限制：wav/mp3，文件 ≤25MB，音频 ≤30 秒。
// 超限音频由服务端自动切分逐段转写后拼接：wav 纯 Go 按帧精确切分（边界吸附
// 静音区），mp3 走 ffmpeg 流拷贝分段（无 ffmpeg 时回落单次直传，交上游报错）。
// asrSeg* 为切分预算（var 便于测试注入）；asrInputCap 为整文件上限，
// 超出后分段请求数与内存都不划算，引导改用火山引擎录音文件通道。
const (
	zhipuASRMaxBytes = 25 << 20
	zhipuASRMaxSecs  = 30
	asrInputCap      = 200 << 20
)

var (
	zhipuASRSegSecs    = 29.0
	zhipuASRMp3SegSecs = 28.0
	zhipuASRSegBytes   = 24 << 20
)

// ASRTool 智谱语音识别（glm-asr-2512）：multipart 直传，同步返回转写文本。
// 无时间戳（不产 SRT）；30 秒上限定位为「短音频/一句话」级转写。
type ASRTool struct {
	client *ASRClient
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewASRTool(apiKey, outDir string) *ASRTool {
	return &ASRTool{client: NewASRClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *ASRTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "zhipu",
		Name:        "asr",
		Title:       "语音识别（智谱）",
		Description: "glm-asr-2512 同步转写（wav/mp3，超 30 秒/25MB 自动分段转写），多语言，支持热词与上下文；输出纯文本",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL（wav/mp3）；留空可配合本地文件直传（无需对象存储）"},
		{Key: "prompt", Label: "上下文", Type: provider.ParamText, Group: "参数",
			Placeholder: "长文本场景提供之前的转录结果作为上下文（建议 <8000 字）"},
		{Key: "hotwords", Label: "热词", Type: provider.ParamString, Group: "参数",
			Placeholder: "逗号分隔，提升领域词汇识别率（≤100 个）"},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	audioPath, source, err := t.resolveInput(ctx, in)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if source == "url" {
		defer os.Remove(audioPath) // URL 下载的临时文件用完即清；本地文件不动
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置智谱 API Key，或 voxbox config set zhipu.api_key", ErrNoCred)
	}
	if fi, statErr := os.Stat(audioPath); statErr == nil && fi.Size() > asrInputCap {
		return provider.TaskOutput{}, fmt.Errorf("音频过大（%.0fMB）：智谱同步转写支持 200MB 内自动分段，更长录音请改用火山引擎识别（最长 5 小时）",
			float64(fi.Size())/1024/1024)
	}
	// 官方仅收 wav/mp3：按扩展名白名单拦截（魔数校验交上游，避免误杀非标头文件）
	ext := strings.ToLower(filepath.Ext(audioPath))
	if ext != ".wav" && ext != ".mp3" {
		return provider.TaskOutput{}, fmt.Errorf("参数错误：智谱识别仅支持 wav / mp3 音频（当前 %s）", ext)
	}

	report(15, "检查音频规格", nil)
	text, totalMS, segs, err := t.transcribeAuto(ctx, audioPath, ext,
		paramString(in.Params, "prompt"), splitHotwords(paramString(in.Params, "hotwords")), report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(80, "保存转写文本", nil)

	reqID := uuid.NewString()
	relPath := filepath.Join("asr", reqID+".txt")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, []byte(text), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入转写文本失败: %w", err)
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "transcript", Path: relPath, Format: "txt",
			Size:       int64(len(text)),
			DurationMS: totalMS,
		}},
		Summary: map[string]any{
			"char_count":  utf8.RuneCountInString(text),
			"model":       "glm-asr-2512",
			"source":      source,
			"segment_num": segs,
			"duration_ms": totalMS,
		},
	}, nil
}

// transcribeAuto 按官方单次限制自动决定「直传」或「切分逐段转写」：
//   - wav：纯 Go 解析头部得精确时长，超预算按帧切分（边界吸附静音区），
//     解析失败的非常规文件回落 mp3 通道；
//   - mp3（及 wav 回落）：ffprobe 探测时长判定，超预算由 ffmpeg 流拷贝分段；
//     ffprobe 缺失时回落单次直传（与既有行为一致，交上游报时长超限）；
//   - 逐段转写以上一段结果的尾部作为 prompt 上下文，缓解切断处的语义断层。
//
// 返回（拼接文本, 总时长毫秒, 分段数）。
func (t *ASRTool) transcribeAuto(ctx context.Context, path, ext, prompt string, hotwords []string, report provider.ProgressReporter) (string, int64, int, error) {
	if ext == ".wav" {
		if data, err := os.ReadFile(path); err == nil {
			if w, perr := provider.ParseWAV(data); perr == nil {
				if w.Duration() > zhipuASRSegSecs || len(data) > zhipuASRMaxBytes {
					return t.transcribeSegmentsWAV(ctx, w, prompt, hotwords, report)
				}
				text, err := t.client.Transcribe(ctx, path, prompt, hotwords)
				return text, int64(w.Duration() * 1000), 1, err
			}
		}
	}

	// mp3（或 wav 解析失败）：探测驱动。≤25MB 且探测不可用时回落单次直传
	//（ffprobe 缺失/非常规文件都交上游裁决，与既有行为一致）；超 25MB 必须分段。
	overSize := false
	if fi, statErr := os.Stat(path); statErr == nil {
		overSize = fi.Size() > zhipuASRMaxBytes
	}
	info, perr := audiotool.ProbeAudio(ctx, path)
	if perr != nil {
		if overSize {
			if lookErr := audiotool.EnsureFFmpeg(); lookErr != nil {
				return "", 0, 0, fmt.Errorf("音频超过 25MB 且无法自动分段：%w", lookErr)
			}
			return "", 0, 0, fmt.Errorf("音频超过 25MB，自动分段需要先探测音频信息: %w", perr)
		}
		text, err := t.client.Transcribe(ctx, path, prompt, hotwords)
		return text, 0, 1, err
	}
	if !overSize && info.Duration <= zhipuASRMp3SegSecs {
		text, err := t.client.Transcribe(ctx, path, prompt, hotwords)
		return text, int64(info.Duration * 1000), 1, err
	}
	segSec := zhipuASRMp3SegSecs
	if info.BitRate > 0 {
		if bySec := float64(zhipuASRSegBytes) / float64(info.BitRate); bySec < segSec {
			segSec = bySec
		}
	}
	if err := audiotool.EnsureFFmpeg(); err != nil {
		return "", 0, 0, fmt.Errorf("音频超过单次转写限制（30 秒/25MB），自动分段需要 ffmpeg：%w", err)
	}
	dir, parts, err := audiotool.SegmentAudioFile(ctx, path, segSec, nil)
	if err != nil {
		return "", 0, 0, err
	}
	defer os.RemoveAll(dir)
	text, segs, err := t.transcribeParts(ctx, parts, prompt, hotwords, report)
	return text, int64(info.Duration * 1000), segs, err
}

// transcribeSegmentsWAV 切分已解析的 WAV 并逐段转写（纯 Go 路径，无外部依赖）。
func (t *ASRTool) transcribeSegmentsWAV(ctx context.Context, w *provider.WAV, prompt string, hotwords []string, report provider.ProgressReporter) (string, int64, int, error) {
	segs := w.Split(zhipuASRSegSecs, zhipuASRSegBytes, true)
	dir, err := os.MkdirTemp("", "zhipu-asr-seg-")
	if err != nil {
		return "", 0, 0, fmt.Errorf("创建分段临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)
	parts := make([]string, 0, len(segs))
	for i, seg := range segs {
		p := filepath.Join(dir, fmt.Sprintf("seg-%03d.wav", i))
		if err := os.WriteFile(p, seg, 0o644); err != nil {
			return "", 0, 0, fmt.Errorf("写入分段音频失败: %w", err)
		}
		parts = append(parts, p)
	}
	text, n, err := t.transcribeParts(ctx, parts, prompt, hotwords, report)
	return text, int64(w.Duration() * 1000), n, err
}

// transcribeParts 逐段转写并拼接：段间以上一段文本尾部作 prompt 上下文。
func (t *ASRTool) transcribeParts(ctx context.Context, parts []string, prompt string, hotwords []string, report provider.ProgressReporter) (string, int, error) {
	var acc strings.Builder
	for i, part := range parts {
		if report != nil {
			report(20+60*i/len(parts), fmt.Sprintf("正在转写第 %d/%d 段", i+1, len(parts)), nil)
		}
		segPrompt := prompt
		if i > 0 && acc.Len() > 0 {
			segPrompt = tailRunes(acc.String(), 4000)
		}
		text, err := t.client.Transcribe(ctx, part, segPrompt, hotwords)
		if err != nil {
			return "", 0, err
		}
		acc.WriteString(text)
	}
	return acc.String(), len(parts), nil
}

// tailRunes 取字符串末尾至多 n 个 rune。
func tailRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}

// resolveInput 解析音频文件输入：Files["audio"] 本地文件直用；否则取 params.url 下载到
// 临时文件后转传（transcriptions 仅收 multipart，无对象存储中转需求）。
// 返回音频本地路径与来源标记。
func (t *ASRTool) resolveInput(ctx context.Context, in provider.TaskInput) (string, string, error) {
	if src := in.Files["audio"]; src != "" {
		if _, err := os.Stat(src); err != nil {
			return "", "", fmt.Errorf("读取音频文件失败: %w", err)
		}
		return src, "file", nil
	}
	u := strings.TrimSpace(paramString(in.Params, "url"))
	if u == "" {
		return "", "", fmt.Errorf("缺少必填参数: 音频（智谱识别需要音频 URL 或本地文件）")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", "", fmt.Errorf("参数错误：音频 URL 仅支持 http(s):// 公网地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	// 不附带 Authorization：目标是用户自己的公网地址，不外泄凭证（与 xiaomi 同理）
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("下载音频失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("下载音频失败: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "zhipu-asr-*"+strings.ToLower(filepath.Ext(u)))
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		os.Remove(tmp.Name())
		return "", "", fmt.Errorf("下载音频失败: %w", err)
	}
	return tmp.Name(), "url", nil
}

// splitHotwords 逗号分隔热词 → 去空切片。
func splitHotwords(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
