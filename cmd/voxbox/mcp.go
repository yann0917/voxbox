package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/service"
	"github.com/yann0917/voxbox/internal/store"
)

// MCP server：把语音能力以 stdio MCP 工具暴露给 AI Agent（Claude Code 等）。
// 复用 CLI 的执行核心 runToolCore，输出与 `--json` 同一契约（docs/json-contract.md）。
// 客户端配置见 docs/mcp.md。

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "启动 MCP server（stdio），供 AI Agent 调用全部能力",
		Long: `以 Model Context Protocol stdio 模式运行 voxbox，暴露 voxbox_* 工具族：
语音合成（同步/长文本/流式）、语音识别、播客、人声分离、机器翻译、语音妙记与音色查询。
凭证复用 ~/.voxbox/config.yaml；工具输出与 CLI --json 同一契约。
配置示例（Claude Code）：
  {"mcpServers": {"voxbox": {"command": "/path/to/voxbox", "args": ["mcp"]}}}`,
		RunE: func(c *cobra.Command, args []string) error {
			return runMCP(c.Context())
		},
	}
}

// mcpRunner 串行执行工具调用：sqlite 写入按单路处理，避免 MCP 客户端并发
// 调用触发 database is locked。代价是分钟级任务（妙记/播客）会阻塞后续调用。
type mcpRunner struct {
	svc *service.Service
	mu  sync.Mutex
}

func (r *mcpRunner) run(ctx context.Context, providerName, toolName string, params map[string]any, files map[string]string) (jsonResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return runToolCore(ctx, r.svc, providerName, toolName, params, files)
}

func runMCP(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	svc, err := service.New(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()
	svc.StartEngine(nil, 2) // stdio 下进度事件无消费者，不订阅
	eprintf("voxbox MCP server 已就绪（stdio）\n")
	return newMCPServer(svc).Run(ctx, &mcp.StdioTransport{})
}

// newMCPServer 组装 MCP Server（stdio 子进程与 serve 内嵌 HTTP 共用同一工具集）。
// 任务经 mcpRunner 串行执行（防 SQLite 并发写），与 Web 任务共享同一引擎并发槽。
func newMCPServer(svc *service.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "voxbox", Version: version}, nil)
	addMCPTools(server, &mcpRunner{svc: svc})
	return server
}

func addMCPTools(server *mcp.Server, r *mcpRunner) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_tts",
		Description: "语音合成：短文本转语音（同步秒级）。计费按字符数。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpTTSIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, jsonResult{}, fmt.Errorf("text 必填")
		}
		params := map[string]any{"text": in.Text}
		if in.Voice != "" {
			params["voice"] = in.Voice
		}
		if in.Format != "" {
			params["format"] = in.Format
		}
		if in.SpeedRatio != 0 {
			params["speed_ratio"] = in.SpeedRatio
		}
		if in.VolumeRatio != 0 {
			params["volume_ratio"] = in.VolumeRatio
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "volcengine", "tts", params, nil)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_tts_long",
		Description: "长文本语音合成：≤10 万字，有声书级异步合成，耗时时长正相关。timestamps=true 额外产出 SRT 字幕。计费按字符数。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpTTSLongIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, jsonResult{}, fmt.Errorf("text 必填")
		}
		params := map[string]any{"text": in.Text}
		if in.Voice != "" {
			params["voice"] = in.Voice
		}
		if in.Format != "" {
			params["format"] = in.Format
		}
		applySharedTTS(&in.SharedTTSOpts, params)
		if in.Timestamps {
			params["timestamps"] = true
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "volcengine", "tts_long", params, nil)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_tts_stream",
		Description: "流式语音合成：低延迟，20 语种 8 方言；context_text 传语音指令（如「用粤语说」「特别愤怒」），subtitle=true 产出字级字幕 SRT。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpTTSStreamIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, jsonResult{}, fmt.Errorf("text 必填")
		}
		params := map[string]any{"text": in.Text}
		if in.Voice != "" {
			params["voice"] = in.Voice
		}
		if in.Format != "" {
			params["format"] = in.Format
		}
		applySharedTTS(&in.SharedTTSOpts, params)
		if in.SilenceDuration != 0 {
			params["silence_duration"] = in.SilenceDuration
		}
		if in.Subtitle {
			params["subtitle"] = true
		}
		if in.AigcWatermark {
			params["aigc_watermark"] = true
		}
		if in.ToneFidelity {
			params["tone_fidelity"] = true
		}
		if in.ExplicitDialect != "" {
			params["explicit_dialect"] = in.ExplicitDialect
		}
		if in.ContextText != "" {
			params["context_text"] = in.ContextText
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "volcengine", "tts_stream", params, nil)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_asr",
		Description: "语音识别：本地音频 path 走一句话识别（同步秒级）；公网 URL 走录音文件识别，version=standard 异步 / idle 闲时低价 24h 内 / flash 极速秒级。输出分句时间戳与 SRT 字幕。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpASRIn) (*mcp.CallToolResult, jsonResult, error) {
		if (in.Path == "") == (in.URL == "") {
			return nil, jsonResult{}, fmt.Errorf("path 与 url 恰好提供其一")
		}
		version := in.Version
		if version == "" {
			if in.Path != "" {
				version = "sentence"
			} else {
				version = "standard"
			}
		}
		if in.Path != "" && version != "sentence" {
			return nil, jsonResult{}, fmt.Errorf("标准版/闲时版/极速版仅支持 URL 输入；本地文件请用 version=sentence（一句话识别）")
		}
		if in.URL != "" && version == "sentence" {
			return nil, jsonResult{}, fmt.Errorf("一句话识别仅支持本地音频文件；URL 请用 version=standard / idle / flash")
		}
		srt := true
		if in.Srt != nil {
			srt = *in.Srt
		}
		params := map[string]any{"srt": srt, "version": version, "language": in.Language}
		if in.Hotwords != "" {
			params["hotwords"] = in.Hotwords
		}
		var files map[string]string
		if in.Path != "" {
			if _, err := os.Stat(in.Path); err != nil {
				return nil, jsonResult{}, fmt.Errorf("音频文件不存在: %s", in.Path)
			}
			files = map[string]string{"audio": in.Path}
		} else {
			params["url"] = in.URL
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "volcengine", "asr", params, files)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_podcast",
		Description: "播客生成：主题文本（text）/长文本/网页 URL（url）/对话稿文件（script）生成双人对话播客音频。speakers 必填恰好 2 个音色 ID（先用 voxbox_voices 查询）。分钟级耗时，计费按字符。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpPodcastIn) (*mcp.CallToolResult, jsonResult, error) {
		params := map[string]any{}
		switch {
		case in.Script != "":
			raw, err := os.ReadFile(in.Script)
			if err != nil {
				return nil, jsonResult{}, fmt.Errorf("读取对话稿失败: %w", err)
			}
			params["script"] = string(raw)
		case in.URL != "":
			params["url"] = in.URL
		case in.Text != "":
			params["input_text"] = in.Text
		default:
			return nil, jsonResult{}, fmt.Errorf("text / url / script 必须提供其一")
		}
		if strings.Count(in.Speakers, ",") != 1 {
			return nil, jsonResult{}, fmt.Errorf("speakers 需要恰好 2 个音色 ID，逗号分隔（顺序为说话人 A、B）")
		}
		params["speakers"] = in.Speakers
		if in.Format != "" {
			params["format"] = in.Format
		}
		if in.HeadMusic {
			params["head_music"] = true
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "volcengine", "podcast", params, nil)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "voxbox_separate",
		Description: "人声/伴奏分离，四引擎。" +
			"engine=mvsep（默认，120+ 算法、每日 50 次免费、本地文件直传无需对象存储）——sep_type 缺省 48（MelBand Roformer，免费档质量最优），" +
			"MVSep 付费算法（如 26=Ensemble）在免费账号会报「unavailable until you purchase premium」。" +
			"engine=gsgc（格式工厂在线版，免费、无需凭证、匿名直连，stems 双轨/单轨，产物自动转码标准 MP3 128k，无需对象存储）——上游为站点私有接口，作为备用通道。" +
			"engine=zhuanhuanmao（转换猫，与格式工厂同后端的镜像线路，免费无需凭证，参数同 gsgc）。" +
			"engine=mediakit（火山 AI MediaKit，计费，需公网 URL 或已配对象存储）——scene=audio|music 双轨，drama|narrate 三轨。" +
			"输入 url 与 file 二选一。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpSeparateIn) (*mcp.CallToolResult, jsonResult, error) {
		if (in.URL == "") == (in.File == "") {
			return nil, jsonResult{}, fmt.Errorf("url 与 file 恰好提供其一")
		}
		var files map[string]string
		if in.File != "" {
			if _, err := os.Stat(in.File); err != nil {
				return nil, jsonResult{}, fmt.Errorf("音频文件不存在: %s", in.File)
			}
			files = map[string]string{"audio": in.File}
		}
		// 与 CLI separate --engine 一致：engine 是别名（mvsep|mediakit|cloudsep），provider 才是注册表名。
		provider, ok := sepEngineProvider(in.Engine)
		if !ok {
			return nil, jsonResult{}, fmt.Errorf("暂不支持该分离引擎 %q（mvsep|gsgc|zhuanhuanmao|mediakit）", in.Engine)
		}
		params := map[string]any{}
		switch provider {
		case "mvsep":
			sepType := in.SepType
			if sepType == "" {
				sepType = mcpDefaultSepType // MelBand Roformer：免费档质量最优
			}
			format := in.Format
			if format == "" {
				format = "0" // 0=MP3
			}
			params["sep_type"] = sepType
			params["output_format"] = format
			for i, v := range []string{in.AddOpt1, in.AddOpt2, in.AddOpt3} {
				if v != "" {
					params[fmt.Sprintf("add_opt%d", i+1)] = v
				}
			}
			if in.URL != "" {
				params["url"] = in.URL
			}
		case "gsgc", "zhuanhuanmao":
			stems := in.Stems
			if stems == "" {
				stems = "both"
			}
			params["stems"] = stems
			if in.URL != "" {
				params["url"] = in.URL
			}
		case "volcengine":
			format := in.Format
			if format == "" {
				format = "mp3"
			}
			scene := in.Scene
			if scene == "" {
				scene = "audio"
			}
			params["scene"] = scene
			params["output_format"] = format
			if in.URL != "" {
				params["url"] = in.URL
			}
		}
		if in.OutDir != "" {
			params["_out"] = in.OutDir
		}
		res, err := r.run(ctx, provider, "separate", params, files)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_translate",
		Description: "机器翻译：32 语种互译，from 缺省自动检测。terms 直传术语「原词＝译词」逗号/换行分隔可固定译法。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpTranslateIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.Text) == "" {
			return nil, jsonResult{}, fmt.Errorf("text 必填")
		}
		if in.To == "" {
			return nil, jsonResult{}, fmt.Errorf("to 必填（目标语言代码，如 zh/en/ja/zh-Hant）")
		}
		params := map[string]any{"text": in.Text, "target_language": in.To}
		if in.From != "" {
			params["source_language"] = in.From
		}
		if in.Terms != "" {
			params["terms"] = in.Terms
		}
		if in.TableID != "" {
			params["glossary_table_id"] = in.TableID
		}
		if in.TableName != "" {
			params["glossary_table_name"] = in.TableName
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "volcengine", "translate", params, nil)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_minutes",
		Description: "语音妙记：公网音视频 URL（≤2 小时、<1G）转结构化纪要：转写+说话人、总结、待办、章节、翻译。features 逗号分隔至少一项：summary|todo|qa|chapter|translation。同步等待分钟级耗时，计费按小时（转写+结构费）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpMinutesIn) (*mcp.CallToolResult, jsonResult, error) {
		if in.URL == "" {
			return nil, jsonResult{}, fmt.Errorf("url 必填（公网音视频地址）")
		}
		if strings.TrimSpace(in.Features) == "" {
			return nil, jsonResult{}, fmt.Errorf("features 必填（至少一项，逗号分隔：summary|todo|qa|chapter|translation）")
		}
		params := map[string]any{"url": in.URL, "features": in.Features}
		if in.SourceLang != "" {
			params["source_lang"] = in.SourceLang
		}
		if in.TargetLang != "" {
			params["target_lang"] = in.TargetLang
		}
		if in.Speakers != 0 {
			params["speakers"] = in.Speakers
		}
		if in.Hotwords != "" {
			params["hotwords"] = in.Hotwords
		}
		if in.AllActivate != nil {
			params["all_activate"] = *in.AllActivate
		}
		if in.WordTimestamps {
			params["word_timestamps"] = true
		}
		if in.OutDir != "" {
			params["_out"] = in.OutDir
		}
		res, err := r.run(ctx, "volcengine", "minutes", params, nil)
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_voices",
		Description: "音色查询：列出火山引擎内置音色（ID/名称/性别/场景/语种），用于 tts/podcast 的 voice/speakers 参数。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpVoicesIn) (*mcp.CallToolResult, mcpVoicesOut, error) {
		filtered := make([]volcengine.Voice, 0)
		for _, v := range volcengine.Voices() {
			if in.Scene != "" && !voicesMatches(v.Scenes, in.Scene) {
				continue
			}
			if in.Lang != "" && !voicesMatches(v.Languages, in.Lang) {
				continue
			}
			if in.Gender != "" && v.Gender != in.Gender {
				continue
			}
			if in.Query != "" && !strings.Contains(v.ID, in.Query) && !strings.Contains(v.Name, in.Query) {
				continue
			}
			filtered = append(filtered, v)
		}
		return nil, mcpVoicesOut{Count: len(filtered), Voices: filtered}, nil
	})

	// 音频剪辑：编辑合为一个 op 枚举工具 + 分析查询工具，
	// 与 Web/CLI 共享同一 audiotool 实现。
	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_audio_edit",
		Description: "音频剪辑：op=trim 切割（保留/挖除选区）|merge 合并（可交叉淡化，files 传其余文件）|pitch 变调变速（乐调半音+速度倍率双轴）|eq 10 段均衡器|volume 音量增益或响度归一化|fade 淡入淡出|reverse 倒放。依赖服务器装有 ffmpeg。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpAudioEditIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.File) == "" {
			return nil, jsonResult{}, fmt.Errorf("file 必填（输入音频绝对路径）")
		}
		files := map[string]string{"audio": in.File}
		for i, f := range in.Files {
			if strings.TrimSpace(f) != "" {
				files[fmt.Sprintf("audio%d", i+2)] = f
			}
		}
		params := map[string]any{}
		if in.Format != "" {
			params["format"] = in.Format
		}
		if in.Bitrate != "" {
			params["bitrate"] = in.Bitrate
		}
		toolName := ""
		switch in.Op {
		case "trim":
			toolName = "trim"
			params["start"] = in.Start
			params["end"] = in.End
			params["cutout"] = in.Cutout
		case "merge":
			toolName = "merge"
			params["crossfade"] = in.Crossfade
		case "pitch":
			toolName = "pitch"
			params["semitones"] = in.Semitones
			if in.Tempo != 0 {
				params["tempo"] = in.Tempo
			}
		case "eq":
			toolName = "equalizer"
			if in.Preset != "" {
				params["preset"] = in.Preset
			}
			if in.Gains != "" {
				params["gains"] = in.Gains
			}
		case "volume":
			toolName = "volume"
			if in.GainDB != 0 {
				params["gain_db"] = in.GainDB
			}
			if in.Normalize {
				params["normalize"] = true
				if in.LUFS != 0 {
					params["lufs"] = in.LUFS
				}
			}
		case "fade":
			toolName = "fade"
			params["fade_in"] = in.FadeIn
			params["fade_out"] = in.FadeOut
			if in.Curve != "" {
				params["curve"] = in.Curve
			}
		case "reverse":
			toolName = "reverse"
		default:
			return nil, jsonResult{}, fmt.Errorf("op 必填且须为: trim|merge|pitch|eq|volume|fade|reverse")
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "audio", toolName, params, files)
		return nil, res, err
	})

	// 垫音混音：分离产物的伴奏+人声双轨合轨出口，
	// 与 Web/CLI 共享同一 audiotool mix 实现；audio=伴奏、audio2=人声。
	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_audio_mix",
		Description: "垫音混音：分离产物的伴奏+人声双轨混音。music=伴奏绝对路径、vocal=人声绝对路径；vocal_gain 人声增益 dB（默认 -16 垫音档，-99 纯伴奏）；vocal_env 垫音包络 JSON [[秒,dB]…]（同刻双点=跳变，非空覆盖 vocal_gain）；vocal_highpass 人声低切 Hz（默认 120，0=关）；music_gain 伴奏增益（默认 0）；master_loudness 响度归一 LUFS（默认 -14，0=关）；format mp3|wav。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpAudioMixIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.Music) == "" || strings.TrimSpace(in.Vocal) == "" {
			return nil, jsonResult{}, fmt.Errorf("music 与 vocal 必填（两轨绝对路径）")
		}
		params := map[string]any{}
		if in.VocalGain != nil {
			params["vocal_gain"] = *in.VocalGain
		}
		if in.VocalEnv != "" {
			params["vocal_env"] = in.VocalEnv
		}
		if in.VocalHighpass != nil {
			params["vocal_highpass"] = *in.VocalHighpass
		}
		if in.MusicGain != nil {
			params["music_gain"] = *in.MusicGain
		}
		if in.MasterLoudness != nil {
			params["master_loudness"] = *in.MasterLoudness
		}
		if in.Format != "" {
			format := strings.ToLower(strings.TrimSpace(in.Format))
			if format != "mp3" && format != "wav" {
				return nil, jsonResult{}, fmt.Errorf("format 仅支持 mp3|wav（缺省 mp3）")
			}
			params["format"] = format
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "audio", "mix", params, map[string]string{"audio": in.Music, "audio2": in.Vocal})
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_audio_analyze",
		Description: "调与 BPM 查询（本地 DSP，零额度）：分析任意歌曲的调（key）、音阶（大/小调）、Camelot 和谐混音编码与 BPM 节奏，附备选节奏与置信度。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpAudioAnalyzeIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.File) == "" {
			return nil, jsonResult{}, fmt.Errorf("file 必填（输入音频绝对路径）")
		}
		res, err := r.run(ctx, "audio", "analyze", nil, map[string]string{"audio": in.File})
		return nil, res, err
	})

	// 副歌候选检测（零产物）与选区切片导出：hook 出候选选区、
	// clip 一键成片，与 Web/CLI 共享同一 audiotool 实现。
	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_audio_hook",
		Description: "副歌候选检测（本地 DSP，零额度）：不产音频，按能量与起伏定位最像副歌的 top-N 选区，返回候选（start/end 秒、score 评分 0-1）供切片选区参考；duration 目标时长秒（5-60，默认 30）、count 候选数（1-5，默认 3）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpAudioHookIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.File) == "" {
			return nil, jsonResult{}, fmt.Errorf("file 必填（输入音频绝对路径）")
		}
		params := map[string]any{}
		if in.Duration != nil {
			params["duration"] = *in.Duration
		}
		if in.Count != nil {
			params["count"] = *in.Count
		}
		res, err := r.run(ctx, "audio", "hook", params, map[string]string{"audio": in.File})
		return nil, res, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_audio_clip",
		Description: "选区切片导出：截取选区加淡入淡出与响度归一导出成片，铃声选 m4r（iPhone 直接可用）、短视频用 mp3；fade_in/fade_out 秒（0 即关，默认 1/0.5）；loudness 响度归一 LUFS（默认 -14，0 即关）；format mp3|m4a|m4r。选区可先用 voxbox_audio_hook 取副歌候选。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpAudioClipIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.File) == "" {
			return nil, jsonResult{}, fmt.Errorf("file 必填（输入音频绝对路径）")
		}
		if in.Start < 0 {
			return nil, jsonResult{}, fmt.Errorf("start 不能为负，收到 %g", in.Start)
		}
		if in.End <= in.Start {
			return nil, jsonResult{}, fmt.Errorf("end（%gs）需大于 start（%gs）", in.End, in.Start)
		}
		params := map[string]any{"start": in.Start, "end": in.End}
		if in.FadeIn != nil {
			params["fade_in"] = *in.FadeIn
		}
		if in.FadeOut != nil {
			params["fade_out"] = *in.FadeOut
		}
		if in.Loudness != nil {
			params["loudness"] = *in.Loudness
		}
		if in.Format != "" {
			format := strings.ToLower(strings.TrimSpace(in.Format))
			if format != "mp3" && format != "m4a" && format != "m4r" {
				return nil, jsonResult{}, fmt.Errorf("format 仅支持 mp3|m4a|m4r（缺省 mp3）")
			}
			params["format"] = format
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		res, err := r.run(ctx, "audio", "clip", params, map[string]string{"audio": in.File})
		return nil, res, err
	})

	// 口播闪避：人声侧链压低 BGM 的播客/口播出口，与 Web/CLI
	// 共享同一 audiotool duck 实现；audio=BGM、audio2=人声（序不可颠倒）。
	mcp.AddTool(server, &mcp.Tool{
		Name:        "voxbox_audio_duck",
		Description: "口播闪避：说话时人声自动压低 BGM。bgm 背景音乐绝对路径、vocal 人声绝对路径，files 顺序 audio=BGM、audio2=人声（序即滤镜图序，不可颠倒）；depth 闪避深度 dB 默认 -12（0 即不压直通，域 -40 ~ 0；实际压深随语音密度过冲，约再加深 4~8dB）；bgm_gain BGM 基线增益默认 -6；loudness 响度归一 LUFS 默认 -14（0 即关）；format mp3|wav。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpAudioDuckIn) (*mcp.CallToolResult, jsonResult, error) {
		if strings.TrimSpace(in.BGM) == "" || strings.TrimSpace(in.Vocal) == "" {
			return nil, jsonResult{}, fmt.Errorf("bgm 与 vocal 必填（两轨绝对路径）")
		}
		params := map[string]any{}
		if in.Depth != nil {
			params["depth"] = *in.Depth
		}
		if in.BgmGain != nil {
			params["bgm_gain"] = *in.BgmGain
		}
		if in.Loudness != nil {
			params["loudness"] = *in.Loudness
		}
		if in.Format != "" {
			format := strings.ToLower(strings.TrimSpace(in.Format))
			if format != "mp3" && format != "wav" {
				return nil, jsonResult{}, fmt.Errorf("format 仅支持 mp3|wav（缺省 mp3）")
			}
			params["format"] = format
		}
		if in.Out != "" {
			params["_out"] = in.Out
		}
		// 输入序铁律（同 audiotool duck 滤镜图序）：audio=BGM 主输入、audio2=人声侧链，
		// 颠倒了被压的是人声本体。
		res, err := r.run(ctx, "audio", "duck", params, map[string]string{"audio": in.BGM, "audio2": in.Vocal})
		return nil, res, err
	})
}

// mcpTaskWait 单次工具调用的总等待上限：分离/妙记/播客这类分钟级任务，外加免费队列排队。
const mcpTaskWait = 45 * time.Minute

// waitTask 轮询落库任务至终态并组装 jsonResult。MCP 侧没有事件推送通道，
// 故用短轮询替代 runToolCore 的 SubmitSync —— 后者不接受 InputRef。
func (r *mcpRunner) waitTask(ctx context.Context, id string) (jsonResult, error) {
	deadline := time.Now().Add(mcpTaskWait)
	for {
		t, err := r.svc.DB().GetTask(id)
		if err != nil {
			return jsonResult{}, fmt.Errorf("查询任务失败: %w", err)
		}
		switch t.Status {
		case store.StatusSucceeded, store.StatusFailed, store.StatusCanceled, store.StatusInterrupted:
			arts, err := r.svc.DB().ListArtifacts(id)
			if err != nil {
				return jsonResult{}, fmt.Errorf("查询产物失败: %w", err)
			}
			res := jsonResult{
				TaskID: t.ID, Provider: t.Provider, Tool: t.Tool,
				Status: string(t.Status), CostMS: t.CostMS,
				Artifacts: []artifactOut{}, Error: t.Error,
			}
			for _, a := range arts {
				res.Artifacts = append(res.Artifacts, artifactOut{
					Kind: a.Kind, Path: absArtifactPath(r.svc.Config().DataDir, a.Path),
					Format: a.Format, Size: a.Size, DurationMS: a.DurationMS,
					URL: artifactURLFromMeta(a.Meta),
				})
			}
			if t.Summary != "" {
				_ = json.Unmarshal([]byte(t.Summary), &res.Summary)
			}
			if t.Status != store.StatusSucceeded {
				return res, fmt.Errorf("任务%s: %s", t.Status, t.Error)
			}
			return res, nil
		}
		if time.Now().After(deadline) {
			return jsonResult{}, fmt.Errorf("等待任务 %s 超时（%s）", id, mcpTaskWait)
		}
		select {
		case <-ctx.Done():
			return jsonResult{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// SharedTTSOpts tts_long/tts_stream 共享的合成参数（键名与 CLI 完全一致）。
type SharedTTSOpts struct {
	SampleRate       int    `json:"sample_rate,omitempty" jsonschema:"采样率 Hz: 8000/16000/22050/24000/32000/44100/48000，默认 24000"`
	SpeechRate       int    `json:"speech_rate,omitempty" jsonschema:"语速 -50-100（100 即 2 倍速）"`
	LoudnessRate     int    `json:"loudness_rate,omitempty" jsonschema:"音量 -50-100（100 即 2 倍音量）"`
	Pitch            int    `json:"pitch,omitempty" jsonschema:"音调 -12-12"`
	BitRate          int    `json:"bit_rate,omitempty" jsonschema:"比特率 bps: 64000/160000"`
	AigcWatermark    bool   `json:"aigc_watermark,omitempty" jsonschema:"音频结尾添加 AIGC 节奏标识"`
	Resource         string `json:"resource,omitempty" jsonschema:"seed-tts-2.0（默认，普通音色）| seed-icl-2.0（复刻音色）"`
	Model            string `json:"model,omitempty" jsonschema:"复刻模型版本（仅复刻音色需指定）"`
	ExplicitLanguage string `json:"explicit_language,omitempty" jsonschema:"朗读语种: zh-cn|en|es-mx|id|pt-br"`
}

func applySharedTTS(o *SharedTTSOpts, params map[string]any) {
	if o.SampleRate != 0 {
		params["sample_rate"] = o.SampleRate
	}
	if o.SpeechRate != 0 {
		params["speech_rate"] = o.SpeechRate
	}
	if o.LoudnessRate != 0 {
		params["loudness_rate"] = o.LoudnessRate
	}
	if o.Pitch != 0 {
		params["pitch"] = o.Pitch
	}
	if o.BitRate != 0 {
		params["bit_rate"] = o.BitRate
	}
	if o.AigcWatermark {
		params["aigc_watermark"] = true
	}
	if o.Resource != "" {
		params["resource"] = o.Resource
	}
	if o.Model != "" {
		params["model"] = o.Model
	}
	if o.ExplicitLanguage != "" {
		params["explicit_language"] = o.ExplicitLanguage
	}
}

type mcpTTSIn struct {
	Text        string  `json:"text" jsonschema:"要合成的文本"`
	Voice       string  `json:"voice,omitempty" jsonschema:"音色 ID，默认 zh_female_cancan_mars_bigtts（可用 voxbox_voices 查询）"`
	Format      string  `json:"format,omitempty" jsonschema:"音频格式: mp3|wav|pcm|ogg_opus，默认 mp3"`
	SpeedRatio  float64 `json:"speed_ratio,omitempty" jsonschema:"语速 0.2-3.0，默认 1.0"`
	VolumeRatio float64 `json:"volume_ratio,omitempty" jsonschema:"音量 0.2-3.0，默认 1.0"`
	Out         string  `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

type mcpTTSLongIn struct {
	Text       string `json:"text" jsonschema:"要合成的长文本（≤10 万字）"`
	Voice      string `json:"voice,omitempty" jsonschema:"音色 ID，默认 zh_female_vv_uranus_bigtts（2.0/复刻音色）"`
	Format     string `json:"format,omitempty" jsonschema:"音频格式: mp3|pcm|ogg_opus，默认 mp3"`
	Timestamps bool   `json:"timestamps,omitempty" jsonschema:"开启时间戳，额外产出 SRT 字幕"`
	SharedTTSOpts
	Out string `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

type mcpTTSStreamIn struct {
	Text            string `json:"text" jsonschema:"要合成的文本"`
	Voice           string `json:"voice,omitempty" jsonschema:"音色 ID，默认 zh_female_cancan_mars_bigtts"`
	Format          string `json:"format,omitempty" jsonschema:"音频格式: mp3|wav|pcm|ogg_opus，默认 mp3"`
	SilenceDuration int    `json:"silence_duration,omitempty" jsonschema:"句尾静音时长 ms（0-3000）"`
	Subtitle        bool   `json:"subtitle,omitempty" jsonschema:"产出字级字幕 SRT"`
	ToneFidelity    bool   `json:"tone_fidelity,omitempty" jsonschema:"复刻音色音色保真"`
	ExplicitDialect string `json:"explicit_dialect,omitempty" jsonschema:"显式方言: yue-CN（粤语）等 8 种"`
	ContextText     string `json:"context_text,omitempty" jsonschema:"语音指令：方言/语种/情感/语速等自然语言描述"`
	SharedTTSOpts
	Out string `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

type mcpASRIn struct {
	Path     string `json:"path,omitempty" jsonschema:"本地音频文件路径（mp3/wav/ogg/pcm，走一句话识别同步秒级），与 url 二选一"`
	URL      string `json:"url,omitempty" jsonschema:"公网音频 URL，与 path 二选一"`
	Version  string `json:"version,omitempty" jsonschema:"识别版本，缺省按输入推断（path→sentence，url→standard）: sentence 一句话识别（本地文件）/ standard 标准版（URL，异步）/ idle 闲时（URL，24h 内）/ flash 极速（URL，秒级）"`
	Language string `json:"language,omitempty" jsonschema:"识别语言，留空自动识别；可选 zh-CN/en-US/ja-JP/yue-CN 等 25 种"`
	Hotwords string `json:"hotwords,omitempty" jsonschema:"热词，逗号分隔，提升专有名词识别率"`
	Srt      *bool  `json:"srt,omitempty" jsonschema:"是否额外产出 SRT 字幕，默认 true"`
	Out      string `json:"out,omitempty" jsonschema:"转写文本输出绝对路径（缺省写入数据目录）"`
}

type mcpPodcastIn struct {
	Text      string `json:"text,omitempty" jsonschema:"播客主题或长文本（与 url/script 三选一）"`
	URL       string `json:"url,omitempty" jsonschema:"网页链接，服务端联网总结后生成（三选一）"`
	Script    string `json:"script,omitempty" jsonschema:"对话稿 JSON 文件路径（三选一）"`
	Speakers  string `json:"speakers" jsonschema:"两个音色 ID，逗号分隔，顺序为说话人 A、B"`
	Format    string `json:"format,omitempty" jsonschema:"音频格式: mp3|ogg_opus|pcm|aac，默认 mp3"`
	HeadMusic bool   `json:"head_music,omitempty" jsonschema:"是否加开头音乐"`
	Out       string `json:"out,omitempty" jsonschema:"播客音频输出绝对路径（缺省写入数据目录，对话稿同路径 .json）"`
}

// mcpDefaultSepType MVSep 缺省算法 render_id：MelBand Roformer（vocals/instrumental 两轨）。
// 免费账号（registered，50 次/天）只能用 price_coefficient=1 的算法；26=Ensemble 等系数 >1 的
// 需付费会员，提交时上游会回 400「unavailable until you purchase premium membership」。
const mcpDefaultSepType = "48"

// sepEngineProvider 把使用者侧的引擎别名归一化为注册表里的 provider 名。
// mvsep 恒定；mediakit 与 volcengine 是同一条火山链路的两种写法（使用文档/前端用 mediakit，
// provider 注册名是 volcengine）—— 别名必须在这里收敛，否则会把 mediakit 当 provider 传给
// service 层，被「provider 仅支持 mvsep | volcengine」挡掉。cloudsep 为转换猫/格式工厂
// 共用免费通道（两站品牌名均接受）。空值按 mvsep。
func sepEngineProvider(engine string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "mvsep":
		return "mvsep", true
	case "mediakit", "volcengine":
		return "volcengine", true
	case "gsgc", "cloudsep", "pcgeshi", "geshi":
		return "gsgc", true
	case "zhuanhuanmao", "convertmao":
		return "zhuanhuanmao", true
	default:
		return "", false
	}
}

type mcpSeparateIn struct {
	URL     string `json:"url,omitempty" jsonschema:"公网音视频 URL（与 file 二选一）"`
	File    string `json:"file,omitempty" jsonschema:"本地音视频文件绝对路径（与 url 二选一）"`
	Engine  string `json:"engine,omitempty" jsonschema:"分离引擎: mvsep（默认，免费且支持本地文件直传）|gsgc（格式工厂线路，免费无需凭证）|zhuanhuanmao（转换猫线路，同后端镜像，免费无需凭证）|mediakit（火山计费）"`
	Stems   string `json:"stems,omitempty" jsonschema:"提取轨道（仅 engine=gsgc/zhuanhuanmao）: both 人声+伴奏双轨（默认）|vocals 只提取人声|instrumental 只提取伴奏"`
	SepType string `json:"sep_type,omitempty" jsonschema:"MVSep 算法 render_id（engine=mvsep），缺省 48=MelBand Roformer；指定前先查可用算法，系数>1 的需付费会员"`
	AddOpt1 string `json:"add_opt1,omitempty" jsonschema:"MVSep 附加选项 1（所选算法 algorithm_fields 定义）"`
	AddOpt2 string `json:"add_opt2,omitempty" jsonschema:"MVSep 附加选项 2（如 Ensemble 的 Model Type）"`
	AddOpt3 string `json:"add_opt3,omitempty" jsonschema:"MVSep 附加选项 3"`
	Scene   string `json:"scene,omitempty" jsonschema:"mediakit 分离场景: audio（默认）|music|drama|narrate"`
	Format  string `json:"format,omitempty" jsonschema:"输出格式。mvsep: 0=MP3（默认）|1-4=WAV 各位深|5=FLAC；mediakit: aac|mp3（默认）|wav|m4a|flac；gsgc 固定 WAV 无需指定"`
	OutDir  string `json:"out_dir,omitempty" jsonschema:"音轨输出目录（缺省写入数据目录）"`
}

// mcpAudioEditIn 音频剪辑统一入参：按 op 取用对应字段，其余忽略。
type mcpAudioEditIn struct {
	Op        string   `json:"op" jsonschema:"操作: trim 切割|merge 合并|pitch 变调变速|eq 均衡器|volume 音量与响度|fade 淡入淡出|reverse 倒放"`
	File      string   `json:"file" jsonschema:"输入音频文件绝对路径（merge 的第一个文件）"`
	Files     []string `json:"files,omitempty" jsonschema:"merge 的其余文件绝对路径（按顺序拼接到 file 之后，至少 1 个）"`
	Start     float64  `json:"start,omitempty" jsonschema:"trim 选区起点（秒）"`
	End       float64  `json:"end,omitempty" jsonschema:"trim 选区终点（秒），0=到结尾"`
	Cutout    bool     `json:"cutout,omitempty" jsonschema:"trim 挖除选区：保留选区以外的部分（去广告/去口误）"`
	Crossfade float64  `json:"crossfade,omitempty" jsonschema:"merge 接缝交叉淡化秒数（0=直接拼接，如 2）"`
	Semitones float64  `json:"semitones,omitempty" jsonschema:"pitch 变调半音数（-12~12，1 半音 = 1 个乐调）"`
	Tempo     float64  `json:"tempo,omitempty" jsonschema:"pitch 速度倍率（0.25~4，缺省 1 不变速）"`
	Preset    string   `json:"preset,omitempty" jsonschema:"eq 预设: flat|pop|rock|jazz|classical|vocal|bass|treble|loudness"`
	Gains     string   `json:"gains,omitempty" jsonschema:"eq 自定义 10 段增益逗号分隔（dB，±12），如 3,0,0,-2,0,1,2,3,4,4；非空覆盖 preset"`
	GainDB    float64  `json:"gain_db,omitempty" jsonschema:"volume 增益 dB（-30~12）"`
	Normalize bool     `json:"normalize,omitempty" jsonschema:"volume 响度归一化（loudnorm，覆盖增益）"`
	LUFS      float64  `json:"lufs,omitempty" jsonschema:"volume 目标响度 LUFS: -14 流媒体（默认）|-16 播客|-23 广播"`
	FadeIn    float64  `json:"fade_in,omitempty" jsonschema:"fade 淡入秒数"`
	FadeOut   float64  `json:"fade_out,omitempty" jsonschema:"fade 淡出秒数"`
	Curve     string   `json:"curve,omitempty" jsonschema:"fade 曲线: linear（默认）|log|exp"`
	Format    string   `json:"format,omitempty" jsonschema:"输出格式: auto 跟随源（默认）|mp3|m4a|wav|flac|ogg"`
	Bitrate   string   `json:"bitrate,omitempty" jsonschema:"码率（无损格式忽略）: 128k|192k（默认）|256k|320k"`
	Out       string   `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

// mcpAudioMixIn 垫音混音入参：music/vocal 是分离产物的伴奏/人声两轨绝对路径。
// 增益类字段用指针区分「未传」与「显式 0」——0 是合法值（vocal_gain=0 原曲人声、
// vocal_highpass=0 关低切），未传时省略键，由 audiotool 端取默认（-16 / 120 / 0 / -14）。
type mcpAudioMixIn struct {
	Music          string   `json:"music" jsonschema:"伴奏音轨绝对路径（分离产物的 instrumental/background 轨）"`
	Vocal          string   `json:"vocal" jsonschema:"人声音轨绝对路径（分离产物的 vocals/voice 轨）"`
	VocalGain      *float64 `json:"vocal_gain,omitempty" jsonschema:"人声增益 dB: -16 垫音档（默认）|0 原曲人声|-99 纯伴奏"`
	VocalEnv       string   `json:"vocal_env,omitempty" jsonschema:"垫音包络 JSON [[秒,dB]…]，同刻双点=跳变；非空覆盖 vocal_gain"`
	VocalHighpass  *float64 `json:"vocal_highpass,omitempty" jsonschema:"人声低切 Hz，默认 120，0=关"`
	MusicGain      *float64 `json:"music_gain,omitempty" jsonschema:"伴奏增益 dB，默认 0"`
	MasterLoudness *float64 `json:"master_loudness,omitempty" jsonschema:"响度归一 LUFS，默认 -14，0=关"`
	Format         string   `json:"format,omitempty" jsonschema:"输出格式: mp3（默认，128k）|wav"`
	Out            string   `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

type mcpAudioAnalyzeIn struct {
	File string `json:"file" jsonschema:"音频文件绝对路径"`
}

// mcpAudioHookIn 副歌候选检测入参：零产物工具，候选区间（start/end/score）走结果 Summary。
// duration/count 用指针区分「未传」与「显式 0」——未传时省略键，由 audiotool 端取默认（30 / 3）。
type mcpAudioHookIn struct {
	File     string   `json:"file" jsonschema:"音频文件绝对路径"`
	Duration *float64 `json:"duration,omitempty" jsonschema:"目标切片时长秒（5-60），默认 30"`
	Count    *float64 `json:"count,omitempty" jsonschema:"候选数量（1-5），默认 3"`
}

// mcpAudioClipIn 选区切片入参：start/end 必填（秒）；fade/loudness 用指针区分「未传」与
// 「显式 0」——0 是合法值（fade 0 即不淡、loudness 0 关响度归一），未传时省略键，
// 由 audiotool 端取默认（1 / 0.5 / -14）。
type mcpAudioClipIn struct {
	File     string   `json:"file" jsonschema:"音频文件绝对路径"`
	Start    float64  `json:"start" jsonschema:"选区起点（秒），不小于 0"`
	End      float64  `json:"end" jsonschema:"选区终点（秒），需大于起点，选区最长 600 秒"`
	FadeIn   *float64 `json:"fade_in,omitempty" jsonschema:"淡入秒数，0 即不淡入，默认 1；超选区一半自动折半"`
	FadeOut  *float64 `json:"fade_out,omitempty" jsonschema:"淡出秒数，0 即不淡出，默认 0.5；超选区一半自动折半"`
	Loudness *float64 `json:"loudness,omitempty" jsonschema:"响度归一 LUFS，默认 -14，0 即关；有效域 -70 ~ -5"`
	Format   string   `json:"format,omitempty" jsonschema:"输出格式: mp3（默认，128k）|m4a|m4r（iPhone 铃声）"`
	Out      string   `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

// mcpAudioDuckIn 口播闪避入参：bgm/vocal 两轨绝对路径，files 顺序 audio=BGM、
// audio2=人声（序即滤镜图序：[0:a]=BGM 主输入、[1:a]=人声侧链，不可颠倒）。
// depth/bgm_gain/loudness 用指针区分「未传」与「显式 0」——0 是合法值（depth 0
// 直通不压、loudness 0 关响度归一），未传时省略键，由 audiotool 端取默认
// （-12 / -6 / -14）。
type mcpAudioDuckIn struct {
	BGM      string   `json:"bgm" jsonschema:"BGM 背景音乐绝对路径（第 1 输入，说话时被压低的主轨）"`
	Vocal    string   `json:"vocal" jsonschema:"人声绝对路径（第 2 输入，侧链控制信号）"`
	Depth    *float64 `json:"depth,omitempty" jsonschema:"闪避深度 dB，默认 -12；0 即不压直通；域 -40 ~ 0；实际压深随语音密度过冲，约再加深 4~8dB"`
	BgmGain  *float64 `json:"bgm_gain,omitempty" jsonschema:"BGM 基线增益 dB，默认 -6；域 -40 ~ 6"`
	Loudness *float64 `json:"loudness,omitempty" jsonschema:"响度归一 LUFS，默认 -14，0 即关；有效域 -70 ~ -5"`
	Format   string   `json:"format,omitempty" jsonschema:"输出格式: mp3（默认，128k）|wav"`
	Out      string   `json:"out,omitempty" jsonschema:"产物输出绝对路径（缺省写入数据目录）"`
}

type mcpTranslateIn struct {
	Text      string `json:"text" jsonschema:"待翻译文本"`
	To        string `json:"to" jsonschema:"目标语言代码（32 语种，如 zh/en/ja/zh-Hant）"`
	From      string `json:"from,omitempty" jsonschema:"源语言代码，缺省自动检测"`
	Terms     string `json:"terms,omitempty" jsonschema:"直传术语：原词＝译词，逗号或换行分隔"`
	TableID   string `json:"table_id,omitempty" jsonschema:"术语表 ID（术语管理平台）"`
	TableName string `json:"table_name,omitempty" jsonschema:"术语表名称（与 table_id 二选一或同传）"`
	Out       string `json:"out,omitempty" jsonschema:"译文输出绝对路径（缺省写入数据目录）"`
}

type mcpMinutesIn struct {
	URL            string `json:"url" jsonschema:"公网音视频 URL（≤2 小时、<1G）"`
	Features       string `json:"features" jsonschema:"附加功能，逗号分隔至少一项: summary|todo|qa|chapter|translation"`
	SourceLang     string `json:"source_lang,omitempty" jsonschema:"源语种: zh_cn（默认）|en_us"`
	TargetLang     string `json:"target_lang,omitempty" jsonschema:"翻译目标语: en_us（默认）|zh_cn（features 含 translation 时生效）"`
	Speakers       int    `json:"speakers,omitempty" jsonschema:"说话人数，0 即自动识别（默认）"`
	Hotwords       string `json:"hotwords,omitempty" jsonschema:"热词，逗号分隔"`
	AllActivate    *bool  `json:"all_activate,omitempty" jsonschema:"打包计费，默认 true（false 按所选功能汇总计费）"`
	WordTimestamps bool   `json:"word_timestamps,omitempty" jsonschema:"需要字级时间序列"`
	OutDir         string `json:"out_dir,omitempty" jsonschema:"结果输出目录（缺省写入数据目录）"`
}

type mcpVoicesIn struct {
	Scene  string `json:"scene,omitempty" jsonschema:"场景筛选，如 通用|带货|扮演|客服|童声"`
	Lang   string `json:"lang,omitempty" jsonschema:"语种筛选，如 中文|英语|日语"`
	Gender string `json:"gender,omitempty" jsonschema:"性别筛选: 男|女"`
	Query  string `json:"query,omitempty" jsonschema:"音色名称/ID 关键词包含匹配"`
}

// mcpVoicesOut 音色查询结果。MCP 规定 outputSchema 顶层必须是 object，
// 直接用 []Voice 会被 go-sdk 推断成 type=array（含 null），客户端校验 tools/list 时直接拒收
// （invalid_value: tools[N].outputSchema.type 期望 object）。故包一层，CLI 的裸数组契约不受影响。
type mcpVoicesOut struct {
	Count  int                `json:"count" jsonschema:"命中音色数量"`
	Voices []volcengine.Voice `json:"voices" jsonschema:"音色列表"`
}
