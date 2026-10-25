package workflow

import (
	"encoding/json"

	"github.com/yann0917/voxbox/internal/workflow"
)

// 内置模板：随版本发布的预置工作流（不入库，「复制为我的」另存后即用户编排）。
// 模板升级随版本走；用户已保存的副本不受影响。

// Template 内置模板定义。Internal=true 表示由业务上下文驱动的内部编排
// （如歌曲行内按钮自带曲目参数）：不进模板库列表，仅供快捷跑端点使用——
// 曲目 ID 这类参数用户无从手填，模板卡不该 asks 用户。
type Template struct {
	Key         string
	Name        string
	Description string
	Graph       workflow.Graph
	Internal    bool
}

// Templates 内置模板清单（顺序即前端模板库展示顺序）。
var Templates = []Template{
	{
		Key:         "link-subtitle",
		Name:        "链接 → 字幕稿",
		Description: "贴视频链接，自动下载音频并本地识别，产出 SRT 字幕与文稿",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "url", Label: "视频链接", Type: "string", Required: true, Placeholder: "https://…"},
			},
			Nodes: []workflow.Node{
				{
					Key: "download", Provider: "downloader", Tool: "download",
					Params:     map[string]any{"url": "{{inputs.url}}", "mode": "audio", "max_items": 1, "subtitles": false},
					InputsFrom: map[string]string{}, X: 60, Y: 80,
				},
				{
					Key: "asr", Provider: "local", Tool: "asr",
					Params:     map[string]any{"model": "sensevoice", "language": "auto", "itn": true},
					InputsFrom: map[string]string{"audio": "download"}, X: 380, Y: 80,
				},
			},
		},
	},
	{
		Key:         "link-subtitled",
		Name:        "链接 → 字幕成片",
		Description: "贴视频链接，下载后本地转写并把字幕直接烧进视频，出一条带字幕的成片",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "url", Label: "视频链接", Type: "string", Required: true, Placeholder: "https://…"},
			},
			Nodes: []workflow.Node{
				{
					Key: "download", Provider: "downloader", Tool: "download",
					Params: map[string]any{"url": "{{inputs.url}}", "mode": "video", "max_items": 1, "subtitles": false},
					X: 40, Y: 80,
				},
				{
					Key: "asr", Provider: "local", Tool: "asr",
					Params:     map[string]any{"model": "sensevoice", "language": "auto", "itn": true, "srt": true},
					InputsFrom: map[string]string{"audio": "download"},
					X: 320, Y: 40,
				},
				{
					Key: "burn", Provider: "audio", Tool: "subtitle_burn",
					Params:     map[string]any{"style": "white-outline"},
					InputsFrom: map[string]string{"audio": "download", "audio2": "asr"},
					X: 600, Y: 80,
				},
			},
		},
	},
	{
		Key:         "link-minutes",
		Name:        "链接 → 语音妙记",
		Description: "贴视频链接，下载后转结构化纪要：转写+说话人+全文总结（需火山凭证）",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "url", Label: "视频链接", Type: "string", Required: true, Placeholder: "https://…"},
			},
			Nodes: []workflow.Node{
				{
					Key: "download", Provider: "downloader", Tool: "download",
					Params: map[string]any{"url": "{{inputs.url}}", "mode": "audio", "max_items": 1, "subtitles": false},
					X:      60, Y: 80,
				},
				{
					Key: "minutes", Provider: "volcengine", Tool: "minutes",
					Params:     map[string]any{"features": "summary", "source_lang": "zh_cn", "target_lang": "zh_cn"},
					InputsFrom: map[string]string{"audio": "download"}, X: 380, Y: 80,
				},
			},
		},
	},
	{
		Key:         "song-stems",
		Name:        "歌曲 → 伴奏人声",
		Description: "搜索选一首歌，下载并自动人声分离，产出人声与伴奏双轨",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "track", Label: "选歌", Type: "track", Required: true, Placeholder: "搜索歌名或歌手，选一首"},
			},
			Nodes: []workflow.Node{
				{
					Key: "download", Provider: "music", Tool: "music_download",
					Params: map[string]any{
						"source": "{{inputs.track.source}}", "rid": "{{inputs.track.rid}}",
						"name": "{{inputs.track.name}}", "artist": "{{inputs.track.artist}}",
						"duration_ms": "{{inputs.track.duration}}",
					},
					X: 60, Y: 80,
				},
				{
					Key: "separate", Provider: "volcengine", Tool: "separate",
					Params:     map[string]any{"scene": "Music", "output_format": "mp3"},
					InputsFrom: map[string]string{"audio": "download"}, X: 380, Y: 80,
				},
			},
		},
	},
	{
		Key:         "song-clip",
		Name:        "歌曲 → 切高潮",
		Description: "搜索选一首歌，下载后直达音频剪辑的切高潮页（副歌候选+选区切片）",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "track", Label: "选歌", Type: "track", Required: true, Placeholder: "搜索歌名或歌手，选一首"},
			},
			Nodes: []workflow.Node{
				{
					Key: "download", Provider: "music", Tool: "music_download",
					Params: map[string]any{
						"source": "{{inputs.track.source}}", "rid": "{{inputs.track.rid}}",
						"name": "{{inputs.track.name}}", "artist": "{{inputs.track.artist}}",
						"duration_ms": "{{inputs.track.duration}}",
					},
					X: 60, Y: 80,
				},
			},
		},
	},
	{
		Key:         "text-podcast",
		Name:        "文案 → 双人播客",
		Description: "贴一篇文章或主题，生成双人对话播客音频（需火山凭证；音色填两个火山音色 ID 逗号分隔）",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "text", Label: "文案", Type: "text", Required: true, Placeholder: "播客主题或长文本（≤12000 字）"},
				{Key: "speakers", Label: "音色", Type: "string", Required: true, Placeholder: "两个音色 ID，逗号分隔"},
			},
			Nodes: []workflow.Node{
				{
					Key: "podcast", Provider: "volcengine", Tool: "podcast",
					Params: map[string]any{
						"input_text": "{{inputs.text}}", "speakers": "{{inputs.speakers}}",
						"format": "mp3", "head_music": false,
					},
					X: 60, Y: 80,
				},
			},
		},
	},
	{
		Key:         "text-tts",
		Name:        "文案 → 配音",
		Description: "贴文案用千问合成配音音频；复制为我的工作流后可换任意合成引擎",
		Graph: workflow.Graph{
			Inputs: []workflow.InputSlot{
				{Key: "text", Label: "文案", Type: "text", Required: true, Placeholder: "输入要合成的文本"},
			},
			Nodes: []workflow.Node{
				{
					Key: "tts", Provider: "qianwen", Tool: "tts",
					Params: map[string]any{"text": "{{inputs.text}}"},
					X:      60, Y: 80,
				},
			},
		},
	},
}

// TemplateByKey 模板键查模板。
func TemplateByKey(key string) (Template, bool) {
	for _, t := range Templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}

// graphJSON 图结构序列化（模板与用户编排同格式落 DTO）。
func graphJSON(g workflow.Graph) json.RawMessage {
	b, _ := json.Marshal(g)
	return b
}
