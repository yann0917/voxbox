// Package mvsep 对接 MVSep（mvsep.com，音乐源分离社区服务）：免费账号每天 50 次
// 分离额度。凭证仅一个 api_token（查询参数 api_token 传递）；分离任务为
// 上传文件 → 轮询 hash → 下载多轨产物三步。区域镜像（de/de2/hk）只服务自己
// 接下的任务，故客户端生命周期内固定一个 baseURL。
package mvsep

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

const (
	// DefaultBaseURL 主站（geo 按客户端位置就近调度，亚洲流量落 hk 节点）。
	DefaultBaseURL = "https://mvsep.com"
	// algorithmsPath 算法列表（带 api_token 时返回嵌套 algorithm_group 名称）。
	algorithmsPath = "/api/app/algorithms"
	userPath       = "/api/app/user"
	queuePath      = "/api/app/queue"
	historyPath    = "/api/app/separation_history"
	createPath     = "/api/separation/create"
	getPath        = "/api/separation/get"
	cancelPath     = "/api/separation/cancel"

	// apiTimeout 常规 JSON 接口超时；uploadTimeout 提交（含 50MB 级文件上传）；
	// downloadTimeout 产物/输入下载（产物为大文件直链）。
	apiTimeout      = 30 * time.Second
	uploadTimeout   = 15 * time.Minute
	downloadTimeout = 10 * time.Minute
)

// ErrAuth token 无效或未授权（上游 401）；ErrNoCred 凭证未配置。
var (
	ErrAuth   = fmt.Errorf("MVSep 凭证无效")
	ErrNoCred = fmt.Errorf("MVSep 凭证未配置")
)

// AlgorithmField 算法附加参数定义：name 即提交时的表单键（add_opt1/add_opt2/…），
// Options 为 key→展示名映射（上游把选项表编码成 JSON 字符串，已在此处解析为 map）。
type AlgorithmField struct {
	Name       string            `json:"name"`
	Text       string            `json:"text"`
	Options    map[string]string `json:"options"`
	DefaultKey string            `json:"default_key"`
	InputType  string            `json:"input_type"`
	Required   bool              `json:"required"`
}

// Algorithm 算法（分离类型）：SepType 即提交 separation/create 的 sep_type
// （取上游 render_id，缺失回落 id）；Orientation >0 表示高级算法（可能消耗积分/仅高级账号）。
type Algorithm struct {
	SepType          int              `json:"sep_type"`
	ID               int              `json:"id"`
	Name             string           `json:"name"`
	GroupID          int              `json:"group_id"`
	GroupName        string           `json:"group_name"`
	PriceCoefficient float64          `json:"price_coefficient"`
	Orientation      int              `json:"orientation"`
	IsActive         bool             `json:"is_active"`
	Description      string           `json:"description"`
	Fields           []AlgorithmField `json:"fields"`
}

// User 账户信息（GET /api/app/user）。
type User struct {
	Name           string `json:"name"`
	Email          string `json:"email"`
	PremiumMinutes int    `json:"premium_minutes"`
	PremiumEnabled bool   `json:"premium_enabled"`
}

// QueueStatus 站点队列与免费额度（GET /api/app/queue）：FreeSeparations 即
// 每日免费分离次数余额（left/max，免费账号 50/天）。
type QueueStatus struct {
	InProcess    int    `json:"in_process"`
	Registered   int    `json:"registered"`
	Unregistered int    `json:"unregistered"`
	Plan         string `json:"plan"`
	FreeLeft     int    `json:"free_left"`
	FreeMax      int    `json:"free_max"`
}

// HistoryItem 分离历史条目（GET /api/app/separation_history）。
type HistoryItem struct {
	ID        int    `json:"id"`
	Hash      string `json:"hash"`
	CreatedAt string `json:"created_at"`
	JobExists bool   `json:"job_exists"`
	Algorithm string `json:"algorithm"`
}

// OutputFile 分离产物单轨直链（hash 查询 done 态返回；链接公开可下载）。
type OutputFile struct {
	Name string `json:"name"`
	Link string `json:"link"`
	Size int64  `json:"size"`
}

// SeparationResult done 态结果：算法名、输出格式与逐轨文件。
type SeparationResult struct {
	Algorithm    string       `json:"algorithm"`
	OutputFormat string       `json:"output_format"`
	Files        []OutputFile `json:"files"`
}

// Client MVSep REST 客户端。token 为空时仅 Algorithms/Queue 可用（上游两个接口
// 免鉴权；带 token 调用 algorithms 可拿到分组名，故有 token 时始终携带）。
type Client struct {
	baseURL  string
	token    string
	resty    *resty.Client
	upload   *resty.Client
	download *resty.Client
}

// New 构造客户端；baseURL 留空走 DefaultBaseURL。
func New(token, baseURL string) *Client {
	u := normalizeBaseURL(baseURL)
	return &Client{
		baseURL:  u,
		token:    token,
		resty:    resty.New().SetBaseURL(u).SetTimeout(apiTimeout),
		upload:   resty.New().SetBaseURL(u).SetTimeout(uploadTimeout),
		download: resty.New().SetTimeout(downloadTimeout),
	}
}

func normalizeBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return DefaultBaseURL
	}
	return u
}

// BaseURL 客户端实际使用的线路地址。
func (c *Client) BaseURL() string { return c.baseURL }

// ---- 上游响应原始结构（options/render_id/required 类型不规则，先按原始形态收） ----

type rawField struct {
	Name       string          `json:"name"`
	Text       string          `json:"text"`
	Options    json.RawMessage `json:"options"` // JSON 字符串或对象两种形态都见过
	DefaultKey string          `json:"default_key"`
	InputType  string          `json:"input_type"`
	Required   json.RawMessage `json:"required"` // 0/1 数字或布尔
}

type rawAlgorithm struct {
	ID             *int   `json:"id"`
	RenderID       *int   `json:"render_id"`
	Name           string `json:"name"`
	AlgorithmGroup *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"algorithm_group"`
	AlgorithmGroupID *int       `json:"algorithm_group_id"`
	PriceCoefficient *float64   `json:"price_coefficient"`
	Orientation      *int       `json:"orientation"`
	IsActive         *int       `json:"is_active"`
	Description      string     `json:"description"`
	AlgorithmFields  []rawField `json:"algorithm_fields"`
}

// Algorithms 拉取算法列表：有 token 时携带（响应含 algorithm_group 名称；无 token
// 只返回分组 id）。GET /api/app/algorithms 免鉴权、限频 60/分钟，调用方自行缓存。
func (c *Client) Algorithms(ctx context.Context) ([]Algorithm, error) {
	resp, err := c.resty.R().SetContext(ctx).SetQueryParam("api_token", c.token).Get(algorithmsPath)
	if err != nil {
		return nil, fmt.Errorf("获取 MVSep 算法列表失败: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("获取 MVSep 算法列表失败(HTTP %d): %s", resp.StatusCode(), bodySnippet(resp))
	}
	var raw []rawAlgorithm
	if err := json.Unmarshal(resp.Body(), &raw); err != nil {
		return nil, fmt.Errorf("解析 MVSep 算法列表失败: %w", err)
	}
	out := make([]Algorithm, 0, len(raw))
	for _, r := range raw {
		out = append(out, convertAlgorithm(r))
	}
	return out, nil
}

func convertAlgorithm(r rawAlgorithm) Algorithm {
	a := Algorithm{Name: r.Name, Description: r.Description,
		IsActive: r.IsActive == nil || *r.IsActive == 1, Fields: []AlgorithmField{}} // Fields 保非 nil：上游 algorithm_fields 可为 null，前端契约避免 null 数组
	if r.RenderID != nil {
		a.SepType = *r.RenderID
	}
	if r.ID != nil {
		a.ID = *r.ID
		if a.SepType == 0 {
			a.SepType = *r.ID
		}
	}
	if r.AlgorithmGroup != nil {
		a.GroupID, a.GroupName = r.AlgorithmGroup.ID, r.AlgorithmGroup.Name
	} else if r.AlgorithmGroupID != nil {
		a.GroupID = *r.AlgorithmGroupID
	}
	if r.PriceCoefficient != nil {
		a.PriceCoefficient = *r.PriceCoefficient
	}
	if r.Orientation != nil {
		a.Orientation = *r.Orientation
	}
	for _, f := range r.AlgorithmFields {
		a.Fields = append(a.Fields, AlgorithmField{
			Name:       f.Name,
			Text:       f.Text,
			Options:    parseFieldOptions(f.Options),
			DefaultKey: f.DefaultKey,
			InputType:  f.InputType,
			Required:   parseFieldRequired(f.Required),
		})
	}
	return a
}

// parseFieldOptions 上游 options 有两种形态：JSON 编码的字符串（实测如此）与内联对象。
func parseFieldOptions(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var asStr string
	if json.Unmarshal(raw, &asStr) == nil && asStr != "" {
		raw = []byte(asStr)
	}
	m := map[string]string{}
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func parseFieldRequired(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var n int
	return json.Unmarshal(raw, &n) == nil && n != 0
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// User 拉取账户信息（GET /api/app/user，需 token）：兼作 token 有效性探测。
func (c *Client) User(ctx context.Context) (*User, error) {
	if c.token == "" {
		return nil, fmt.Errorf("未配置 MVSep API Token：请在设置页填写 mvsep.api_token")
	}
	body, err := c.getJSON(ctx, userPath, "查询账户信息")
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("解析 MVSep 账户信息失败: %w", err)
	}
	var raw struct {
		Name           string `json:"name"`
		Email          string `json:"email"`
		PremiumMinutes int    `json:"premium_minutes"`
		PremiumEnabled any    `json:"premium_enabled"` // 上游为 0/1 数字
	}
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return nil, fmt.Errorf("解析 MVSep 账户信息失败: %w", err)
	}
	return &User{
		Name: raw.Name, Email: raw.Email, PremiumMinutes: raw.PremiumMinutes,
		PremiumEnabled: flexBool(raw.PremiumEnabled),
	}, nil
}

// flexBool 宽松布尔解析：上游布尔字段有 true/false、0/1 数字与 "1" 字符串三种形态。
func flexBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		return b == "1" || strings.EqualFold(b, "true")
	}
	return false
}

// anyBytes any → json.RawMessage 适配（结构体匿名字段用 RawMessage 更繁琐）。
func anyBytes(v any) json.RawMessage {
	switch b := v.(type) {
	case json.RawMessage:
		return b
	case nil:
		return nil
	}
	out, _ := json.Marshal(v)
	return out
}

// flexInt64 宽松整数解析（上游实测同字段有数字与字符串两种形态）；解析失败返回 0。
func flexInt64(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		_ = json.Unmarshal([]byte(s), &n)
	}
	return n
}

// Queue 拉取站点队列与免费额度（GET /api/app/queue；带 token 显示账号所在套餐队列）。
func (c *Client) Queue(ctx context.Context) (*QueueStatus, error) {
	body, err := c.getJSON(ctx, queuePath, "查询队列状态")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Queue struct {
			InProcess    int `json:"in_process"`
			Registered   int `json:"registered"`
			Unregistered int `json:"unregistered"`
		} `json:"queue"`
		Current struct {
			Plan  string `json:"plan"`
			Queue int    `json:"queue"`
		} `json:"current"`
		FreeSeparations *struct {
			Left int `json:"left"`
			Max  int `json:"max"`
		} `json:"free_separations"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 MVSep 队列状态失败: %w", err)
	}
	qs := &QueueStatus{
		InProcess:    raw.Queue.InProcess,
		Registered:   raw.Queue.Registered,
		Unregistered: raw.Queue.Unregistered,
		Plan:         raw.Current.Plan,
	}
	if raw.FreeSeparations != nil {
		qs.FreeLeft, qs.FreeMax = raw.FreeSeparations.Left, raw.FreeSeparations.Max
	}
	return qs, nil
}

// History 拉取分离历史（GET /api/app/separation_history，需 token）。
func (c *Client) History(ctx context.Context, start, limit int) ([]HistoryItem, error) {
	if c.token == "" {
		return nil, fmt.Errorf("未配置 MVSep API Token：请在设置页填写 mvsep.api_token")
	}
	if start < 0 {
		start = 0
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	body, err := c.getJSON(ctx, fmt.Sprintf("%s?start=%d&limit=%d", historyPath, start, limit), "查询分离历史")
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("解析 MVSep 分离历史失败: %w", err)
	}
	var raw []struct {
		ID        int    `json:"id"`
		Hash      string `json:"hash"`
		CreatedAt string `json:"created_at"`
		// job_exists 上游布尔形态不稳定（实测 true/1 都有），宽松解析
		JobExists any `json:"job_exists"`
		Algorithm *struct {
			Name string `json:"name"`
		} `json:"algorithm"`
	}
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return nil, fmt.Errorf("解析 MVSep 分离历史失败: %w", err)
	}
	items := make([]HistoryItem, 0, len(raw))
	for _, r := range raw {
		item := HistoryItem{ID: r.ID, Hash: r.Hash, CreatedAt: r.CreatedAt, JobExists: flexBool(r.JobExists)}
		if r.Algorithm != nil {
			item.Algorithm = r.Algorithm.Name
		}
		items = append(items, item)
	}
	return items, nil
}

// CreateInput 创建分离任务的输入：Reader 由调用方打开并在 Create 返回后关闭。
type CreateInput struct {
	Filename     string
	Reader       io.Reader
	Size         int64
	SepType      int
	AddOpts      map[string]string // add_opt1/add_opt2/… → 选项 key（仅非空提交）
	OutputFormat int               // 0=mp3 1=wav16 2=wav24 3=wav32f 4=wav32 5=flac
	IsDemo       bool
}

// Create 提交分离任务（POST /api/separation/create，multipart 上传）：成功返回任务 hash。
// 上传大文件耗时较长，调用方可用 ctx 取消；进度心跳由工具层负责。
func (c *Client) Create(ctx context.Context, in CreateInput) (string, error) {
	if c.token == "" {
		return "", fmt.Errorf("未配置 MVSep API Token：请在设置页填写 mvsep.api_token")
	}
	if in.Reader == nil || in.Filename == "" {
		return "", fmt.Errorf("缺少音频文件")
	}
	if in.SepType <= 0 {
		return "", fmt.Errorf("缺少分离类型 sep_type")
	}
	form := map[string]string{
		"api_token":     c.token,
		"sep_type":      strconv.Itoa(in.SepType),
		"output_format": strconv.Itoa(in.OutputFormat),
		"is_demo":       map[bool]string{true: "1", false: "0"}[in.IsDemo],
	}
	for k, v := range in.AddOpts {
		if strings.TrimSpace(v) != "" {
			form[k] = v
		}
	}
	resp, err := c.upload.R().
		SetContext(ctx).
		SetFormData(form).
		SetFileReader("audiofile", in.Filename, in.Reader).
		Post(createPath)
	if err != nil {
		return "", fmt.Errorf("提交 MVSep 分离任务失败: %w", err)
	}
	if err := checkHTTP("提交分离任务", resp); err != nil {
		return "", err
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Hash string `json:"hash"`
			Link string `json:"link"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &env); err != nil {
		return "", fmt.Errorf("解析 MVSep 提交响应失败: %w", err)
	}
	if !env.Success || env.Data.Hash == "" {
		msg := env.Message
		if msg == "" {
			msg = bodySnippet(resp)
		}
		return "", fmt.Errorf("MVSep 提交分离任务失败: %s", msg)
	}
	return env.Data.Hash, nil
}

// Get 查询任务状态与结果（GET /api/separation/get，免鉴权）：
// status: waiting|converting|processing|distributing|merging → 未完成（err nil，result nil）；
// done → 解析逐轨文件；failed/not_found → 任务终态错误（err 非 nil，不可重试）。
func (c *Client) Get(ctx context.Context, hash string) (string, *SeparationResult, error) {
	resp, err := c.resty.R().SetContext(ctx).SetQueryParam("hash", hash).Get(getPath)
	if err != nil {
		return "", nil, fmt.Errorf("查询 MVSep 分离任务失败: %w", err)
	}
	if !resp.IsSuccess() {
		return "", nil, fmt.Errorf("查询 MVSep 分离任务失败(HTTP %d): %s", resp.StatusCode(), bodySnippet(resp))
	}
	var raw struct {
		Success bool   `json:"success"`
		Status  string `json:"status"`
		Data    struct {
			Algorithm    string `json:"algorithm"`
			OutputFormat string `json:"output_format"`
			Message      string `json:"message"`
			Files        []struct {
				Type  string `json:"type"` // 轨道名（如 Vocals/Instrum/Other）
				URL   string `json:"url"`  // 产物直链（所在区域节点）
				Name  string `json:"name"` // 旧版字段名，保底兼容
				Link  string `json:"link"`
				Bytes any    `json:"bytes"` // 字节数（数字）；size 字段是 "159.23 KB" 人类可读串
				Size  any    `json:"size"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &raw); err != nil {
		return "", nil, fmt.Errorf("解析 MVSep 任务状态失败: %w", err)
	}
	status := raw.Status
	if status == "" && !raw.Success {
		status = "not_found"
	}
	switch status {
	case "done":
		res := &SeparationResult{Algorithm: raw.Data.Algorithm, OutputFormat: raw.Data.OutputFormat}
		for _, f := range raw.Data.Files {
			name, link, size := f.Type, f.URL, flexInt64(anyBytes(f.Bytes))
			if name == "" {
				name = f.Name
			}
			if link == "" {
				link = f.Link
			}
			if size == 0 {
				size = flexInt64(anyBytes(f.Size))
			}
			if name == "" {
				// 轨道名兜底：取直链文件名
				name = link[strings.LastIndex(link, "/")+1:]
			}
			res.Files = append(res.Files, OutputFile{Name: name, Link: link, Size: size})
		}
		if len(res.Files) == 0 {
			return status, nil, fmt.Errorf("MVSep 分离完成但结果文件为空")
		}
		return status, res, nil
	case "failed":
		msg := raw.Data.Message
		if msg == "" {
			msg = "上游处理失败"
		}
		return status, nil, fmt.Errorf("MVSep 分离任务失败：%s", msg)
	case "not_found":
		return status, nil, fmt.Errorf("MVSep 任务不存在或已过期：%s", hash)
	}
	return status, nil, nil
}

// Cancel 取消排队中的任务（POST /api/separation/cancel，退回当日额度）；
// 已在处理的任务上游会拒绝，调用方按 best-effort 语义忽略失败。
func (c *Client) Cancel(ctx context.Context, hash string) error {
	if c.token == "" {
		return fmt.Errorf("未配置 MVSep API Token")
	}
	resp, err := c.upload.R().
		SetContext(ctx).
		SetFormData(map[string]string{"api_token": c.token, "hash": hash}).
		Post(cancelPath)
	if err != nil {
		return err
	}
	return checkHTTP("取消分离任务", resp)
}

// Download 下载产物直链（公开链接，不带 token）：产物文件较大，单独放宽超时。
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := c.download.R().SetContext(ctx).Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("下载 MVSep 分离产物失败: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("下载 MVSep 分离产物失败(HTTP %d): %s", resp.StatusCode(), bodySnippet(resp))
	}
	return resp.Body(), nil
}

// DownloadToFile 与 Download 相同语义，但流式落盘（输入音频可能几十 MB，不驻留内存）。
func (c *Client) DownloadToFile(ctx context.Context, rawURL, dst string) (int64, error) {
	resp, err := c.download.R().SetContext(ctx).SetOutput(dst).Get(rawURL)
	if err != nil {
		return 0, fmt.Errorf("下载音频失败: %w", err)
	}
	if !resp.IsSuccess() {
		return 0, fmt.Errorf("下载音频失败(HTTP %d): %s", resp.StatusCode(), bodySnippet(resp))
	}
	return resp.Size(), nil
}

// getJSON 带 token 的 GET JSON 通用封装（user/history 等鉴权接口）。
func (c *Client) getJSON(ctx context.Context, pathWithQuery, op string) ([]byte, error) {
	resp, err := c.resty.R().
		SetContext(ctx).
		SetQueryParam("api_token", c.token).
		Get(pathWithQuery)
	if err != nil {
		return nil, fmt.Errorf("%s失败: %w", op, err)
	}
	if err := checkHTTP(op, resp); err != nil {
		return nil, err
	}
	return resp.Body(), nil
}

// checkHTTP HTTP 状态校验：2xx 通过；401 → ErrAuth（token 无效）；其余非 2xx → 中文错误含摘要。
func checkHTTP(op string, resp *resty.Response) error {
	if resp.IsSuccess() {
		return nil
	}
	code := resp.StatusCode()
	if code == 401 || code == 403 {
		return fmt.Errorf("%w: MVSep %s被拒绝(HTTP %d)，请检查 mvsep.api_token", ErrAuth, op, code)
	}
	return fmt.Errorf("MVSep %s失败(HTTP %d): %s", op, code, bodySnippet(resp))
}

// bodySnippet 响应 body 摘要（按 rune 截断，避免错误文案过长或截出坏字符）。
func bodySnippet(resp *resty.Response) string {
	s := strings.TrimSpace(string(resp.Body()))
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…(截断)"
	}
	return s
}
