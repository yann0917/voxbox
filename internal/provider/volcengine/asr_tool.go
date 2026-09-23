package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// ASR 异步轮询节奏（var 便于测试注入更短间隔）：起步间隔指数退避、退避上限、总超时。
var (
	asrPollInterval = 2 * time.Second
	asrPollMax      = 30 * time.Second
	asrPollTimeout  = 10 * time.Minute
)

// 闲时版轮询节奏与极速版同步预算（同样 var 供测试注入）：闲时任务官方口径 24h 内完成，
// 退避上限放宽到 2 分钟避免全天高频轮询；极速版同步接口长音频可能超过 resty 单请求 60s。
var (
	asrIdlePollInterval = 10 * time.Second
	asrIdlePollMax      = 2 * time.Minute
	asrIdlePollTimeout  = 24 * time.Hour
	asrFlashTimeout     = 15 * time.Minute
)

// ASR 版本与服务的对应关系（四条通道、两个服务族，开通控制台时按此核对）：
//   - sentence 一句话识别：流式语音识别 2.0（单向流式大模型，6561/2628951）的整段非流式模式，
//     本地文件 WS 同步秒级返回，资源 volc.seedasr.sauc.duration；
//   - standard 标准版：录音文件识别大模型（6561/1354868），URL 异步 submit/query，
//     资源 volc.seedasr.auc；
//   - idle 闲时版（6561/2608618）/ flash 极速版（6561/2608628）：仅 URL。
const (
	asrVersionSentence = "sentence" // 一句话识别：仅本地文件，WS 同步
	asrVersionStandard = "standard" // 标准版：仅 URL，异步 submit/query
	asrVersionIdle     = "idle"     // 仅 URL，闲时算力执行，任务通常 24h 内完成
	asrVersionFlash    = "flash"    // 仅 URL，同步返回结果（≤100MB、2 小时内音频）
)

// asrNormalizeVersion 归一版本参数：空与未知值一律回退标准版（向后兼容旧任务参数）；
// 旧参数 standard+本地文件的组合在 Run 中自动按一句话识别处理（标准版语义已改为仅 URL）。
func asrNormalizeVersion(v string) string {
	switch v {
	case asrVersionSentence, asrVersionIdle, asrVersionFlash:
		return v
	}
	return asrVersionStandard
}

// ASRTool 语音识别工具：一句话识别（本地文件，Files["audio"]，WS 同步）+
// 录音文件识别标准/闲时/极速（Params["url"]，标准版异步 submit/query，极速同步、闲时长轮询），
// 产物为转写文本与 SRT 字幕。
type ASRTool struct {
	ws     *ASRClient
	auc    *ASRAUCClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewASRTool(cred SpeechCred, outDir string) *ASRTool {
	return &ASRTool{
		ws:     NewASRClient(cred),
		auc:    NewASRAUCClient(cred),
		cred:   cred,
		outDir: outDir,
	}
}

func (t *ASRTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "asr",
		Title:       "语音识别",
		Description: "音频转文字，支持本地文件与公网 URL，输出分句时间戳与 SRT 字幕",
		Group:       "语音",
	}
}

// asrLanguages 录音文件识别支持语种（官方文档 6561/2608618/2608628 同款词表）。
// language 留空时模型自动识别：中文、英文、上海话、闽南话、四川话、陕西话、粤语。
var asrLanguages = []provider.ParamOption{
	{Value: "zh-CN", Label: "中文普通话"},
	{Value: "en-US", Label: "英语"},
	{Value: "ja-JP", Label: "日语"},
	{Value: "id-ID", Label: "印尼语"},
	{Value: "es-MX", Label: "西班牙语"},
	{Value: "pt-BR", Label: "葡萄牙语"},
	{Value: "de-DE", Label: "德语"},
	{Value: "fr-FR", Label: "法语"},
	{Value: "ko-KR", Label: "韩语"},
	{Value: "fil-PH", Label: "菲律宾语"},
	{Value: "ms-MY", Label: "马来语"},
	{Value: "th-TH", Label: "泰语"},
	{Value: "ar-SA", Label: "阿拉伯语"},
	{Value: "it-IT", Label: "意大利语"},
	{Value: "bn-BD", Label: "孟加拉语"},
	{Value: "el-GR", Label: "希腊语"},
	{Value: "nl-NL", Label: "荷兰语"},
	{Value: "ru-RU", Label: "俄语"},
	{Value: "tr-TR", Label: "土耳其语"},
	{Value: "vi-VN", Label: "越南语"},
	{Value: "pl-PL", Label: "波兰语"},
	{Value: "ro-RO", Label: "罗马尼亚语"},
	{Value: "ne-NP", Label: "尼泊尔语"},
	{Value: "uk-UA", Label: "乌克兰语"},
	{Value: "yue-CN", Label: "粤语"},
}

func (t *ASRTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "version", Label: "识别版本", Type: provider.ParamEnum, Default: asrVersionSentence, Group: "输入",
			Options: []provider.ParamOption{
				{Value: asrVersionSentence, Label: "一句话识别（本地文件，同步秒级）"},
				{Value: asrVersionStandard, Label: "标准版（URL，录音文件识别）"},
				{Value: asrVersionIdle, Label: "闲时版（URL，24h 内完成）"},
				{Value: asrVersionFlash, Label: "极速版（URL，秒级返回）"},
			}},
		{Key: "url", Label: "音频 URL", Type: provider.ParamString,
			Placeholder: "公网音频 URL，标准/闲时/极速版使用；留空可配合本地文件（需配置对象存储）", Group: "输入"},
		{Key: "hotwords", Label: "热词", Type: provider.ParamString,
			Placeholder: "逗号分隔热词", Group: "输入"},
		{Key: "language", Label: "语言", Type: provider.ParamEnum, Default: "", Group: "输入",
			Options:     asrLanguages,
			Placeholder: "留空自动识别（中文/英文/常见方言）"},
		{Key: "srt", Label: "生成 SRT 字幕", Type: provider.ParamBool,
			Default: true, Group: "输出"},
	}
}

func (t *ASRTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行：缺少输入、版本与输入方式冲突、格式不受支持属参数错误（退出码 2），
	// 不应被凭证校验（退出码 4）掩盖。
	version := asrNormalizeVersion(paramString(in.Params, "version"))
	audioPath := in.Files["audio"]
	audioURL := strings.TrimSpace(paramString(in.Params, "url"))
	if audioPath == "" && audioURL == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少输入：请上传音频文件或提供音频 URL")
	}
	// 一句话版只吃本地文件（sauc WS 协议传音频字节）；闲时/极速版协议只收 audio.url
	//（6561/2608618、6561/2608628）：URL 直用，本地文件经对象存储中转（ensureURLInput）；
	// 标准版+本地文件：配置了对象存储时同样中转后走真标准版异步（尊重版本选择），
	// 未配置存储时保持旧行为降级一句话同步（历史任务重跑兼容）。
	if version == asrVersionSentence && audioPath == "" {
		return provider.TaskOutput{}, fmt.Errorf("一句话识别仅支持本地上传音频文件，URL 请使用标准版/闲时版/极速版")
	}
	localSync := version == asrVersionSentence
	if version == asrVersionStandard && audioPath != "" && in.Storage == nil {
		version = asrVersionSentence
		localSync = true
	}
	var format string
	if localSync && audioPath != "" {
		var err error
		if format, err = audioFormatOf(audioPath); err != nil {
			return provider.TaskOutput{}, err
		}
	}
	if !localSync && audioPath != "" {
		// 转存前先拦格式与大小，省一次注定失败的上传（上限对齐官方：极速 100MB/2h、闲时 512MB/5h）。
		if err := checkURLFileForBridge(audioPath, version); err != nil {
			return provider.TaskOutput{}, err
		}
	}
	if err := t.cred.Validate(); err != nil {
		return provider.TaskOutput{}, err
	}

	var (
		resp   ASRNostreamResp
		source string
	)
	switch {
	case localSync: // 一句话识别：本地文件 WS 同步（旧 standard+文件参数已自动归入本分支）
		source = "file"
		audio, err := os.ReadFile(audioPath)
		if err != nil {
			return provider.TaskOutput{}, fmt.Errorf("读取音频文件失败: %w", err)
		}
		report(20, "正在识别音频（一句话）", nil)
		resp, err = t.ws.Recognize(ctx, ASRNostreamReq{
			Audio:    audio,
			Format:   format,
			Language: paramString(in.Params, "language"),
			Hotwords: paramString(in.Params, "hotwords"),
		})
		if err != nil {
			return provider.TaskOutput{}, err
		}
	case version == asrVersionIdle: // 闲时版：URL 直用 / 本地文件转存，submit + 长周期轮询（任务通常 24h 内完成）
		source = "url"
		audioURL, err := ensureURLInput(ctx, in, "url", "音频",
			"缺少输入：闲时版需要音频 URL 或本地文件（对象存储中转）", report)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		req, err := idleFlashRequest(audioURL,
			paramString(in.Params, "language"), paramString(in.Params, "hotwords"))
		if err != nil {
			return provider.TaskOutput{}, err
		}
		report(10, "提交闲时识别任务", nil)
		taskID, err := t.auc.SubmitIdle(ctx, req)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		report(30, "闲时任务已提交，通常 24 小时内完成", map[string]any{"task_id": taskID})
		resp, err = t.pollAUC(ctx, taskID, aucPoller{
			interval: asrIdlePollInterval, max: asrIdlePollMax, timeout: asrIdlePollTimeout,
			query: t.auc.QueryIdle,
			onPoll: func(n int, status string) {
				report(30, "等待闲时识别结果", map[string]any{"task_id": taskID, "poll": n, "status": status})
			},
			timeoutMsg: "等待火山闲时识别结果超时（24 小时），任务可能仍在处理，请稍后重试或联系技术支持",
		})
		if err != nil {
			return provider.TaskOutput{}, err
		}
	case version == asrVersionFlash: // 极速版：URL 直用 / 本地文件转存，同步返回无需轮询
		source = "url"
		audioURL, err := ensureURLInput(ctx, in, "url", "音频",
			"缺少输入：极速版需要音频 URL 或本地文件（对象存储中转）", report)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		req, err := idleFlashRequest(audioURL,
			paramString(in.Params, "language"), paramString(in.Params, "hotwords"))
		if err != nil {
			return provider.TaskOutput{}, err
		}
		report(20, "极速识别中", nil)
		fctx, cancel := context.WithTimeout(ctx, asrFlashTimeout)
		resp, err = t.auc.RecognizeFlash(fctx, req)
		cancel()
		if err != nil {
			return provider.TaskOutput{}, err
		}
	default: // 标准版：URL 直用 / 本地文件转存，异步 submit + 轮询
		source = "url"
		audioURL, err := ensureURLInput(ctx, in, "url", "音频",
			"缺少输入：标准版需要音频 URL 或本地文件（对象存储中转）", report)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		report(10, "提交异步识别任务", nil)
		taskID, err := t.auc.Submit(ctx, audioURL)
		if err != nil {
			return provider.TaskOutput{}, err
		}
		report(30, "等待识别结果", map[string]any{"task_id": taskID})
		resp, err = t.pollAUC(ctx, taskID, aucPoller{
			interval: asrPollInterval, max: asrPollMax, timeout: asrPollTimeout,
			query:      t.auc.Query,
			timeoutMsg: "等待火山 ASR 识别结果超时，请稍后重试",
		})
		if err != nil {
			return provider.TaskOutput{}, err
		}
	}
	report(90, "保存识别结果", nil)
	return t.saveArtifacts(in, resp, source, version)
}

// aucPoller 轮询配置：query 为具体版本的查询函数，onPoll 每次查询后回调（可空，用于进度上报）。
type aucPoller struct {
	interval   time.Duration // 起步间隔（指数退避）
	max        time.Duration // 退避上限
	timeout    time.Duration // 总超时兜底（空 status 等中间态靠它终止）
	query      func(context.Context, string) (ASRNostreamResp, string, error)
	onPoll     func(n int, status string)
	timeoutMsg string
}

// pollAUC 轮询异步识别任务：起步 interval 指数退避至 max，总时长 timeout 兜底；
// ctx 取消优先返回。终态：Completed → 结果；Failed → 报错。
func (t *ASRTool) pollAUC(ctx context.Context, taskID string, p aucPoller) (ASRNostreamResp, error) {
	deadline := time.Now().Add(p.timeout)
	interval := p.interval
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return ASRNostreamResp{}, fmt.Errorf("ASR 识别已取消: %w", err)
		}
		if time.Now().After(deadline) {
			return ASRNostreamResp{}, fmt.Errorf("%s", p.timeoutMsg)
		}
		resp, status, err := p.query(ctx, taskID)
		if err != nil {
			return ASRNostreamResp{}, err
		}
		if p.onPoll != nil {
			p.onPoll(n, status)
		}
		switch status {
		case "Completed":
			return resp, nil
		case "Failed":
			return ASRNostreamResp{}, fmt.Errorf("上游识别失败（任务 %s）", taskID)
		}
		select {
		case <-ctx.Done():
			return ASRNostreamResp{}, fmt.Errorf("ASR 识别已取消: %w", ctx.Err())
		case <-time.After(interval):
		}
		interval *= 2
		if interval > p.max {
			interval = p.max
		}
	}
}

// saveArtifacts 落盘转写文本（asr/<uuid>.txt）与 SRT 字幕（asr/<uuid>.srt，
// srt 参数默认开启且分句非空时生成），并按 M2 契约处理 _out 重定向。
func (t *ASRTool) saveArtifacts(in provider.TaskInput, resp ASRNostreamResp, source, version string) (provider.TaskOutput, error) {
	srtContent := ""
	if asrSRTEnabled(in.Params) && len(resp.Segments) > 0 {
		srtContent = BuildSRT(resp.Segments)
	}

	reqID := uuid.NewString()
	txtPath := filepath.Join("asr", reqID+".txt")
	// _out 参数（CLI --out）重定向产物路径；不进 ParamSpecs，属机器约定。
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		txtPath = outParam
	}
	// srt 路径跟随 txt 路径：仅换扩展名（_out 为绝对路径时 srt 同为绝对；相对时在相对段上替换）。
	srtPath := strings.TrimSuffix(txtPath, filepath.Ext(txtPath)) + ".srt"

	txtAbs := txtPath
	if !filepath.IsAbs(txtAbs) {
		txtAbs = filepath.Join(t.outDir, txtPath)
		txtPath, _ = filepath.Rel(t.outDir, txtAbs)
	}
	srtAbs := srtPath
	if !filepath.IsAbs(srtAbs) {
		srtAbs = filepath.Join(t.outDir, srtPath)
		srtPath, _ = filepath.Rel(t.outDir, srtAbs)
	}
	if err := os.MkdirAll(filepath.Dir(txtAbs), 0o755); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(txtAbs, []byte(resp.Text), 0o644); err != nil {
		return provider.TaskOutput{}, fmt.Errorf("写入转写文本失败: %w", err)
	}

	arts := []provider.Artifact{{
		Kind: "transcript", Path: txtPath, Format: "txt",
		Size: int64(len(resp.Text)), DurationMS: resp.DurationMS,
	}}
	if srtContent != "" {
		if err := os.WriteFile(srtAbs, []byte(srtContent), 0o644); err != nil {
			return provider.TaskOutput{}, fmt.Errorf("写入 SRT 字幕失败: %w", err)
		}
		arts = append(arts, provider.Artifact{
			Kind: "subtitle", Path: srtPath, Format: "srt", Size: int64(len(srtContent)),
		})
	}

	segs := make([]map[string]any, 0, len(resp.Segments))
	for _, s := range resp.Segments {
		segs = append(segs, map[string]any{"text": s.Text, "start_ms": s.StartMS, "end_ms": s.EndMS})
	}
	return provider.TaskOutput{
		Artifacts: arts,
		Summary: map[string]any{
			"segments":    segs,
			"duration_ms": resp.DurationMS,
			"source":      source,
			"version":     version,
		},
	}, nil
}

// checkURLFileForBridge URL 版本本地文件转存前置校验：标准/闲时/极速三版本格式白名单一致
// （wav/mp3/ogg/spx/amr/aac/m4a，转存后格式由 URL 扩展名推断），大小对齐官方上限——
// 超限在转存前拦截，不白传大文件；标准版无明确大小上限文档，交服务端裁决。
func checkURLFileForBridge(path, version string) error {
	const exts = "wav/mp3/ogg/spx/amr/aac/m4a"
	var maxBytes int64
	switch version {
	case asrVersionFlash:
		maxBytes = 100 << 20 // 极速版 ≤100MB / 2h
	case asrVersionIdle:
		maxBytes = 512 << 20 // 闲时版 ≤512MB / 5h
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if !strings.Contains("/"+exts+"/", "/"+ext+"/") {
		return fmt.Errorf("%s版不支持该音频格式 .%s（支持 %s）", versionLabel(version), ext, exts)
	}
	if maxBytes <= 0 {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("读取音频文件失败: %w", err)
	}
	if fi.Size() > maxBytes {
		return fmt.Errorf("%s版仅支持 %dMB 内音频（当前 %.0fMB）", versionLabel(version), maxBytes>>20, float64(fi.Size())/1024/1024)
	}
	return nil
}

// versionLabel 版本中文名（错误文案用）。
func versionLabel(version string) string {
	switch version {
	case asrVersionFlash:
		return "极速"
	case asrVersionIdle:
		return "闲时"
	default:
		return "标准"
	}
}

// idleFlashRequest 组装闲时版/极速版提交请求：format 必填、由 URL 扩展名推断；
// 热词打包为 corpus.context 直传。错误消息含「仅支持」以命中参数错误退出码。
func idleFlashRequest(audioURL, language, hotwords string) (aucTaskRequest, error) {
	format, err := audioFormatFromURL(audioURL)
	if err != nil {
		return aucTaskRequest{}, err
	}
	return aucTaskRequest{
		Audio: aucAudioMeta{URL: audioURL, Format: format, Language: strings.TrimSpace(language)},
		Request: aucTaskOption{
			ModelName:      "bigmodel",
			EnableITN:      true,
			EnablePunc:     true,
			ShowUtterances: true,
			Corpus:         hotwordsCorpus(hotwords),
		},
	}, nil
}

// audioFormatFromURL 从 URL 路径段推断音频格式（忽略查询串与片段）。
// 闲时版/极速版提交 body 的 audio.format 必填（6561/2608618、6561/2608628）。
func audioFormatFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("音频 URL 无法解析: %w", err)
	}
	switch ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(u.Path)), "."); ext {
	case "wav", "mp3", "ogg", "spx", "amr", "aac", "m4a":
		return ext, nil
	}
	return "", fmt.Errorf("无法从 URL 识别音频格式（闲时版/极速版仅支持 wav/mp3/ogg/spx/amr/aac/m4a，请使用带扩展名的音频 URL）")
}

// hotwordsCorpus 把逗号/分号分隔热词打包为闲时版/极速版的 corpus.context JSON 字符串；
// 空热词返回 nil（不携带 corpus 字段）。协议约束：corpus 与 enable_auto_lang 互斥。
func hotwordsCorpus(hotwords string) *aucCorpus {
	fields := strings.FieldsFunc(hotwords, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；'
	})
	words := make([]map[string]string, 0, len(fields))
	for _, w := range fields {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, map[string]string{"word": w})
		}
	}
	if len(words) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"hotwords": words})
	return &aucCorpus{Context: string(raw)}
}

// audioFormatOf 由扩展名推断音频格式，白名单对齐官方 bigmodel_nostream 文档的
// audio.format 可选值：wav/mp3/ogg/pcm/spx/amr/aac/m4a；之外的扩展名（flac/mp4 及未知）
// 一律报「暂不支持」（CLI 侧映射参数错误退出码 2）。
func audioFormatOf(path string) (string, error) {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	switch strings.ToLower(ext) {
	case "wav", "mp3", "ogg", "pcm", "spx", "amr", "aac", "m4a":
		return strings.ToLower(ext), nil
	}
	if ext == "" {
		return "", fmt.Errorf("暂不支持该音频格式（支持 wav/mp3/ogg/pcm/spx/amr/aac/m4a）")
	}
	return "", fmt.Errorf("暂不支持该音频格式 .%s（支持 wav/mp3/ogg/pcm/spx/amr/aac/m4a）", ext)
}

// asrSRTEnabled 读取 srt 参数（默认开启；兼容 bool 与字符串 "false"）。
func asrSRTEnabled(params map[string]any) bool {
	switch v := params["srt"].(type) {
	case bool:
		return v
	case string:
		return !strings.EqualFold(v, "false")
	}
	return true
}

// paramString 取字符串参数（缺失或类型不符返回空串）。
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}
