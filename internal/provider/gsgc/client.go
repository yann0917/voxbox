// Package gsgc 「格式工厂」工具集：既含本地媒体处理（internal/pkg/gsgc，逆向复刻的 16 个
// ffmpeg/图片功能，见 toolset.go），也含云端人声分离通道（本文件，z.pcgeshi.com）。
//
// 云端通道背景：转换猫（www.zhuanhuanmao.com）与格式工厂在线版（z.pcgeshi.com）是
// 同一后端的两块牌子（同火山 TOS 桶、同一任务 ID 序列、同一套任务接口），线路与模型
// 编号在此硬编码（无配置键）。通道匿名可用、无需凭证，协议为「预签名直传 TOS → 创建
// 分离任务 → 轮询 → 取产物直链」四步，产物原生为双轨 WAV（vocals/instrumental），voxbox 保存前转码标准 MP3。接口
// 为站点私有未公开契约，字段形态随站点版本有差异（info 字符串/对象、fileSize/file_size），
// 客户端一律宽松解析；非官方 API，上游随时可能变更或限流，定位为免费备用通道而非承重链路。
//
// 双线路注意：两站分离任务的 create_task 参数形态不同（2026-09-19 自转换猫前端 chunk
// 逆向确认）——格式工厂 stem 为数组 ["instrumental","vocals"] 且必带 model:"103"，
// 转换猫 stem 为字符串 "instrumental_vocals" 且不带 model；各线路按自家前端形态提交
// （见 site_tools.go 的 separateStemsTransform / zhmStemsTransform），不可混用。
package gsgc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

const (
	// DefaultBaseURL 云端分离线路（格式工厂在线版，接口形态较新），硬编码无配置键。
	DefaultBaseURL = "https://z.pcgeshi.com"

	// ZHMBaseURL 转换猫线路（同后端镜像站点，上传前缀 convertmao-web）。
	// 分离页第二 Tab / CLI 与 MCP 的 engine=zhuanhuanmao 走这条线路。
	ZHMBaseURL = "https://www.zhuanhuanmao.com"

	// DefaultModel 官方网页端 create_task 实测携带的模型编号。实测不携带（或 stem
	// 数组顺序偏离官方形态）时，上游会静默退化成只出人声单轨——故客户端固定默认
	// 值并按官方顺序提交，调用方一般不应覆写。
	DefaultModel = "103"

	fetchUploadURLPath = "/api/fetch_upload_url"
	createTaskPath     = "/api/create_task"
	batchGetPath       = "/api/tasks/batchGet"
	fetchDownloadPath  = "/api/fetch_download_url"

	// apiTimeout 常规 JSON 接口超时；uploadTimeout 文件直传 TOS；downloadTimeout
	// 产物下载（站点原生 WAV 大文件直链）。
	apiTimeout      = 30 * time.Second
	uploadTimeout   = 15 * time.Minute
	downloadTimeout = 10 * time.Minute

	// ua 浏览器 UA：站点按浏览器形态被调用，避免请求被简单 UA 规则过滤。
	ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

// Stem 轨道名（create_task 的 stem 数组元素与产物轨道同词表）。
const (
	StemVocals       = "vocals"
	StemInstrumental = "instrumental"
)

// Client 云端分离通道 REST 客户端（线路/模型硬编码）。
type Client struct {
	baseURL  string
	model    string // 上游模型目录编号（官方前端实测值 DefaultModel）
	resty    *resty.Client
	download *resty.Client
}

// New 构造客户端。线路与模型编号按 DefaultBaseURL/DefaultModel 硬编码
// （缺失 model 会导致上游静默退化单轨，故不再暴露配置）。
func New() *Client { return newWithBaseURL(DefaultBaseURL, DefaultModel) }

// NewForSite 按站点线路构造客户端（转换猫与格式工厂同协议，仅 API host 不同；
// 模型编号固定 DefaultModel——转换猫线路的 payload 不含 model，见 zhmStemsTransform）。
func NewForSite(baseURL string) *Client {
	return newWithBaseURL(normalizeBaseURL(baseURL), DefaultModel)
}

// newWithBaseURL 测试注入口：mock 线路需替换 baseURL；生产代码统一走 New()。
func newWithBaseURL(baseURL, model string) *Client {
	return &Client{
		baseURL:  baseURL,
		model:    model,
		resty:    resty.New().SetBaseURL(baseURL).SetTimeout(apiTimeout).SetHeader("User-Agent", ua),
		download: resty.New().SetTimeout(downloadTimeout).SetHeader("User-Agent", ua),
	}
}

func normalizeBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return DefaultBaseURL
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return u
}

// BaseURL 客户端实际使用的线路地址。
func (c *Client) BaseURL() string { return c.baseURL }

// ---- 上游信封 {code,status,message,data}：code=200 且 data 非空为成功；
// 失败文案可能在 message（参数错误）或 status（"系统异常"）----

type envelope struct {
	Code    int             `json:"code"`
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e envelope) err(op string) error {
	msg := e.Message
	if msg == "" {
		msg = e.Status
	}
	if msg == "" || msg == "error" {
		msg = fmt.Sprintf("code=%d", e.Code)
	}
	return fmt.Errorf("云端分离通道%s失败: %s", op, msg)
}

// post 取站点接口（POST JSON），成功返回 data 原始字节。
func (c *Client) post(ctx context.Context, op, path string, body any) (json.RawMessage, error) {
	resp, err := c.resty.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		Post(path)
	if err != nil {
		return nil, fmt.Errorf("云端分离通道%s失败: %w", op, err)
	}
	return checkResp(op, resp)
}

// get 取站点接口（GET 查询串），成功返回 data 原始字节。
func (c *Client) get(ctx context.Context, op, pathWithQuery string) (json.RawMessage, error) {
	resp, err := c.resty.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		Get(pathWithQuery)
	if err != nil {
		return nil, fmt.Errorf("云端分离通道%s失败: %w", op, err)
	}
	return checkResp(op, resp)
}

func checkResp(op string, resp *resty.Response) (json.RawMessage, error) {
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("云端分离通道%s失败(HTTP %d): %s", op, resp.StatusCode(), snippet(resp))
	}
	var env envelope
	if err := json.Unmarshal(resp.Body(), &env); err != nil {
		return nil, fmt.Errorf("解析云端分离通道%s响应失败: %w", op, err)
	}
	if env.Code != 200 || len(env.Data) == 0 {
		return nil, env.err(op)
	}
	return env.Data, nil
}

func snippet(resp *resty.Response) string {
	s := strings.TrimSpace(string(resp.Body()))
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…(截断)"
	}
	return s
}

// FetchUploadURL 申请输入文件预签名直传地址：fileName 仅用于确定对象扩展名，
// 返回的 inputPathID 是后续 create_task 的输入引用（不透明 token）。
func (c *Client) FetchUploadURL(ctx context.Context, fileName string) (uploadURL, inputPathID string, err error) {
	data, err := c.get(ctx, "申请上传地址", fetchUploadURLPath+"?fileName="+url.QueryEscape(fileName))
	if err != nil {
		return "", "", err
	}
	var raw struct {
		UploadURL   string `json:"upload_url"`
		InputPathID string `json:"input_path_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", "", fmt.Errorf("解析上传地址响应失败: %w", err)
	}
	if raw.UploadURL == "" || raw.InputPathID == "" {
		return "", "", fmt.Errorf("云端分离通道申请上传地址响应缺少 upload_url/input_path_id")
	}
	return raw.UploadURL, raw.InputPathID, nil
}

// UploadFile 直传输入文件到对象存储（预签名 PUT；签名 URL 已含全部鉴权，
// Content-Length 显式给出避免分块传输编码——TOS 签名 PUT 对部分网关更稳）。
func (c *Client) UploadFile(ctx context.Context, uploadURL, contentType string, r io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, r)
	if err != nil {
		return fmt.Errorf("上传音频到云端分离通道失败: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", ua)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("上传音频到云端分离通道失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("上传音频到云端分离通道失败(HTTP %d)", resp.StatusCode)
	}
	return nil
}

// CreateTask 创建任意类型的站点任务（payload 由调用方按 task_type 组装；
// 未设置的键不要放进 payload——上游以「缺键」为走默认，显式 null/空值行为未知）。
// 全站功能共用这一个入口：audio_separate / audio_converter / audio_compress /
// audio_denoise / video_converter / video_compress / video_process /
// image_converter / image_compress（2026-09-18 自站点前端逆向）。
func (c *Client) CreateTask(ctx context.Context, payload map[string]any) (string, error) {
	if payload["task_type"] == "" {
		return "", fmt.Errorf("站点任务 payload 缺少 task_type")
	}
	data, err := c.post(ctx, "创建任务", createTaskPath, payload)
	if err != nil {
		return "", err
	}
	var raw struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("解析创建任务响应失败: %w", err)
	}
	if raw.TaskID == "" {
		return "", fmt.Errorf("云端分离通道创建任务响应缺少 task_id")
	}
	return raw.TaskID, nil
}

// TaskInfo 轮询返回的单任务状态。Status 原样保留上游词表（已知 running → completed）。
// Progress/QueueTasks 用浮点承接：上游 progress 是百分比浮点（实测 26.08695652173913），
// 严格 int 解析会让整次轮询报错。
type TaskInfo struct {
	TaskID          string  `json:"task_id"`
	Status          string  `json:"status"`
	Progress        float64 `json:"progress"`
	ConsumeDuration float64 `json:"consume_duration"` // 上游计的音频时长（秒）
	QueueTasks      float64 `json:"queue_tasks"`
}

// BatchGet 批量查询任务状态（本工具单任务使用）。
func (c *Client) BatchGet(ctx context.Context, taskIDs ...string) ([]TaskInfo, error) {
	data, err := c.post(ctx, "查询任务状态", batchGetPath, map[string]any{"task_id": taskIDs})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析任务状态响应失败: %w", err)
	}
	return raw.Tasks, nil
}

// OutputFile 产物单轨直链：Stem 已从上游两种 info 形态（字符串 "vocals" 与
// 对象 {"stem":"vocals"}）归一；Size 兼容 file_size/fileSize 两种键名。
type OutputFile struct {
	URL  string
	Stem string
	Size int64
}

// FetchDownloadURL 取任务产物直链（预签名 URL，上游有效期约 1 小时，取到尽快下载）。
func (c *Client) FetchDownloadURL(ctx context.Context, taskID string) ([]OutputFile, error) {
	data, err := c.get(ctx, "获取产物地址",
		fetchDownloadPath+"?task_id="+url.QueryEscape(taskID))
	if err != nil {
		return nil, err
	}
	var flex []struct {
		URL       string          `json:"url"`
		FileSize  json.RawMessage `json:"file_size"`
		FileSize2 json.RawMessage `json:"fileSize"`
		Info      json.RawMessage `json:"info"`
	}
	if err := json.Unmarshal(data, &flex); err != nil {
		return nil, fmt.Errorf("解析产物地址响应失败: %w", err)
	}
	out := make([]OutputFile, 0, len(flex))
	for _, f := range flex {
		if f.URL == "" {
			continue
		}
		out = append(out, OutputFile{
			URL:  f.URL,
			Stem: stemOf(f.Info),
			Size: flexInt64(f.FileSize, f.FileSize2),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("云端分离通道任务已完成但产物列表为空")
	}
	return out, nil
}

// stemOf 产物轨道名：info 为字符串或 {"stem":…} 对象两种形态统一取值。
func stemOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Stem string `json:"stem"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Stem
	}
	return ""
}

// flexInt64 依次尝试解析（上游数字/字符串两种形态实测并存）；全失败返回 0。
func flexInt64(raws ...json.RawMessage) int64 {
	for _, raw := range raws {
		if len(raw) == 0 {
			continue
		}
		var n int64
		if json.Unmarshal(raw, &n) == nil {
			return n
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if json.Unmarshal([]byte(s), &n) == nil {
				return n
			}
		}
	}
	return 0
}

// DownloadToFile 流式下载产物直链到本地（.part 中转，成功才改名），返回字节数。
// 失败态清理半截文件（resty SetOutput 无论状态码都会落盘）。
func (c *Client) DownloadToFile(ctx context.Context, rawURL, dst string) (int64, error) {
	resp, err := c.download.R().SetContext(ctx).SetOutput(dst).Get(rawURL)
	if err != nil {
		_ = os.Remove(dst)
		return 0, fmt.Errorf("下载分离产物失败: %w", err)
	}
	if !resp.IsSuccess() {
		_ = os.Remove(dst)
		return 0, fmt.Errorf("下载分离产物失败(HTTP %d)", resp.StatusCode())
	}
	return resp.Size(), nil
}
