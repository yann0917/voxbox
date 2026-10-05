package minimax

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/pronunciation"
	"github.com/yann0917/voxbox/internal/provider"
)

// 长文本合成轮询节奏（var 便于测试注入更短间隔）：起步间隔指数退避、退避上限、总超时。
// 官方未给合成时长 SLA，5 万字符按分钟级估算，超时放宽到 1 小时兜底；查询接口限
// 每秒 10 次，退避节奏远低于该阈值。
var (
	ttsLongPollInterval = 2 * time.Second
	ttsLongPollMax      = 15 * time.Second
	ttsLongPollTimeout  = time.Hour
)

const (
	// ttsLongMaxLength 单次提交文本上限（官方：最长 5 万字符）。
	ttsLongMaxLength = 50000
	// ttsLongMaxQueryErrors 轮询期间容忍的连续查询失败次数（长任务对瞬时网络抖动更敏感）。
	ttsLongMaxQueryErrors = 3
)

// TTSLongTool MiniMax 异步长文本语音合成（t2a_async_v2 创建 + 轮询 + retrieve_content 下载）。
// 与同步 tts 工具并存：单次 5 万字符直出单文件（无服务端分段拼接），适合书/文章级合成。
type TTSLongTool struct {
	client *TTSLongClient
	apiKey string
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTTSLongTool(apiKey, outDir string) *TTSLongTool {
	return &TTSLongTool{client: NewTTSLongClient(apiKey, BaseURL), apiKey: apiKey, outDir: outDir}
}

func (t *TTSLongTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "minimax",
		Name:        "tts_long",
		Title:       "长文本语音合成（MiniMax）",
		Description: "5 万字以内长文本异步合成（speech-2.8），提交后轮询任务状态，完成后取回音频文件",
		Group:       "语音",
	}
}

// ttsLongFormats 音频格式枚举（异步单文件直出，无拼接诉求）。
var ttsLongFormats = []provider.ParamOption{
	{Value: "mp3", Label: "MP3"}, {Value: "wav", Label: "WAV"}, {Value: "flac", Label: "FLAC"},
}

func (t *TTSLongTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "文本", Type: provider.ParamText, Required: true,
			Placeholder: "输入要合成的长文本（≤50000 字符）", Group: "内容"},
		{Key: "model", Label: "模型", Type: provider.ParamEnum, Default: ModelTTSHD, Group: "参数",
			Options: []provider.ParamOption{
				{Value: ModelTTSHD, Label: "speech-2.8-hd（高清）"},
				{Value: ModelTTSTurbo, Label: "speech-2.8-turbo（提速降本）"},
			}},
		{Key: "voice", Label: "音色", Type: provider.ParamString, Default: DefaultVoice, Group: "参数",
			Placeholder: "音色 ID，完整列表见 Web 语音合成页下拉（GET /api/voices?provider=minimax）"},
		{Key: "format", Label: "音频格式", Type: provider.ParamEnum, Default: "mp3", Group: "参数",
			Options: ttsLongFormats},
		{Key: "speed", Label: "语速", Type: provider.ParamFloat, Group: "参数",
			Placeholder: "0.5-2.0，默认 1.0"},
		{Key: "volume", Label: "音量", Type: provider.ParamFloat, Group: "参数",
			Placeholder: "(0, 10]，默认 1.0"},
		{Key: "pitch", Label: "音调", Type: provider.ParamInt, Group: "参数",
			Placeholder: "-12 到 12，默认 0"},
		{Key: "emotion", Label: "情绪", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: ttsEmotions},
		{Key: "language_boost", Label: "语种增强", Type: provider.ParamEnum, Default: "", Group: "参数",
			Options: ttsLanguageBoosts},
	}
}

func (t *TTSLongTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	text := pronunciation.Apply(paramString(in.Params, "text"), "")
	if utf8.RuneCountInString(text) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	if utf8.RuneCountInString(text) > ttsLongMaxLength {
		return provider.TaskOutput{}, fmt.Errorf("文本超出长度限制：单次最多 %d 字符（当前 %d 字符）",
			ttsLongMaxLength, utf8.RuneCountInString(text))
	}
	if t.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w：请在设置页「云端服务」配置 MiniMax API Key，或 voxbox config set minimax.api_key", ErrNoCred)
	}
	voice := paramString(in.Params, "voice")
	if voice == "" {
		voice = DefaultVoice
	}
	model := paramString(in.Params, "model")
	format := paramString(in.Params, "format")
	if format == "" {
		format = "mp3"
	}
	speed, volume := paramFloat(in.Params, "speed"), paramFloat(in.Params, "volume")
	pitch := int(paramFloat(in.Params, "pitch"))
	emotion := paramString(in.Params, "emotion")
	languageBoost := paramString(in.Params, "language_boost")

	report(10, "提交长文本合成任务", nil)
	taskID, err := t.client.Create(ctx, TTSLongCreateReq{
		Text: text, Voice: voice, Model: model, Format: format,
		Speed: speed, Volume: volume, Pitch: pitch,
		Emotion: emotion, LanguageBoost: languageBoost,
	})
	if err != nil {
		return provider.TaskOutput{}, err
	}

	// 轮询任务状态：指数退避；容忍瞬时查询失败（连续超限才终止）
	interval := ttsLongPollInterval
	start := time.Now()
	queryErrors := 0
	for {
		if ctx.Err() != nil {
			return provider.TaskOutput{}, fmt.Errorf("任务 %s 已取消（官方查询接口可续查结果）: %w", taskID, ctx.Err())
		}
		if time.Since(start) > ttsLongPollTimeout {
			return provider.TaskOutput{}, fmt.Errorf("任务 %s 超过 %.0f 分钟未完成，已放弃等待", taskID, ttsLongPollTimeout.Minutes())
		}
		time.Sleep(interval)
		if interval < ttsLongPollMax {
			interval *= 2
			if interval > ttsLongPollMax {
				interval = ttsLongPollMax
			}
		}
		resp, err := t.client.Query(ctx, taskID)
		if err != nil {
			queryErrors++
			if queryErrors > ttsLongMaxQueryErrors {
				return provider.TaskOutput{}, fmt.Errorf("查询任务 %s 状态失败: %w", taskID, err)
			}
			continue
		}
		queryErrors = 0
		switch resp.Status {
		case "success":
			report(80, "下载合成音频", nil)
			audio, err := t.client.Download(ctx, resp.FileID)
			if err != nil {
				return provider.TaskOutput{}, err
			}
			return t.save(in, audio, format, map[string]any{
				"char_count": utf8.RuneCountInString(text),
				"model":      effectiveModel(model),
				"voice":      voice,
				"task_id":    taskID,
			})
		case "failed":
			return provider.TaskOutput{}, fmt.Errorf("MiniMax 长文本合成任务失败（task_id %s）%s", taskID, baseRespSuffix(resp.BaseResp))
		case "expired":
			return provider.TaskOutput{}, fmt.Errorf("MiniMax 长文本合成任务已过期（task_id %s）", taskID)
		default: // processing 及未知状态继续等（进度随等待时长缓涨，60 分钟封顶前最多走到 75）
			elapsed := time.Since(start).Seconds()
			progress := 15 + int(elapsed/60.0)
			if progress > 75 {
				progress = 75
			}
			report(progress, fmt.Sprintf("任务处理中（%s）", taskID), nil)
		}
	}
}

// save 落盘音频产物：_out 可重定向；扩展名跟随请求 format（zip 抽取出的音频以实际
// 内容为准的场景极少，仍按请求格式落盘——hex/zip 嗅探已在上游归一为单音频字节）。
func (t *TTSLongTool) save(in provider.TaskInput, audio []byte, format string, summary map[string]any) (provider.TaskOutput, error) {
	reqID := uuid.NewString()
	relPath := filepath.Join("tts", reqID+"."+format)
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		relPath = outParam
	}
	absPath, relPath := resolveOut(t.outDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(absPath, audio, 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入音频文件失败: %w", err)
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{{
			Kind: "audio", Path: relPath, Format: format,
			Size: int64(len(audio)),
		}},
		Summary: summary,
	}, nil
}

// baseRespSuffix 任务失败时附带的官方状态详情（可为空）。
func baseRespSuffix(b baseResp) string {
	if strings.TrimSpace(b.StatusMsg) == "" {
		return ""
	}
	return fmt.Sprintf("：%s", b.StatusMsg)
}
