// 播客工具：text/url/script 三选一输入 → 双人播客音频 + 对话稿 JSON 落盘。
// 生成走 PodcastClient（v3 WS，逐轮进度经 onRound 透传 report）。
package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

const (
	// podToolTimeout 单次播客生成总超时：任务数分钟级（含断点续传），30 分钟兜底。
	podToolTimeout = 30 * time.Minute
	// podDownloadTimeout audio_url 兜底下载超时（链接 1 小时有效，仅兜底路径使用）。
	podDownloadTimeout = 10 * time.Minute
)

// PodcastTool 语音播客工具（火山播客 v3 WS）。
// 产物为播客音频（361 分片拼接为主、363 audio_url 兜底下载）与对话稿 JSON。
type PodcastTool struct {
	client *PodcastClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewPodcastTool(cred SpeechCred, outDir string) *PodcastTool {
	return &PodcastTool{client: NewPodcastClient(cred), cred: cred, outDir: outDir}
}

func (t *PodcastTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "podcast",
		Title:       "语音播客",
		Description: "文本/网页/对话稿生成双人播客，逐轮进度推送",
		Group:       "语音",
	}
}

func (t *PodcastTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "input_text", Label: "文本", Type: provider.ParamText,
			Placeholder: "播客主题或长文本（≤12000 字）", Group: "内容"},
		{Key: "url", Label: "网页链接", Type: provider.ParamString,
			Placeholder: "网页链接，与文本二选一", Group: "内容"},
		// script 为 JSON 原文字符串（M4 Task 4 定案：CLI 读文件内容 / Web 前端直传文本），Files 不参与。
		{Key: "script", Label: "对话稿", Type: provider.ParamText,
			Placeholder: "对话稿 JSON 原文", Group: "内容"},
		{Key: "speakers", Label: "音色", Type: provider.ParamString, Required: true,
			Placeholder: "两个音色 ID，逗号分隔", Group: "参数"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum,
			Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "mp3", Label: "MP3"}, {Value: "ogg_opus", Label: "OGG Opus"},
				{Value: "pcm", Label: "PCM"}, {Value: "aac", Label: "AAC"},
			}},
		{Key: "head_music", Label: "开头音乐", Type: provider.ParamBool, Default: false, Group: "参数"},
	}
}

func (t *PodcastTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（M3 Task 9 定案）：输入三选一、speakers、对话稿、格式均为参数错误（退出码 2），
	// 不应被凭证校验（退出码 4）掩盖。
	inputText, url, script := paramString(in.Params, "input_text"), paramString(in.Params, "url"), paramString(in.Params, "script")
	provided := 0
	for _, v := range []string{inputText, url, script} {
		if v != "" {
			provided++
		}
	}
	if provided == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少播客内容输入：请提供文本、网页链接或对话稿")
	}
	if provided > 1 {
		return provider.TaskOutput{}, fmt.Errorf("播客输入只能提供其一（文本/网页/对话稿）")
	}
	speakers, err := parsePodSpeakers(paramString(in.Params, "speakers"))
	if err != nil {
		return provider.TaskOutput{}, err
	}
	var turns []PodcastTurn
	if script != "" {
		if turns, err = parsePodScript(script); err != nil {
			return provider.TaskOutput{}, err
		}
	}
	format := paramString(in.Params, "format")
	if format == "" {
		format = "mp3"
	}
	switch format {
	case "mp3", "ogg_opus", "pcm", "aac":
	default:
		return provider.TaskOutput{}, fmt.Errorf("暂不支持该音频格式（支持 mp3/ogg_opus/pcm/aac）")
	}

	if err := t.cred.ValidatePodcast(); err != nil {
		return provider.TaskOutput{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, podToolTimeout)
	defer cancel()

	// onRound 在 Generate 调用方 goroutine 同步回调，计数无需加锁（逐轮进度实时上抛）。
	roundsDone := 0
	res, err := t.client.Generate(ctx, PodcastRequest{
		InputText: inputText,
		URL:       url,
		Script:    turns,
		Speakers:  speakers,
		Format:    format,
		HeadMusic: podBoolParam(in.Params["head_music"], false),
	}, func(r PodcastRound) {
		roundsDone++
		report(min(95, roundsDone*15), fmt.Sprintf("第 %d 轮对话（说话人 %.12s…）", r.RoundID, r.Speaker), map[string]any{
			"round_id": r.RoundID, "speaker": r.Speaker, "text": r.Text, "rounds_done": roundsDone,
		})
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(90, "保存播客产物", nil)

	// 音频以 361 分片拼接为准；分片为零字节且 363 带 audio_url 时兜底下载转存（防 1 小时失效）。
	fallback := false
	audio := res.Audio
	if len(audio) == 0 && res.AudioURL != "" {
		report(85, "音频分片为空，转存 audio_url", nil)
		audio, err = downloadPodAudio(ctx, res.AudioURL)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		fallback = true
	}
	if len(audio) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("播客生成失败：未收到音频内容")
	}

	return t.saveArtifacts(in, audio, format, res, speakers, fallback)
}

// saveArtifacts 落盘播客音频（podcast/<uuid>.<ext>）与对话稿 JSON（podcast/<uuid>.json），
// 并按 M2 契约处理 _out 重定向：音频路径重定向后，dialog 路径跟随仅换扩展名。
func (t *PodcastTool) saveArtifacts(in provider.TaskInput, audio []byte, format string, res PodcastResult, speakers [2]string, fallback bool) (provider.TaskOutput, error) {
	reqID := uuid.NewString()
	audioPath := filepath.Join("podcast", reqID+"."+extOf(format))
	// _out 参数（CLI --out）重定向产物路径；不进 ParamSpecs，属机器约定。
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		audioPath = outParam
	}
	// dialog 路径跟随音频路径：仅换扩展名（_out 为绝对路径时 dialog 同为绝对；相对时在相对段上替换）。
	dialogPath := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".json"

	audioAbs := audioPath
	if !filepath.IsAbs(audioAbs) {
		audioAbs = filepath.Join(t.outDir, audioPath)
		audioPath, _ = filepath.Rel(t.outDir, audioAbs)
	}
	dialogAbs := dialogPath
	if !filepath.IsAbs(dialogAbs) {
		dialogAbs = filepath.Join(t.outDir, dialogPath)
		dialogPath, _ = filepath.Rel(t.outDir, dialogAbs)
	}
	if err := os.MkdirAll(filepath.Dir(audioAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(audioAbs, audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入播客音频失败: %w", err)
	}

	rounds := make([]podDialogRound, 0, len(res.Rounds))
	var totalDur float64
	for _, r := range res.Rounds {
		rounds = append(rounds, podDialogRound{RoundID: r.RoundID, Speaker: r.Speaker, Text: r.Text, DurationS: r.DurationS})
		totalDur += r.DurationS
	}
	dialogBytes, err := json.Marshal(podDialogJSON{Rounds: rounds})
	if err != nil {
		return provider.TaskOutput{}, fmt.Errorf("序列化对话稿失败: %w", err)
	}
	if err := os.WriteFile(dialogAbs, dialogBytes, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入对话稿失败: %w", err)
	}

	summary := map[string]any{
		"rounds":             len(rounds),
		"duration_s":         totalDur,
		"speakers":           []string{speakers[0], speakers[1]},
		"audio_url_fallback": fallback,
	}
	if len(res.Usage) > 0 {
		summary["usage"] = res.Usage
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{
			{Kind: "audio", Path: audioPath, Format: format, Size: int64(len(audio))},
			{Kind: "dialog", Path: dialogPath, Format: "json", Size: int64(len(dialogBytes))},
		},
		Summary: summary,
	}, nil
}

// podDialogJSON 对话稿产物结构：360/362 事件收集的实际播报内容与时长。
type podDialogJSON struct {
	Rounds []podDialogRound `json:"rounds"`
}

type podDialogRound struct {
	RoundID   int     `json:"round_id"`
	Speaker   string  `json:"speaker"`
	Text      string  `json:"text"`
	DurationS float64 `json:"duration_s"`
}

// podScriptFile 对话稿 JSON 原文结构：{"rounds":[{"speaker":"音色ID","text":"..."}]}。
type podScriptFile struct {
	Rounds []struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	} `json:"rounds"`
}

// parsePodScript 解析对话稿 JSON 原文（script 参数类型为 text，非文件）：
// JSON 非法 / rounds 为空 / 轮次缺 speaker 或 text 均报「对话稿格式错误：…」（CLI 映射参数错误退出码 2）。
func parsePodScript(s string) ([]PodcastTurn, error) {
	var f podScriptFile
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		return nil, fmt.Errorf("对话稿格式错误：JSON 解析失败（%v）", err)
	}
	if len(f.Rounds) == 0 {
		return nil, fmt.Errorf("对话稿格式错误：rounds 为空")
	}
	turns := make([]PodcastTurn, 0, len(f.Rounds))
	for i, r := range f.Rounds {
		if r.Speaker == "" || r.Text == "" {
			return nil, fmt.Errorf("对话稿格式错误：第 %d 轮缺少 speaker 或 text", i+1)
		}
		turns = append(turns, PodcastTurn{Speaker: r.Speaker, Text: r.Text})
	}
	return turns, nil
}

// parsePodSpeakers 解析逗号分隔的音色参数：分段 TrimSpace 后取非空项，须恰好 2 个（顺序即 A/B 说话人）。
func parsePodSpeakers(s string) ([2]string, error) {
	var out [2]string
	n := 0
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		if n >= 2 {
			return out, fmt.Errorf("speakers 需要恰好 2 个音色 ID（逗号分隔，voxbox voices list 查询）")
		}
		out[n] = part
		n++
	}
	if n != 2 {
		return out, fmt.Errorf("speakers 需要恰好 2 个音色 ID（逗号分隔，voxbox voices list 查询）")
	}
	return out, nil
}

// podBoolParam 读布尔参数（兼容 bool 与字符串 "true"/"false"，其余/缺省回 def，同 asrSRTEnabled 风格）。
func podBoolParam(v any, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return def
}

// downloadPodAudio 兜底下载 363 事件的 audio_url 转存（podDownloadTimeout 超时）。
func downloadPodAudio(ctx context.Context, audioURL string) ([]byte, error) {
	resp, err := resty.New().SetTimeout(podDownloadTimeout).R().SetContext(ctx).Get(audioURL)
	if err != nil {
		return nil, fmt.Errorf("下载播客音频失败: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("下载播客音频失败(HTTP %d)", resp.StatusCode())
	}
	return resp.Body(), nil
}
