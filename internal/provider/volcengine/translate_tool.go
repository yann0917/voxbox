package volcengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yann0917/voxbox/internal/provider"
)

// MTLanguages 机器翻译支持的 32 种语言（ISO 639-1 / BCP-47，官方文档 6561）。
var mtLanguages = []struct{ Code, Name string }{
	{"zh", "中文（简体）"}, {"en", "英语"}, {"ja", "日语"}, {"ko", "韩语"},
	{"fr", "法语"}, {"de", "德语"}, {"es", "西班牙语"}, {"pt", "葡萄牙语"},
	{"ru", "俄语"}, {"ar", "阿拉伯语"}, {"it", "意大利语"}, {"nl", "荷兰语"},
	{"pl", "波兰语"}, {"ro", "罗马尼亚语"}, {"sv", "瑞典语"}, {"da", "丹麦语"},
	{"nb", "挪威语"}, {"fi", "芬兰语"}, {"hu", "匈牙利语"}, {"cs", "捷克语"},
	{"hr", "克罗地亚语"}, {"el", "希腊语"}, {"he", "希伯来语"}, {"tr", "土耳其语"},
	{"uk", "乌克兰语"}, {"th", "泰语"}, {"vi", "越南语"}, {"id", "印度尼西亚语"},
	{"ms", "马来语"}, {"tl", "菲律宾语"}, {"hi", "印地语"}, {"zh-Hant", "中文（繁体）"},
}

// mtLangNames 语言代码 → 中文名（校验与摘要展示共用）。
var mtLangNames = func() map[string]string {
	m := make(map[string]string, len(mtLanguages))
	for _, l := range mtLanguages {
		m[l.Code] = l.Name
	}
	return m
}()

// TranslateTool 机器翻译工具（火山机器翻译大模型 matx_translate，同步接口）。
// 文本进出、无音频产物：译文落 txt 产物（kind translation）供下载与跨工具联动。
type TranslateTool struct {
	client *MTClient
	cred   SpeechCred
	outDir string // 产物写入目录（默认 ~/.voxbox/data，由构造方注入）
}

func NewTranslateTool(cred SpeechCred, outDir string) *TranslateTool {
	return &TranslateTool{client: NewMTClient(cred), cred: cred, outDir: outDir}
}

// NewTranslateToolWithBaseURL 供测试注入 mock 地址。
func NewTranslateToolWithBaseURL(cred SpeechCred, outDir, baseURL string) *TranslateTool {
	return &TranslateTool{client: NewMTClientWithBaseURL(cred, baseURL), cred: cred, outDir: outDir}
}

func (t *TranslateTool) Meta() provider.ToolMeta {
	return provider.ToolMeta{
		Provider:    "volcengine",
		Name:        "translate",
		Title:       "机器翻译",
		Description: "大模型机器翻译：32 语种互译、自动检测源语言、术语定制（需开通 volc.speech.mt）",
		Group:       "翻译",
	}
}

// mtLangOptions ParamSpec 枚举：带空值首项（auto）由调用方决定是否加入。
func mtLangOptions(withAuto bool) []provider.ParamOption {
	opts := make([]provider.ParamOption, 0, len(mtLanguages)+1)
	if withAuto {
		opts = append(opts, provider.ParamOption{Value: "", Label: "自动检测"})
	}
	for _, l := range mtLanguages {
		opts = append(opts, provider.ParamOption{Value: l.Code, Label: l.Name})
	}
	return opts
}

func (t *TranslateTool) ParamSpecs() []provider.ParamSpec {
	return []provider.ParamSpec{
		{Key: "text", Label: "原文", Type: provider.ParamText, Required: true,
			Placeholder: "输入要翻译的文本（单条不超过 1024 Tokens）", Group: "内容"},
		{Key: "target_language", Label: "目标语言", Type: provider.ParamEnum,
			Required: true, Default: "en", Options: mtLangOptions(false), Group: "语言"},
		{Key: "source_language", Label: "源语言", Type: provider.ParamEnum,
			Default: "", Options: mtLangOptions(true), Group: "语言"},
		{Key: "terms", Label: "术语", Type: provider.ParamText,
			Placeholder: "每行一条：原词=译词（如 Volcengine=火山引擎）；直传术语优先于术语表", Group: "术语"},
		{Key: "glossary_table_id", Label: "术语表 ID", Type: provider.ParamString,
			Placeholder: "从术语管理平台获取", Group: "术语"},
		{Key: "glossary_table_name", Label: "术语表名称", Type: provider.ParamString,
			Placeholder: "与术语表 ID 二选一或同时传入", Group: "术语"},
	}
}

func (t *TranslateTool) Run(ctx context.Context, in provider.TaskInput, report provider.ProgressReporter) (provider.TaskOutput, error) {
	// 参数校验先行（退出码 2），不被凭证校验（退出码 4）掩盖。
	text := strings.TrimRight(paramString(in.Params, "text"), "\n")
	if strings.TrimSpace(text) == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: text")
	}
	target := paramString(in.Params, "target_language")
	if target == "" {
		return provider.TaskOutput{}, fmt.Errorf("缺少必填参数: target_language")
	}
	if err := mtValidateLang(target, "target_language"); err != nil {
		return provider.TaskOutput{}, err
	}
	source := paramString(in.Params, "source_language")
	if source != "" {
		if err := mtValidateLang(source, "source_language"); err != nil {
			return provider.TaskOutput{}, err
		}
	}
	glossary, termsCount, err := mtParseTerms(paramString(in.Params, "terms"))
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if err := t.cred.Validate(); err != nil {
		return provider.TaskOutput{}, err
	}

	req := MTTranslateReq{
		SourceLanguage: source, TargetLanguage: target,
		TextList:     []string{text},
		GlossaryList: glossary, GlossaryTableID: paramString(in.Params, "glossary_table_id"),
		GlossaryTableName: paramString(in.Params, "glossary_table_name"),
	}

	report(10, "提交翻译请求", nil)
	items, err := t.client.Translate(ctx, req)
	if err != nil {
		return provider.TaskOutput{}, err
	}
	if len(items) != 1 {
		return provider.TaskOutput{}, fmt.Errorf("翻译结果数 %d 与请求数不符", len(items))
	}
	item := items[0]

	report(90, "写入译文文件", nil)
	artifact, err := t.saveArtifact(in, item.Translation)
	if err != nil {
		return provider.TaskOutput{}, err
	}

	summary := map[string]any{
		// 译文正文随 summary 一并入库：CLI --json 可直接读取，Web 结果区无需二次读产物。
		// 单条上限 1024 Tokens，量级对 SQLite 与 WS 消息都安全。
		"translation":       item.Translation,
		"source_language":   source,
		"target_language":   target,
		"char_count":        utf8.RuneCountInString(text),
		"terms_count":       termsCount,
		"prompt_tokens":     item.Usage.PromptTokens,
		"completion_tokens": item.Usage.CompletionTokens,
		"total_tokens":      item.Usage.TotalTokens,
	}
	if item.DetectedSourceLanguage != "" {
		summary["detected_source_language"] = item.DetectedSourceLanguage
	}
	return provider.TaskOutput{
		Artifacts: []provider.Artifact{*artifact},
		Summary:   summary,
	}, nil
}

// saveArtifact 译文落盘（translate/<uuid>.txt），按 M2 契约处理 _out 重定向。
func (t *TranslateTool) saveArtifact(in provider.TaskInput, translation string) (*provider.Artifact, error) {
	path := filepath.Join("translate", uuid.NewString()+".txt")
	if outParam, ok := in.Params["_out"].(string); ok && outParam != "" {
		path = outParam
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(t.outDir, path)
		path, _ = filepath.Rel(t.outDir, abs)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("创建产物目录失败: %w", err)
	}
	if err := os.WriteFile(abs, []byte(translation), 0o644); err != nil {
		return nil, fmt.Errorf("写入译文文件失败: %w", err)
	}
	return &provider.Artifact{
		Kind: "translation", Path: path, Format: "txt", Size: int64(len(translation)),
	}, nil
}

// mtValidateLang 校验语言代码在 32 语种清单内（CLI 自由输入时拦截）。
func mtValidateLang(code, field string) error {
	if _, ok := mtLangNames[code]; ok {
		return nil
	}
	return fmt.Errorf("仅支持官方 32 种语言代码，%s=%q 不在清单内（如 zh/en/ja/zh-Hant）", field, code)
}

// mtParseTerms 术语解析：换行或逗号分隔，每条「原词=译词」（首次 = 分隔，译词可含 =）。
// 空行跳过；格式非法报参数错误。返回词典与条数。
func mtParseTerms(raw string) (map[string]string, int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, 0, nil
	}
	glossary := map[string]string{}
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' })
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		k, v, ok := strings.Cut(f, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, 0, fmt.Errorf("术语格式错误（每行应为 原词=译词）: %q", f)
		}
		glossary[k] = v
	}
	return glossary, len(glossary), nil
}
