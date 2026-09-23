// 语音妙记工具：公网音视频 URL → lark minutes 异步任务（提交/轮询）→
// 结果文件（24h 临时链接）立即下载转存，转写额外转出 txt 全文与 SRT 字幕。
// 鉴权同语音三件套：新版 X-Api-Key 单键即可（官方 demo 双头为兼容写法）。
package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// 妙记异步轮询节奏（var 便于测试注入更短间隔）：官方建议查询频次 >30s，
// 产出时间取决于音频大小与排队负载；任务 24h 未结束自动丢弃，工具层 2h 兜底。
var (
	larkPollInterval = 30 * time.Second
	larkPollMax      = 2 * time.Minute
	larkToolTimeout  = 2 * time.Hour
)

// MinutesTool 语音妙记工具：音视频转结构化纪要（转写+说话人、全文总结、
// 待办/问答提取、章节总结、中英翻译）。收公网 URL 或本地文件（对象存储中转）（<1G、≤2h）。
type MinutesTool struct {
	client *LarkClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewMinutesTool(cred SpeechCred, outDir string) *MinutesTool {
	return &MinutesTool{client: NewLarkClient(cred), cred: cred, outDir: outDir}
}

// NewMinutesToolWithBaseURL 供测试注入 mock 地址。
func NewMinutesToolWithBaseURL(cred SpeechCred, outDir, baseURL string) *MinutesTool {
	return &MinutesTool{client: NewLarkClientWithBaseURL(cred, baseURL), cred: cred, outDir: outDir}
}

func (t *MinutesTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "minutes",
		Title:       "语音妙记",
		Description: "音视频 URL 转结构化纪要：转写+说话人、全文总结、待办/问答提取、章节总结、中英翻译（≤2h、<1G）",
		Group:       "语音",
	}
}

// larkLangOptions 语种枚举（妙记当前仅中英）。
var larkLangOptions = []provider.ParamOption{
	{Value: "zh_cn", Label: "中文"},
	{Value: "en_us", Label: "英语"},
}

func (t *MinutesTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		// url 非必填：本地文件 + 对象存储中转时由任务运行期补齐（ensureURLInput）。
		{Key: "url", Label: "音视频 URL", Type: provider.ParamString,
			Placeholder: "公网可访问的音视频 URL；留空则使用上传的本地文件（需配置对象存储）", Group: "输入"},
		{Key: "features", Label: "附加功能", Type: provider.ParamEnum, Default: "summary", Group: "功能",
			Placeholder: "逗号分隔：summary 全文总结 / todo 待办 / qa 问答 / chapter 章节 / translation 翻译（至少一项）"},
		{Key: "source_lang", Label: "源语种", Type: provider.ParamEnum,
			Default: "zh_cn", Options: larkLangOptions, Group: "参数"},
		{Key: "target_lang", Label: "翻译目标语", Type: provider.ParamEnum,
			Default: "zh_cn", Options: larkLangOptions, Group: "参数"},
		{Key: "speakers", Label: "说话人数", Type: provider.ParamInt, Default: 0, Group: "参数",
			Placeholder: "0 = 自动识别"},
		{Key: "hotwords", Label: "热词", Type: provider.ParamString,
			Placeholder: "逗号分隔，提升专有名词准确率", Group: "参数"},
		{Key: "all_activate", Label: "打包计费", Type: provider.ParamBool, Default: true, Group: "高级",
			Placeholder: "true 按打包价；false 按所选功能汇总计费"},
		{Key: "word_timestamps", Label: "字级时间戳", Type: provider.ParamBool, Default: false, Group: "高级"},
	}
}

func (t *MinutesTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（退出码 2），不被凭证校验（退出码 4）掩盖；凭证校验先于文件转存
	//（无凭证不白传大文件）。输入二选一：公网 URL，或本地文件（配置了对象存储时自动中转）。
	hotwords := larkHotwordsJSON(paramString(in.Params, "hotwords"))
	if hotwords == "" && paramString(in.Params, "hotwords") != "" {
		return provider.TaskOutput{}, fmt.Errorf("热词格式错误：请提供逗号分隔的非空词表")
	}
	features, err := larkParseFeatures(paramString(in.Params, "features"))
	if err != nil {
		return provider.TaskOutput{}, err
	}
	sourceLang := paramString(in.Params, "source_lang")
	if sourceLang == "" {
		sourceLang = "zh_cn"
	}
	if sourceLang != "zh_cn" && sourceLang != "en_us" {
		return provider.TaskOutput{}, fmt.Errorf("仅支持源语种 zh_cn/en_us，收到 %q", sourceLang)
	}
	targetLang := paramString(in.Params, "target_lang")
	if targetLang == "" {
		targetLang = "zh_cn"
	}
	if targetLang != "zh_cn" && targetLang != "en_us" {
		return provider.TaskOutput{}, fmt.Errorf("仅支持翻译目标语 zh_cn/en_us，收到 %q", targetLang)
	}
	// 妙记鉴权同语音三件套：新版 X-Api-Key 单键即可（demo 双头为兼容写法），通用校验足够。
	if err := t.cred.Validate(); err != nil {
		return provider.TaskOutput{}, err
	}
	fileURL, err := ensureURLInput(ctx, in, "url", "音视频",
		"缺少输入：请提供公网可访问的音视频 URL（<1G、≤2 小时），或上传本地文件（需在设置页配置对象存储）", report)
	if err != nil {
		return provider.TaskOutput{}, err
	}

	req := LarkSubmitReq{
		FileURL:               fileURL,
		FileType:              larkFileTypeByExt(fileURL),
		SourceLang:            sourceLang,
		SpeakerIdentification: true,
		NumberOfSpeaker:       toInt(in.Params["speakers"], 0),
		HotWords:              hotwords,
		NeedWordTimeSeries:    paramBool(in.Params, "word_timestamps"),
		AllActivate:           paramBool(in.Params, "all_activate", "all"),
		TranslationEnable:     features.translation,
		TranslationTargetLang: targetLang,
		ExtractTodo:           features.todo,
		ExtractQA:             features.qa,
		SummarizationEnable:   features.summary,
		ChapterEnable:         features.chapter,
	}

	report(5, "提交妙记任务", nil)
	taskID, err := t.client.Submit(ctx, req)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	report(10, "任务已提交，等待处理", map[string]any{"task_id": taskID})

	result, err := t.poll(ctx, taskID, report)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	return t.saveArtifacts(ctx, in, taskID, result, features, report)
}

// larkFeatures 附加功能开关（至少一项，官方约束否则提交失败）。
type larkFeatures struct {
	summary, todo, qa, chapter, translation bool
}

// names 供 summary/features 回显。
func (f larkFeatures) names() []string {
	out := make([]string, 0, 5)
	if f.summary {
		out = append(out, "summary")
	}
	if f.todo {
		out = append(out, "todo")
	}
	if f.qa {
		out = append(out, "qa")
	}
	if f.chapter {
		out = append(out, "chapter")
	}
	if f.translation {
		out = append(out, "translation")
	}
	return out
}

// larkParseFeatures 解析附加功能：逗号分隔 summary/todo/qa/chapter/translation；
// 空默认 summary（最有用且最便宜路径）；未知项报参数错误；全空清单拒绝。
func larkParseFeatures(raw string) (larkFeatures, error) {
	var f larkFeatures
	raw = strings.TrimSpace(raw)
	if raw == "" {
		f.summary = true
		return f, nil
	}
	for _, item := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "":
			continue
		case "summary":
			f.summary = true
		case "todo":
			f.todo = true
		case "qa", "question_answer":
			f.qa = true
		case "chapter":
			f.chapter = true
		case "translation":
			f.translation = true
		default:
			return f, fmt.Errorf("暂不支持该附加功能 %q（支持 summary/todo/qa/chapter/translation，至少一项）", item)
		}
	}
	if !f.summary && !f.todo && !f.qa && !f.chapter && !f.translation {
		return f, fmt.Errorf("附加功能至少选择一项（summary/todo/qa/chapter/translation），否则上游提交失败")
	}
	return f, nil
}

// larkHotwordsJSON 逗号分隔热词 → 官方 JSON 串 [{"word":"..."}]；空输入返回空串。
func larkHotwordsJSON(raw string) string {
	fields := strings.Split(raw, ",")
	words := make([]map[string]string, 0, len(fields))
	for _, w := range fields {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, map[string]string{"word": w})
		}
	}
	if len(words) == 0 {
		return ""
	}
	b, _ := json.Marshal(words)
	return string(b)
}

// poll 轮询妙记任务直到 success/failed：起步 larkPollInterval 指数退避至 max，
// 总超时由 ctx（larkToolTimeout 兜底）约束；连续 larkMaxQueryErrors 次查询失败视为不可恢复。
func (t *MinutesTool) poll(ctx context.Context, taskID string, report provider.ProgressReporter) (*LarkResult, error) {
	const maxQueryErrors = 3
	deadline := time.Now().Add(larkToolTimeout)
	interval := larkPollInterval
	consecutiveErrs := 0
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("妙记任务已取消（上游任务 %s 仍会在服务端保留，结果链接 24h 有效）: %w", taskID, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待妙记结果超时（2 小时，上游任务 %s，可稍后用任务 ID 自行查询）", taskID)
		}
		result, status, err := t.client.Query(ctx, taskID)
		if err != nil {
			consecutiveErrs++
			if consecutiveErrs >= maxQueryErrors {
				return nil, fmt.Errorf("查询妙记任务连续 %d 次失败: %w", consecutiveErrs, err)
			}
			report(10, fmt.Sprintf("查询瞬时失败（%d/%d），继续等待", consecutiveErrs, maxQueryErrors), nil)
		} else {
			consecutiveErrs = 0
			switch status {
			case larkStatusSuccess, larkStatusCompleted:
				if result.Result == nil || result.Result.AudioTranscriptionFile == "" {
					return nil, fmt.Errorf("妙记任务已完成但未返回转写结果（任务 %s）", taskID)
				}
				return result.Result, nil
			case larkStatusFailed:
				return nil, fmt.Errorf("妙记任务失败（任务 %s）: %s", taskID, larkTaskErrMsg(result.ErrCode, result.ErrMessage))
			default:
				progress := 15 + n*2
				if progress > 90 {
					progress = 90
				}
				report(progress, "转写与纪要生成中（耗时与音视频时长正相关）",
					map[string]any{"task_id": taskID, "poll": n, "status": status})
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("妙记任务已取消（上游任务 %s 仍会在服务端保留，结果链接 24h 有效）: %w", taskID, ctx.Err())
		case <-time.After(interval):
		}
		interval *= 2
		if interval > larkPollMax {
			interval = larkPollMax
		}
	}
}

// ---------- 结果文件结构（官方 6561/1798094 数据结构） ----------

// larkSentence 转写分句（AudioTranscriptionFile JSON 数组元素）。
type larkSentence struct {
	SentenceID  string `json:"sentence_id"`
	ParagraphID string `json:"paragraph_id"`
	Speaker     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type any    `json:"type"`
	} `json:"speaker"`
	Content   string `json:"content"`
	Lang      string `json:"lang"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

// larkChapterSummary 章节总结（ChapterFile {chapter_summary:[...]}）。
type larkChapterSummary struct {
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
}

// larkTodo 待办（InformationExtractionFile todo_list 元素，字段取展示必需子集）。
type larkTodo struct {
	Content    string   `json:"content"`
	Executor   []string `json:"executor"`
	StartTime  int64    `json:"start_time"`
	SentenceID []string `json:"sentence_id"`
}

// larkSummaryDoc 全文总结（SummarizationFile {title, paragraph}）。
type larkSummaryDoc struct {
	Title     string `json:"title"`
	Paragraph string `json:"paragraph"`
}

// saveArtifacts 下载全部结果文件并落盘（minutes/<uuid>_<kind>.<ext>），转写解析为
// txt 全文（说话人前缀）与 SRT 字幕；总结/待办/章节解析进 Summary 供页面直接渲染。
// 与 separate 同契约：先全部下载成功再写盘，单文件失败整体报错不落半截产物。
// _out 参数（CLI --out-dir 透传）为输出目录重定向，文件名 <uuid>_<kind>.<ext> 不变。
func (t *MinutesTool) saveArtifacts(ctx context.Context, in provider.TaskInput, taskID string, result *LarkResult, features larkFeatures, report provider.ProgressReporter) (provider.TaskOutput, error) {
	type download struct {
		name, url string
	}
	downloads := make([]download, 0, 5)
	downloads = append(downloads, download{"transcription", result.AudioTranscriptionFile})
	if result.ChapterFile != "" {
		downloads = append(downloads, download{"chapter", result.ChapterFile})
	}
	if result.InformationExtractionFile != "" {
		downloads = append(downloads, download{"extraction", result.InformationExtractionFile})
	}
	if result.SummarizationFile != "" {
		downloads = append(downloads, download{"summary", result.SummarizationFile})
	}
	if result.TranslationFile != "" {
		downloads = append(downloads, download{"translation", result.TranslationFile})
	}

	payloads := make(map[string][]byte, len(downloads))
	for k, d := range downloads {
		data, err := t.client.Download(ctx, d.url)
		if err != nil {
			return provider.TaskOutput{}, fmt.Errorf("转存妙记结果 %s 失败: %w", d.name, err)
		}
		payloads[d.name] = data
		report(90+(k+1)*8/len(downloads), fmt.Sprintf("转存结果 %d/%d", k+1, len(downloads)), nil)
	}

	// 转写解析：分句 → 全文（说话人前缀）+ SRT（带说话人）。
	var sentences []larkSentence
	if err := json.Unmarshal(payloads["transcription"], &sentences); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("解析妙记转写结果失败: %w", err)
	}
	if len(sentences) == 0 {
		return provider.TaskOutput{}, fmt.Errorf("妙记转写结果为空")
	}
	var txt strings.Builder
	segs := make([]ASRSegment, 0, len(sentences))
	speakerIDs := map[string]bool{}
	for _, s := range sentences {
		speaker := larkSpeakerName(s)
		speakerIDs[speaker] = true
		if s.StartTime > 0 {
			txt.WriteString("\n")
		}
		fmt.Fprintf(&txt, "%s：%s", speaker, s.Content)
		segs = append(segs, ASRSegment{Text: speaker + "：" + s.Content, StartMS: s.StartTime, EndMS: s.EndTime})
	}
	fullText := strings.TrimPrefix(txt.String(), "\n")
	srtContent := BuildSRT(segs)
	durationMS := sentences[len(sentences)-1].EndTime

	reqID := uuid.NewString()
	outDirParam, _ := in.Params["_out"].(string)
	writeArtifact := func(name, kind, format string, data []byte, durationMS int64) (provider.Artifact, error) {
		var artPath, absPath string
		if outDirParam != "" {
			absPath = filepath.Join(outDirParam, name)
			artPath = absPath
			if !filepath.IsAbs(artPath) {
				absPath = filepath.Join(t.outDir, absPath)
				artPath, _ = filepath.Rel(t.outDir, absPath)
			}
		} else {
			artPath = filepath.Join("minutes", name)
			absPath = filepath.Join(t.outDir, artPath)
		}
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return provider.Artifact{}, fmt.Errorf("创建产物目录失败: %w", err)
		}
		if err := os.WriteFile(absPath, data, 0o644); err != nil {
			return provider.Artifact{}, fmt.Errorf("写入产物 %s 失败: %w", name, err)
		}
		return provider.Artifact{Kind: kind, Path: artPath, Format: format, Size: int64(len(data)), DurationMS: durationMS}, nil
	}

	arts := make([]provider.Artifact, 0, 6)
	txtName := reqID + "_transcript.txt"
	if a, err := writeArtifact(txtName, "transcript", "txt", []byte(fullText), durationMS); err != nil {
		return provider.TaskOutput{}, err
	} else {
		arts = append(arts, a)
	}
	if a, err := writeArtifact(reqID+"_transcript.srt", "subtitle", "srt", []byte(srtContent), 0); err != nil {
		return provider.TaskOutput{}, err
	} else {
		arts = append(arts, a)
	}
	for _, d := range downloads {
		if d.name == "transcription" {
			continue // 已转为 txt/srt 落盘，原始 JSON 不重复保留
		}
		if a, err := writeArtifact(reqID+"_"+d.name+".json", "minutes", "json", payloads[d.name], 0); err != nil {
			return provider.TaskOutput{}, err
		} else {
			arts = append(arts, a)
		}
	}

	// Summary：解析总结/待办/章节供页面直接渲染（文件另存 JSON 供下载留档）。
	summary := map[string]any{
		"upstream_task_id": taskID,
		"features":         features.names(),
		"duration_ms":      durationMS,
		"sentences":        len(sentences),
		"speakers_count":   len(speakerIDs),
		"segments":         segs,
	}
	if raw, ok := payloads["summary"]; ok {
		var doc larkSummaryDoc
		if err := json.Unmarshal(raw, &doc); err == nil {
			summary["minutes_title"] = doc.Title
			summary["summary_text"] = doc.Paragraph
		}
	}
	if raw, ok := payloads["extraction"]; ok {
		var doc struct {
			TodoList []larkTodo `json:"todo_list"`
		}
		if err := json.Unmarshal(raw, &doc); err == nil && len(doc.TodoList) > 0 {
			todos := make([]map[string]any, 0, len(doc.TodoList))
			for _, td := range doc.TodoList {
				todos = append(todos, map[string]any{
					"content":    td.Content,
					"executor":   td.Executor,
					"start_time": td.StartTime,
				})
			}
			summary["todos"] = todos
		}
	}
	if raw, ok := payloads["chapter"]; ok {
		var doc struct {
			Chapters []larkChapterSummary `json:"chapter_summary"`
		}
		if err := json.Unmarshal(raw, &doc); err == nil && len(doc.Chapters) > 0 {
			chapters := make([]map[string]any, 0, len(doc.Chapters))
			for _, ch := range doc.Chapters {
				chapters = append(chapters, map[string]any{
					"title":      ch.Title,
					"summary":    ch.Summary,
					"start_time": ch.StartTime,
					"end_time":   ch.EndTime,
				})
			}
			summary["chapters"] = chapters
		}
	}
	if raw, ok := payloads["translation"]; ok {
		// 翻译文件为 JSON（数组结构与转写同构），提取纯文本摘要供展示。
		var items []larkSentence
		if err := json.Unmarshal(raw, &items); err == nil && len(items) > 0 {
			var tb strings.Builder
			for _, it := range items {
				tb.WriteString(it.Content)
				tb.WriteString("\n")
			}
			summary["translation_text"] = strings.TrimRight(tb.String(), "\n")
		}
	}

	return provider.TaskOutput{Artifacts: arts, Summary: summary}, nil
}

// larkSpeakerName 说话人展示名：优先 name，缺失回退「说话人 N」（ID+1，官方 ID 从 0/1 起不一致，仅作展示）。
func larkSpeakerName(s larkSentence) string {
	if s.Speaker.Name != "" {
		return s.Speaker.Name
	}
	if s.Speaker.ID != "" {
		return "说话人" + s.Speaker.ID
	}
	return "说话人"
}
