package qianwen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// filetrans 轮询节奏（var 供测试注入）：起步 5s 指数退避至 30s，总超时 2h（上游 ≤12h，
// 常规文件分钟级完成，2h 未决视为异常留人工重试）。
var (
	asrPollInterval = 5 * time.Second
	asrPollMax      = 30 * time.Second
	asrPollTimeout  = 2 * time.Hour
)

const (
	asrModelQwen3     = "qwen3-asr-flash-filetrans"
	asrModelQwenAudio = "qwen-audio-3.1-asr-flash-filetrans"
)

// ASRTool 千问文件转写（filetrans）：URL 直用 / 本地文件经对象存储中转，异步提交轮询，
// 产出分句 txt + SRT（与 volcengine asr 产物同构）。
type ASRTool struct {
	client       *ASRClient
	apiKey       string
	outDir       string
	pollInterval time.Duration // 测试注入（0=免等待）
}

func NewASRTool(apiKey, outDir string) *ASRTool {
	return &ASRTool{client: NewASRClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir, pollInterval: asrPollInterval}
}

func (t *ASRTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "qianwen",
		Name:        "asr",
		Title:       "语音识别（千问）",
		Description: "qwen3-asr 文件转写，支持长音频（≤12h/2GB）、说话人分离与词级时间戳，输出分句文本与 SRT 字幕",
		Group:       "语音",
	}
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "url", Label: "音频 URL", Type: provider.ParamString, Group: "输入",
			Placeholder: "公网音频 URL；留空可配合本地文件（需配置对象存储）"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: asrModelQwen3, Group: "输入",
			Options: []provider.ParamOption{
				{Value: asrModelQwen3, Label: "qwen3-asr-flash-filetrans（推荐）"},
				{Value: asrModelQwenAudio, Label: "qwen-audio-3.1-asr-flash-filetrans（说话人分离更强）"},
			}},
		{Key: "language_hints", Label: "语言", Type: provider.ParamEnum, Default: "", Group: "输入",
			Options: []provider.ParamOption{
				{Value: "", Label: "自动识别"},
				{Value: "zh", Label: "中文"}, {Value: "en", Label: "英语"},
				{Value: "ja", Label: "日语"}, {Value: "ko", Label: "韩语"},
				{Value: "de", Label: "德语"}, {Value: "fr", Label: "法语"},
				{Value: "ru", Label: "俄语"}, {Value: "es", Label: "西班牙语"},
				{Value: "pt", Label: "葡萄牙语"}, {Value: "it", Label: "意大利语"},
			}},
		{Key: "diarization_enabled", Label: "说话人分离", Type: provider.ParamBool, Default: false, Group: "参数",
			Placeholder: "区分不同说话人（≤2h 且单声道音频）"},
		{Key: "enable_itn", Label: "数字规整（ITN）", Type: provider.ParamBool, Default: true, Group: "参数"},
		{Key: "enable_words", Label: "词级时间戳", Type: provider.ParamBool, Default: false, Group: "参数"},
		{Key: "srt", Label: "生成 SRT 字幕", Type: provider.ParamBool, Default: true, Group: "输出"},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	audioURL, err := provider.EnsureURLInput(ctx, in, "url", "音频",
		"缺少输入：千问识别需要音频 URL 或本地文件（对象存储中转）", report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置千问平台凭证，或 voxbox config set qianwen.api_key", ErrNoCred)
	}
	model := paramString(in.Params, "model")
	if model == "" {
		model = asrModelQwen3
	}
	lang := paramString(in.Params, "language_hints")
	var hints []string
	if lang != "" {
		hints = []string{lang}
	}
	itn := paramBool(in.Params, "enable_itn", true)
	words := paramBool(in.Params, "enable_words", false)

	report(10, "提交千问转写任务", nil)
	taskID, err := t.client.SubmitTranscription(ctx, model, audioURL, ASRParams{
		LanguageHints:      hints,
		DiarizationEnabled: paramBool(in.Params, "diarization_enabled", false),
		EnableITN:          &itn,
		EnableWords:        &words,
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(30, "任务已提交，等待转写", map[string]any{"task_id": taskID})

	// 轮询：指数退避，ctx 取消优先。
	deadline := time.Now().Add(asrPollTimeout)
	interval := t.pollInterval
	var task TranscriptionTask
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("识别已取消: %w", err)
		}
		if time.Now().After(deadline) {
			return provider.TaskOutput{}, fmt.Errorf("等待千问转写结果超时（2 小时），任务 %s 可能仍在处理，请稍后在历史页查看", taskID)
		}
		task, err = t.client.QueryTask(ctx, taskID)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		switch task.Status {
		case StatusSucceeded:
			if len(task.TranscriptionURLs) == 0 {
				return provider.TaskOutput{}, fmt.Errorf("转写成功但未返回结果地址（任务 %s）", taskID)
			}
			report(80, "拉取转写结果", nil)
			return t.saveArtifacts(ctx, in, task, model)
		case StatusFailed:
			msg := task.Message
			if msg == "" {
				msg = "上游未给出原因"
			}
			return provider.TaskOutput{}, fmt.Errorf("千问转写失败（任务 %s）: %s", taskID, msg)
		}
		report(30+min(40, n*2), "等待转写结果", map[string]any{"task_id": taskID, "poll": n, "status": task.Status})
		interval *= 2
		if interval > asrPollMax || interval < 0 {
			interval = asrPollMax
		}
		if interval > 0 { // pollInterval=0（测试注入）免等待
			select {
			case <-ctx.Done():
				return provider.TaskOutput{}, fmt.Errorf("识别已取消: %w", ctx.Err())
			case <-time.After(interval):
			}
		}
	}
}

// saveArtifacts txt + srt 落盘（与 volcengine asr 的 _out/相对路径语义一致），
// summary.segments 供前端文稿联动（含 speaker_id）。
func (t *ASRTool) saveArtifacts(ctx context.Context, in provider.TaskInput, task TranscriptionTask, model string) (provider.TaskOutput, error) {
	tr, err := t.client.FetchTranscription(ctx, task.TranscriptionURLs[0])
	if err != nil {
		return provider.TaskOutput{}, err
	}
	var sentences []TranscriptionSentence
	for _, ts := range tr.Transcripts {
		sentences = append(sentences, ts.Sentences...)
	}
	if len(sentences) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("转写结果为空（任务 %s）", task.TaskID)
	}

	var text strings.Builder
	var lastSpeaker string
	segs := make([]provider.SRTSegment, 0, len(sentences))
	segSummaries := make([]map[string]any, 0, len(sentences))
	for i, s := range sentences {
		if i > 0 {
			text.WriteByte('\n')
		}
		// 说话人切换时加标注（开启分离时 speaker_id 非空）
		if s.SpeakerID != "" && s.SpeakerID != lastSpeaker {
			fmt.Fprintf(&text, "[说话人 %s] ", s.SpeakerID)
			lastSpeaker = s.SpeakerID
		}
		text.WriteString(s.Text)
		segs = append(segs, provider.SRTSegment{StartMS: s.BeginTime, EndMS: s.EndTime, Text: s.Text})
		segSummaries = append(segSummaries, map[string]any{
			"text": s.Text, "start_ms": s.BeginTime, "end_ms": s.EndTime, "speaker_id": s.SpeakerID,
		})
	}

	reqID := uuid.NewString()
	txtPath := filepath.Join("asr", reqID+".txt")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		txtPath = outParam
	}
	srtPath := strings.TrimSuffix(txtPath, filepath.Ext(txtPath)) + ".srt"
	txtAbs, relTxt := resolveOut(t.outDir, txtPath)
	srtAbs, relSrt := resolveOut(t.outDir, srtPath)
	if err := os.MkdirAll(filepath.Dir(txtAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(txtAbs, []byte(text.String()), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入转写文本失败: %w", err)
	}
	arts := []provider.Artifact{{
		Kind: "transcript", Path: relTxt, Format: "txt",
		Size: int64(text.Len()), DurationMS: sentences[len(sentences)-1].EndTime,
	}}
	if paramBool(in.Params, "srt", true) {
		srtContent := provider.BuildSRT(segs)
		if err := os.WriteFile(srtAbs, []byte(srtContent), 0o644); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("写入 SRT 字幕失败: %w", err)
		}
		arts = append(arts, provider.Artifact{
			Kind: "subtitle", Path: relSrt, Format: "srt", Size: int64(len(srtContent)),
		})
	}
	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"segments":    segSummaries,
			"duration_ms": sentences[len(sentences)-1].EndTime,
			"model":       model,
			"source":      "url",
		},
	}, nil
}

// resolveOut 相对路径锚定 outDir 并回算相对形态；绝对 _out 仅当落在 outDir 内时
// 归一为相对展示路径，outDir 外保持绝对——相对化会回算出 ../ 逃逸路径，
// 破坏产物越界防护与 CLI --json 对绝对 path 的消费约定。
func resolveOut(outDir, p string) (abs, rel string) {
	outDir = filepath.Clean(outDir)
	abs = p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(outDir, p)
	}
	if !strings.HasPrefix(abs, outDir+string(filepath.Separator)) {
		return abs, abs
	}
	rel, _ = filepath.Rel(outDir, abs)
	return abs, rel
}

// paramBool 取布尔参数（缺失用默认；兼容字符串 "false"）。
func paramBool(params map[string]any, key string, def bool) bool {
	switch v := params[key].(type) {
	case bool:
		return v
	case string:
		if strings.EqualFold(v, "false") {
			return false
		}
		if strings.EqualFold(v, "true") {
			return true
		}
	}
	return def
}
