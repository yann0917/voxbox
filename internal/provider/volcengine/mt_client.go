package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

const (
	// mtTranslatePath 机器翻译大模型（matx）翻译路径。
	mtTranslatePath = "/api/v3/machine_translation/matx_translate"
	// mtResourceID 机器翻译资源 ID（需在控制台开通该权限）。
	mtResourceID = "volc.speech.mt"
	// mtCodeOK 成功状态码（响应体 code 字段，同 TTS 长文本的 20000000 语义）。
	mtCodeOK = 20000000

	// mtMaxTexts 单次请求待翻译文本条数上限（官方约束）。
	mtMaxTexts = 16
)

// 机器翻译上游业务错误码（官方文档仅列 4 个；鉴权失败未给码，按 HTTP 401/403 判定）。
const (
	mtCodeBadParam = 45000001 // 请求参数错误（如 target_language 未指定）
	mtCodeOverflow = 45000130 // 载荷过大：列表 >16 条或单条 >1024 Tokens
	mtCodeSrvError = 55000001 // 服务内部错误，可重试
)

// MTTranslateReq 翻译请求（Tool 层完成校验与默认值）。
type MTTranslateReq struct {
	SourceLanguage    string            // 源语言代码；空串=自动检测
	TargetLanguage    string            // 目标语言代码（必填）
	TextList          []string          // 待翻译文本，≤16 条、单条 ≤1024 Tokens
	GlossaryList      map[string]string // 直传术语 {"原词":"译词"}，优先级高于术语表
	GlossaryTableID   string            // 术语表 ID（术语管理平台），与 Name 二选一或同传
	GlossaryTableName string            // 术语表名称
}

// MTUsage Token 用量统计。
type MTUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// MTTranslation 单条翻译结果，与请求 text_list 一一对应。
type MTTranslation struct {
	Translation            string
	DetectedSourceLanguage string // 仅未指定 source_language 时返回
	Usage                  MTUsage
}

// MTClient 火山引擎机器翻译大模型（matx_translate）REST 客户端。
// 鉴权同语音三件套：SpeechCred（新版 X-Api-Key 或旧版 X-Api-App-Key + X-Api-Access-Key）。
// 同步接口：一次 POST 返回全部译文。
type MTClient struct {
	resty *resty.Client
	cred  SpeechCred
}

func NewMTClient(cred SpeechCred) *MTClient {
	return NewMTClientWithBaseURL(cred, ttsBaseURL)
}

// NewMTClientWithBaseURL 供测试注入 mock 地址。
func NewMTClientWithBaseURL(cred SpeechCred, baseURL string) *MTClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(60 * time.Second)
	return &MTClient{resty: r, cred: cred}
}

// mtCorpus 术语配置：三项全空时不携带该对象。
type mtCorpus struct {
	GlossaryList      map[string]string `json:"glossary_list,omitempty"`
	GlossaryTableID   string            `json:"glossary_table_id,omitempty"`
	GlossaryTableName string            `json:"glossary_table_name,omitempty"`
}

type mtTranslateAPIReq struct {
	SourceLanguage string    `json:"source_language,omitempty"`
	TargetLanguage string    `json:"target_language"`
	TextList       []string  `json:"text_list"`
	Corpus         *mtCorpus `json:"corpus,omitempty"`
}

type mtTranslateAPIResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *struct {
		TranslationList []struct {
			Translation            string  `json:"translation"`
			DetectedSourceLanguage string  `json:"detected_source_language"`
			Usage                  MTUsage `json:"usage"`
		} `json:"translation_list"`
	} `json:"data"`
}

// Translate 同步翻译：POST matx_translate，返回与 TextList 一一对应的译文列表。
func (c *MTClient) Translate(ctx context.Context, req MTTranslateReq) ([]MTTranslation, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, mtResourceID)

	apiReq := mtTranslateAPIReq{
		SourceLanguage: req.SourceLanguage,
		TargetLanguage: req.TargetLanguage,
		TextList:       req.TextList,
	}
	if len(req.GlossaryList) > 0 || req.GlossaryTableID != "" || req.GlossaryTableName != "" {
		apiReq.Corpus = &mtCorpus{
			GlossaryList:      req.GlossaryList,
			GlossaryTableID:   req.GlossaryTableID,
			GlossaryTableName: req.GlossaryTableName,
		}
	}

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetBody(apiReq).
		Post(mtTranslatePath)
	if err != nil {
		return nil, fmt.Errorf("请求火山机器翻译失败: %w", err)
	}
	// 鉴权失败走 HTTP 状态码（401/403），映射 ErrAuth 供 CLI 退出码 4 与设置页判定。
	if httpResp.StatusCode() == http.StatusUnauthorized || httpResp.StatusCode() == http.StatusForbidden {
		return nil, fmt.Errorf("%w: 火山机器翻译鉴权失败(HTTP %d): %s%s",
			ErrAuth, httpResp.StatusCode(), aucBodyExcerpt(httpResp.Body()), ttsLogidSuffix(httpResp))
	}
	// 非 200 也可能是带业务码的结构化错误体（实测资源未开通返回 HTTP 500 +
	// {"code":55000000,"message":"...requested resource not granted"}），
	// 故先尝试解析包络，解析不出再退回 HTTP 状态码 + 响应体摘录。
	if httpResp.StatusCode() != http.StatusOK {
		var apiResp mtTranslateAPIResp
		if jsonErr := json.Unmarshal(httpResp.Body(), &apiResp); jsonErr == nil && apiResp.Code != 0 {
			return nil, mtTranslateError(apiResp.Code, apiResp.Message, ttsLogidSuffix(httpResp))
		}
		return nil, fmt.Errorf("请求火山机器翻译失败(HTTP %d): %s%s",
			httpResp.StatusCode(), aucBodyExcerpt(httpResp.Body()), ttsLogidSuffix(httpResp))
	}

	var apiResp mtTranslateAPIResp
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return nil, fmt.Errorf("解析火山机器翻译响应失败: %w", err)
	}
	if apiResp.Code != mtCodeOK {
		return nil, mtTranslateError(apiResp.Code, apiResp.Message, ttsLogidSuffix(httpResp))
	}
	if apiResp.Data == nil || len(apiResp.Data.TranslationList) == 0 {
		return nil, fmt.Errorf("火山机器翻译未返回翻译结果")
	}

	out := make([]MTTranslation, 0, len(apiResp.Data.TranslationList))
	for _, item := range apiResp.Data.TranslationList {
		out = append(out, MTTranslation{
			Translation:            item.Translation,
			DetectedSourceLanguage: item.DetectedSourceLanguage,
			Usage:                  item.Usage,
		})
	}
	return out, nil
}

// mtTranslateError 上游业务错误码 → 中文错误（保留原始码便于排查）。
// 「resource not granted」字面落在 55000000（服务内部错误）下，但语义是权限未开通，
// 单独识别为 ErrNotGranted（退出码 4），避免被当成可重试的任务失败。
func mtTranslateError(code int, message, logid string) error {
	if mtIsNotGranted(message) {
		return fmt.Errorf("%w: 机器翻译资源 volc.speech.mt 未开通（上游 %d: %s）%s；请到火山引擎控制台开通「机器翻译」服务并确认资源包/权限已生效",
			ErrNotGranted, code, message, logid)
	}
	switch code {
	case mtCodeBadParam:
		return fmt.Errorf("火山机器翻译参数错误(%d): %s%s", code, message, logid)
	case mtCodeOverflow:
		return fmt.Errorf("翻译载荷超限(%d): %s（单条文本不超过 1024 Tokens、列表不超过 %d 条，请分段提交）%s",
			code, message, mtMaxTexts, logid)
	case mtCodeSrvError:
		return fmt.Errorf("火山机器翻译服务内部错误(%d): %s，请重试%s", code, message, logid)
	}
	return fmt.Errorf("火山机器翻译失败(%d): %s%s", code, message, logid)
}

// mtIsNotGranted 判定「资源未授予」：上游文案不固定（requested resource not granted /
// resource not granted / not granted），统一大小写后按关键片段匹配。
func mtIsNotGranted(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "not granted") || strings.Contains(m, "not authorized") ||
		strings.Contains(m, "未开通") || strings.Contains(m, "未授权")
}
