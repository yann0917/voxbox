package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

const (
	// larkSubmitPath 妙记（语音妙记大模型）任务提交路径。
	larkSubmitPath = "/api/v3/auc/lark/submit"
	// larkQueryPath 妙记任务查询路径。
	larkQueryPath = "/api/v3/auc/lark/query"
	// larkResourceID 妙记资源 ID（官方文档 6561/1798094）。
	larkResourceID = "volc.lark.minutes"
	// larkCodeOK 成功状态码（响应头 X-Api-Status-Code，提交与查询共用语义）。
	larkCodeOK = "20000000"
	// 查询中间态：正在处理 / 排队中（继续轮询）。
	larkCodeProcessing = "20000001"
	larkCodeQueued     = "20000002"
)

// larkTaskStatus 任务状态枚举（查询响应 Data.Status）。
const (
	larkStatusRunning = "running"
	larkStatusSuccess = "success"
	// 官方文档只列 running/success/failed；demo 额外容忍 completed，一并归为完成。
	larkStatusCompleted = "completed"
	larkStatusFailed    = "failed"
)

// LarkSubmitReq 妙记提交请求（Tool 层完成校验与默认值）。
type LarkSubmitReq struct {
	FileURL  string // 音视频公网 URL（<1G、≤2 小时）
	FileType string // audio | video
	// SourceLang 源语种：zh_cn | en_us
	SourceLang string
	// SpeakerIdentification 是否开启说话人识别；NumberOfSpeaker 说话人数（0=自动）
	SpeakerIdentification bool
	NumberOfSpeaker       int
	// HotWords 热词（工具层已组装为 [{"word":"..."}] JSON 串），空不传
	HotWords string
	// NeedWordTimeSeries 是否需要字级时间序列
	NeedWordTimeSeries bool
	// AllActivate 打包计费（true 按打包价，false 按所选附加功能汇总计费）
	AllActivate bool
	// 附加功能（官方约束：至少开启一个，否则提交失败）
	TranslationEnable     bool
	TranslationTargetLang string // zh_cn | en_us
	ExtractTodo           bool   // 待办提取
	ExtractQA             bool   // 问答提取
	SummarizationEnable   bool   // 全文总结
	ChapterEnable         bool   // 章节总结
}

// LarkResult 任务结果：五类文件 URL（24h 有效，仅开启的附加功能对应字段非空）。
type LarkResult struct {
	AudioTranscriptionFile    string
	ChapterFile               string
	InformationExtractionFile string
	SummarizationFile         string
	TranslationFile           string
}

// LarkQueryResult 查询结果状态。
type LarkQueryResult struct {
	TaskID     string
	Status     string // running | success | failed | completed
	ErrCode    int
	ErrMessage string
	Result     *LarkResult
}

// LarkClient 火山引擎语音妙记（lark minutes）REST 客户端。
// 鉴权与语音三件套同：SpeechCred 新版 X-Api-Key 单键即可（官方 demo 双头为兼容写法），
// 缺省回退 X-Api-App-Key + X-Api-Access-Key。
// 用法：Submit 提交音视频 URL → 轮询 Query（建议 >30s）至 success → Result 文件 URL 24h 有效，及时 Download。
type LarkClient struct {
	resty *resty.Client
	cred  SpeechCred
}

func NewLarkClient(cred SpeechCred) *LarkClient {
	return NewLarkClientWithBaseURL(cred, ttsBaseURL)
}

// NewLarkClientWithBaseURL 供测试注入 mock 地址。
func NewLarkClientWithBaseURL(cred SpeechCred, baseURL string) *LarkClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(60 * time.Second)
	return &LarkClient{resty: r, cred: cred}
}

// larkAuthHeader 妙记鉴权头：固定资源 ID + 请求 ID + 序列号。
// 新版 API Key 优先，缺省回退 APP ID + Access Token；查询时 requestID 传任务 ID。
func (c *LarkClient) larkAuthHeader(requestID string) http.Header {
	h := http.Header{}
	h.Add("X-Api-Resource-Id", larkResourceID)
	h.Add("X-Api-Request-Id", requestID)
	if c.cred.APIKey != "" {
		h.Add("X-Api-Key", c.cred.APIKey)
	} else {
		h.Add("X-Api-Access-Key", c.cred.AccessToken)
		h.Add("X-Api-App-Key", c.cred.AppID)
	}
	h.Add("X-Api-Sequence", "-1")
	return h
}

// larkSubmitPayload 提交请求体（字段名大驼峰，官方协议）。
type larkSubmitPayload struct {
	Input struct {
		Offline struct {
			FileURL  string `json:"FileURL"`
			FileType string `json:"FileType"`
		} `json:"Offline"`
	} `json:"Input"`
	Params struct {
		AllActivate              bool   `json:"AllActivate"`
		SourceLang               string `json:"SourceLang"`
		AudioTranscriptionEnable bool   `json:"AudioTranscriptionEnable"`
		AudioTranscriptionParams struct {
			SpeakerIdentification bool   `json:"SpeakerIdentification"`
			NumberOfSpeaker       int    `json:"NumberOfSpeaker"`
			HotWords              string `json:"HotWords,omitempty"`
			NeedWordTimeSeries    bool   `json:"NeedWordTimeSeries"`
		} `json:"AudioTranscriptionParams"`
		TranslationEnable bool `json:"TranslationEnable"`
		TranslationParams *struct {
			TargetLang string `json:"TargetLang"`
		} `json:"TranslationParams,omitempty"`
		InformationExtractionEnabled bool `json:"InformationExtractionEnabled"`
		InformationExtractionParams  *struct {
			Types []string `json:"Types"`
		} `json:"InformationExtractionParams,omitempty"`
		SummarizationEnabled bool `json:"SummarizationEnabled"`
		SummarizationParams  *struct {
			Types []string `json:"Types"`
		} `json:"SummarizationParams,omitempty"`
		ChapterEnabled bool `json:"ChapterEnabled"`
	} `json:"Params"`
}

// larkEnvelope 提交/查询共用响应包络（Code 在查询响应体中为 int；提交响应体仅 Data.TaskID）。
type larkEnvelope struct {
	Code    int    `json:"Code"`
	Message string `json:"Message"`
	Data    *struct {
		TaskID     string          `json:"TaskID"`
		Status     string          `json:"Status"`
		ErrCode    int             `json:"ErrCode"`
		ErrMessage string          `json:"ErrMessage"`
		Result     *larkResultJSON `json:"Result"`
	} `json:"Data"`
}

type larkResultJSON struct {
	AudioTranscriptionFile    string `json:"AudioTranscriptionFile"`
	ChapterFile               string `json:"ChapterFile"`
	InformationExtractionFile string `json:"InformationExtractionFile"`
	SummarizationFile         string `json:"SummarizationFile"`
	TranslationFile           string `json:"TranslationFile"`
}

// Submit 提交妙记任务：POST submit，成功判定为响应头 X-Api-Status-Code == 20000000，
// 任务 ID 取响应体 Data.TaskID（服务端生成，查询时经 X-Api-Request-Id 回传）。
func (c *LarkClient) Submit(ctx context.Context, req LarkSubmitReq) (string, error) {
	var payload larkSubmitPayload
	payload.Input.Offline.FileURL = req.FileURL
	payload.Input.Offline.FileType = req.FileType
	payload.Params.AllActivate = req.AllActivate
	payload.Params.SourceLang = req.SourceLang
	payload.Params.AudioTranscriptionEnable = true
	payload.Params.AudioTranscriptionParams.SpeakerIdentification = req.SpeakerIdentification
	payload.Params.AudioTranscriptionParams.NumberOfSpeaker = req.NumberOfSpeaker
	payload.Params.AudioTranscriptionParams.HotWords = req.HotWords
	payload.Params.AudioTranscriptionParams.NeedWordTimeSeries = req.NeedWordTimeSeries
	payload.Params.TranslationEnable = req.TranslationEnable
	if req.TranslationEnable {
		payload.Params.TranslationParams = &struct {
			TargetLang string `json:"TargetLang"`
		}{TargetLang: req.TranslationTargetLang}
	}
	extractTypes := make([]string, 0, 2)
	if req.ExtractTodo {
		extractTypes = append(extractTypes, "todo_list")
	}
	if req.ExtractQA {
		extractTypes = append(extractTypes, "question_answer")
	}
	payload.Params.InformationExtractionEnabled = len(extractTypes) > 0
	if payload.Params.InformationExtractionEnabled {
		payload.Params.InformationExtractionParams = &struct {
			Types []string `json:"Types"`
		}{Types: extractTypes}
	}
	payload.Params.SummarizationEnabled = req.SummarizationEnable
	if req.SummarizationEnable {
		payload.Params.SummarizationParams = &struct {
			Types []string `json:"Types"`
		}{Types: []string{"summary"}}
	}
	payload.Params.ChapterEnabled = req.ChapterEnable

	requestID := uuid.New().String()
	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(c.larkAuthHeader(requestID))).
		SetBody(payload).
		Post(larkSubmitPath)
	if err != nil {
		return "", fmt.Errorf("提交妙记任务失败: %w", err)
	}
	if err := checkLarkStatusCode("提交任务", httpResp.Header().Get("X-Api-Status-Code"),
		httpResp.Header().Get("X-Api-Message"), ttsLogidSuffix(httpResp)); err != nil {
		return "", err
	}

	var env larkEnvelope
	if err := json.Unmarshal(httpResp.Body(), &env); err != nil {
		return "", fmt.Errorf("解析妙记提交响应失败: %w", err)
	}
	if env.Data == nil || env.Data.TaskID == "" {
		return "", fmt.Errorf("妙记任务提交成功但未返回任务 ID")
	}
	return env.Data.TaskID, nil
}

// Query 查询妙记任务：POST query，请求体 {"TaskID":...}，X-Api-Request-Id 回传任务 ID。
// 成功判定：响应头 X-Api-Status-Code == 20000000（此时看 Data.Status 区分终态）；
// 20000001/20000002 为中间态（running/queued），原样返回由上层继续轮询。
func (c *LarkClient) Query(ctx context.Context, taskID string) (LarkQueryResult, string, error) {
	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(c.larkAuthHeader(taskID))).
		SetBody(map[string]string{"TaskID": taskID}).
		Post(larkQueryPath)
	if err != nil {
		return LarkQueryResult{}, "", fmt.Errorf("查询妙记任务失败: %w", err)
	}
	code := httpResp.Header().Get("X-Api-Status-Code")
	switch code {
	case larkCodeOK, larkCodeProcessing, larkCodeQueued, "":
		// 成功、中间态或未返回：交由响应体 Data.Status 表达任务状态。
	default:
		return LarkQueryResult{}, "", checkLarkStatusCode("查询任务", code,
			httpResp.Header().Get("X-Api-Message"), ttsLogidSuffix(httpResp))
	}

	var env larkEnvelope
	if err := json.Unmarshal(httpResp.Body(), &env); err != nil {
		return LarkQueryResult{}, "", fmt.Errorf("解析妙记查询响应失败: %w", err)
	}
	out := LarkQueryResult{}
	if env.Data != nil {
		out = LarkQueryResult{
			TaskID:     env.Data.TaskID,
			Status:     env.Data.Status,
			ErrCode:    env.Data.ErrCode,
			ErrMessage: env.Data.ErrMessage,
		}
		if env.Data.Result != nil {
			out.Result = &LarkResult{
				AudioTranscriptionFile:    env.Data.Result.AudioTranscriptionFile,
				ChapterFile:               env.Data.Result.ChapterFile,
				InformationExtractionFile: env.Data.Result.InformationExtractionFile,
				SummarizationFile:         env.Data.Result.SummarizationFile,
				TranslationFile:           env.Data.Result.TranslationFile,
			}
		}
	}
	// 响应体未给 status 时按响应头码归一（20000001/2 → running），缺失则视为 running。
	if out.Status == "" {
		if code == larkCodeProcessing || code == larkCodeQueued {
			return out, larkStatusRunning, nil
		}
		return out, larkStatusRunning, nil
	}
	return out, out.Status, nil
}

// Download 下载结果文件（转写/总结/章节/结构化/翻译，URL 24h 有效，查询成功后立即取回）。
// 独立 TOS 签名地址，不走 base URL。
func (c *LarkClient) Download(ctx context.Context, fileURL string) ([]byte, error) {
	resp, err := c.resty.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("下载妙记结果文件失败: %w", err)
	}
	defer resp.RawBody().Close()
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("下载妙记结果文件失败(HTTP %d)", resp.StatusCode())
	}
	return io.ReadAll(resp.RawBody())
}

// checkLarkStatusCode 校验响应头 X-Api-Status-Code：45 开头为参数/鉴权类
// （45000001 参数无效、45000002 空音频、45000151 格式不正确；鉴权失败同样 4 开头），
// 55000031 服务器繁忙可重试，其余非成功码原样报错。空（未返回）视为通过。
// 实测资源未开通返回 45000030 + "requested resource not granted"（文档错误码表未列），
// 语义是权限未开通而非参数错误，单列 ErrNotGranted（退出码 4）避免误导排查方向。
func checkLarkStatusCode(op, code, message, logid string) error {
	if code == "" || code == larkCodeOK {
		return nil
	}
	if code == larkCodeProcessing || code == larkCodeQueued {
		return nil
	}
	if mtIsNotGranted(message) {
		return fmt.Errorf("%w: 妙记资源 volc.lark.minutes 未开通（上游 %s: %s）%s；请到火山引擎控制台开通「语音妙记」服务并等待生效",
			ErrNotGranted, code, message, logid)
	}
	switch code {
	case "20000003":
		return fmt.Errorf("妙记%s失败(%s): 静音音频，请更换有效音频后重试%s", op, code, logid)
	case "55000031":
		return fmt.Errorf("妙记%s失败(%s): 服务器繁忙，请稍后重试%s", op, code, logid)
	}
	if strings.HasPrefix(code, "45") {
		return fmt.Errorf("妙记%s被拒绝(%s): %s——请检查凭证与请求参数%s", op, code, message, logid)
	}
	return fmt.Errorf("妙记%s失败(%s): %s%s", op, code, message, logid)
}

// larkTaskErrMsg 任务级错误（Data.ErrCode）转中文：官方任务错误码表 6561/1798094。
func larkTaskErrMsg(errCode int, errMessage string) string {
	names := map[int]string{
		4004: "文件大小超限", 4801: "空音频", 4802: "文本格式错误", 4803: "不支持的语种",
		4804: "空文本", 4805: "空文件", 4806: "没有可用时长", 4807: "音频长度超限",
		4808: "不支持的音频格式", 4809: "url 无效", 4810: "下载超时", 4811: "下载错误",
		4812: "文件大小超限", 4813: "不支持的语种",
	}
	name, ok := names[errCode]
	if !ok {
		name = "任务处理失败"
	}
	if errMessage != "" {
		return fmt.Sprintf("%s(%d): %s", name, errCode, errMessage)
	}
	return fmt.Sprintf("%s(%d)", name, errCode)
}

// larkFileTypeByExt 由 URL 扩展名推断 FileType（audio/video）：官方视频格式
// MP4/AVI/MKV/MOV/FLV/WMV，音频 MP3/WAV/AAC/FLAC/OGG；无法识别时默认 audio。
func larkFileTypeByExt(rawURL string) string {
	u := rawURL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(u), ".")) {
	case "mp4", "avi", "mkv", "mov", "flv", "wmv":
		return "video"
	}
	return "audio"
}
