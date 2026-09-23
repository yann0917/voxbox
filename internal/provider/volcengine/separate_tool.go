// 人声背景音分离工具：公网音视频 URL → AI MediaKit 分离任务（提交/轮询）→ 多轨音频转存落盘。
// 凭证体系独立于语音三件套（SpeechCred）：仅一个 MediaKit apiKey（Bearer 鉴权）。
package volcengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// 分离异步轮询节奏（var 便于测试注入更短间隔，同 asr 先例）与总超时：
// 分离是重计算任务，总超时 15 分钟（Run 内 context.WithTimeout 兜底，ctx 取消/超时优先返回）。
var (
	sepPollInterval = 2 * time.Second
	sepPollMax      = 30 * time.Second
	sepToolTimeout  = 15 * time.Minute
)

// SeparateTool 人声背景音分离工具（AI MediaKit）。
// 产物为多轨音频：Audio/Music 场景 2 轨（voice/background），Drama/Narrate 场景 3 轨（voice/music/sfx）。
type SeparateTool struct {
	client *MediaKitClient
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewSeparateTool(apiKey string, outDir string) *SeparateTool {
	return &SeparateTool{client: NewMediaKitClient(apiKey), outDir: outDir}
}

func (t *SeparateTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "separate",
		Title:       "人声背景音分离",
		Description: "从音视频分离人声与背景音（AI MediaKit）：公网 URL 或本地文件（对象存储中转）",
		Group:       "语音",
	}
}

func (t *SeparateTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		// url 非必填：本地文件 + 对象存储中转时由任务运行期补齐（ensureURLInput）。
		{Key: "url", Label: "音视频 URL", Type: provider.ParamString,
			Placeholder: "公网可访问的音视频 URL；留空则使用上传的本地文件（需配置对象存储）", Group: "输入"},
		{Key: "scene", Label: "分离场景", Type: provider.ParamEnum,
			Default: "Audio", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "Audio", Label: "通用（人声+背景）"},
				{Value: "Music", Label: "音乐（人声+伴奏）"},
				{Value: "Drama", Label: "短剧（人声+音乐+音效）"},
				{Value: "Narrate", Label: "口播（人声+音乐+音效）"},
			}},
		{Key: "output_format", Label: "输出格式", Type: provider.ParamEnum,
			Default: "mp3", Group: "参数",
			Options: []provider.ParamOption{
				{Value: "aac", Label: "AAC"}, {Value: "mp3", Label: "MP3"},
				{Value: "wav", Label: "WAV"}, {Value: "m4a", Label: "M4A"},
				{Value: "flac", Label: "FLAC"},
			}},
	}
}

func (t *SeparateTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（与 tts/asr/podcast 同序，M3 b964939 语义）：参数错误（退出码 2）
	// 不应被凭证校验（退出码 4）掩盖。凭证校验先于文件转存：无凭证不白传大文件。
	scene, err := parseSeparateScene(paramString(in.Params, "scene"))
	if err != nil {
		return provider.TaskOutput{}, err
	}
	format, err := parseSeparateFormat(paramString(in.Params, "output_format"))
	if err != nil {
		return provider.TaskOutput{}, err
	}

	// MediaKit 凭证独立于语音三件套（无 SpeechCred，不调 cred.Validate）。
	if t.client == nil || t.client.apiKey == "" {
		return provider.TaskOutput{}, fmt.Errorf("%w: 未配置 AI MediaKit API Key：请执行 voxbox config set volc.mediakit.api_key 或在 Web 设置页配置", ErrNoCred)
	}

	// 输入二选一：公网 URL，或本地文件（配置了对象存储时自动中转取签名 URL）。
	mediaURL, err := ensureURLInput(ctx, in, "url", "音视频",
		"缺少输入：请提供公网可访问的音视频 URL，或上传本地文件（需在设置页配置对象存储）", report)
	if err != nil {
		return provider.TaskOutput{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, sepToolTimeout)
	defer cancel()

	taskID, err := t.client.Submit(ctx, separateMediaField(mediaURL), mediaURL, scene, format)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(5, "分离任务已提交", map[string]any{"task_id": taskID})

	res, err := t.pollSeparate(ctx, taskID, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	return t.saveTracks(ctx, in, res, scene, format, report)
}

// pollSeparate 轮询分离任务：起步 sepPollInterval 指数退避至 sepPollMax；
// 总超时由 ctx（sepToolTimeout 兜底）约束。终态：completed → 结果（res 必非 nil，
// result 字段缺失时报「分离结果为空」）；failed → 任务终态而非传输错误，报「分离任务失败」不可重试。
func (t *SeparateTool) pollSeparate(ctx context.Context, taskID string, report provider.ProgressReporter) (*MediaKitResult, error) {
	timeoutErr := func(err error) error {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("等待人声分离结果超时，请稍后重试")
		}
		return fmt.Errorf("人声分离已取消: %w", err)
	}
	interval := sepPollInterval
	polls := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, timeoutErr(err)
		}
		status, res, errMsg, err := t.client.Query(ctx, taskID)
		if err != nil {
			return nil, err
		}
		switch status {
		case "completed":
			if res == nil || len(res.Tracks) == 0 {
				return nil, fmt.Errorf("分离结果为空")
			}
			return res, nil
		case "failed":
			return nil, fmt.Errorf("分离任务失败：%s", errMsg)
		}
		polls++
		report(min(90, 10+polls*5), "分离处理中…", nil)
		select {
		case <-ctx.Done():
			return nil, timeoutErr(ctx.Err())
		case <-time.After(interval):
		}
		interval *= 2
		if interval > sepPollMax {
			interval = sepPollMax
		}
	}
}

// saveTracks 逐轨下载转存并落盘（默认 separate/<uuid>_<track>.<ext>）。
// 先全部下载成功再写盘：单轨下载失败即整体报错，不落半截 artifacts（引擎承重契约）。
// _out 参数（CLI --out-dir 透传，机器约定不进 ParamSpecs）为输出目录重定向：
// 绝对路径直用；相对路径 Join(outDir) 后按 Rel 回算（产物 Path 保持相对 outDir），
// 文件名不变 <uuid>_<track>.<ext>（不再叠加 separate/ 前缀）。
func (t *SeparateTool) saveTracks(ctx context.Context, in provider.TaskInput, res *MediaKitResult, scene, format string, report provider.ProgressReporter) (provider.TaskOutput, error) {
	n := len(res.Tracks)
	payloads := make([][]byte, 0, n)
	for k, track := range res.Tracks {
		data, err := t.client.Download(ctx, track.URL)
		if err != nil {
			return provider.TaskOutput{}, fmt.Errorf("转存音轨 %s 失败: %w", track.Kind, err)
		}
		k++
		report(90+10*k/n, fmt.Sprintf("转存音轨 %d/%d", k, n), nil)
		payloads = append(payloads, data)
	}

	reqID := uuid.NewString()
	// 产物命名前缀：本地文件用其基名（歌名/原文件名）；URL 输入无语义名则只按轨道命名
	srcBase := ""
	if src := in.Files["audio"]; src != "" {
		srcBase = provider.SourceBase(src)
	}
	outParam, _ := in.Params["_out"].(string)
	arts := make([]provider.Artifact, 0, n)
	kinds := make([]string, 0, n)
	for k, track := range res.Tracks {
		name := track.Kind + "." + format
		if srcBase != "" {
			name = srcBase + "_" + name
		}
		var artPath, absPath string
		if outParam != "" {
			absPath = filepath.Join(outParam, name)
			artPath = absPath
			if !filepath.IsAbs(artPath) {
				absPath = filepath.Join(t.outDir, absPath)
				artPath, _ = filepath.Rel(t.outDir, absPath)
			}
		} else {
			// 每任务独立子目录防碰撞，文件名不再带 uuid 前缀
			artPath = filepath.Join("separate", reqID, name)
			absPath = filepath.Join(t.outDir, artPath)
		}
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
		}
		if err := os.WriteFile(absPath, payloads[k], 0o644); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("写入音轨 %s 失败: %w", track.Kind, err)
		}
		arts = append(arts, provider.Artifact{
			Kind: "audio", Path: artPath, Format: format,
			Size: int64(len(payloads[k])),
			// url 是上游产物地址（火山 TOS 签名 URL，24 小时有效）：转存到本地后 URL 会丢，
			// 落进 Meta 让 REST/CLI/MCP 都能拿到，便于直接引用。
			Meta: map[string]any{"track": track.Kind, "url": track.URL},
		})
		kinds = append(kinds, track.Kind)
	}
	// 分离成功后写回结果缓存（best-effort：缓存失败不影响任务产物）。
	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"scene":      scene,
			"duration_s": res.DurationS,
			"tracks":     kinds,
		},
	}, nil
}

// parseSeparateScene 校验分离场景（大小写不敏感，归一为上游枚举值；CLI 以小写 flag 透传）：
// 空 → 默认 Audio。
func parseSeparateScene(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "Audio", nil
	case "audio":
		return "Audio", nil
	case "music":
		return "Music", nil
	case "drama":
		return "Drama", nil
	case "narrate":
		return "Narrate", nil
	}
	return "", fmt.Errorf("暂不支持该分离场景 %s（支持 Audio/Music/Drama/Narrate）", s)
}

// parseSeparateFormat 校验输出格式（ext 原样透传上游，无需特殊映射）：空 → 默认 mp3。
func parseSeparateFormat(s string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(s))
	if f == "" {
		return "mp3", nil
	}
	switch f {
	case "aac", "mp3", "wav", "m4a", "flac":
		return f, nil
	}
	return "", fmt.Errorf("暂不支持该输出格式 %s（支持 aac/mp3/wav/m4a/flac）", s)
}

// separateMediaField 由 URL 扩展名推断提交字段（上游接口 video_url/audio_url 二选一）：
// 常见视频扩展名走 video_url，其余（音频/无扩展名）默认 audio_url；查询串/锚点先剔除。
func separateMediaField(rawURL string) string {
	u := rawURL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(u), ".")) {
	case "mp4", "mov", "avi", "mkv", "webm", "flv", "wmv", "m4v", "mpg", "mpeg", "3gp":
		return "video_url"
	}
	return "audio_url"
}
