// Package deepseek DeepSeek 开放平台（api.deepseek.com）provider：目前仅承载 AI 助手的
// 文本大模型（POST /responses，OpenAI Responses 风格流式协议）与联网工具（网页搜索
// /v1/tools/search、网页抓取 /v1/tools/fetch），无语音能力，故不注册任务工具、不进
// 工具注册表。Bearer API Key 鉴权，BaseURL 固定官方。
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseURL DeepSeek 开放平台固定入口，不提供覆写。/responses 与 /v1/tools/* 同域同鉴权。
const BaseURL = "https://api.deepseek.com"

// 官方路径（assistant 层的流式对话直连 /responses，不经本包客户端）。
const (
	PathModels    = "/models"
	PathWebSearch = "/v1/tools/search"
	PathWebFetch  = "/v1/tools/fetch"
)

// ErrNoCred 凭证未配置哨兵：CLI 退出码与 Web 业务码按此映射为 4（配置类问题，重试无意义），
// 与 qianwen/xiaomi/zhipu/minimax.ErrNoCred 同款模式。assistant 层以 %w 包装并附配置指引。
var ErrNoCred = errors.New("DeepSeek API Key 未配置")

// SearchQueryMax 搜索词长度上限（官方 1~128 个 Unicode 字符）。
const SearchQueryMax = 128

// Client DeepSeek 开放平台客户端（搜索/抓取/探活）。baseURL 可注入以便测试。
type Client struct{ apiKey, baseURL string }

// NewClient 构造客户端；baseURL 传空回落官方入口。
func NewClient(apiKey, baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}
	return &Client{apiKey: apiKey, baseURL: baseURL}
}

// SearchResult 网页搜索单条结果（/v1/tools/search 响应字段，按官方相关度排序返回）。
type SearchResult struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Snippet     string  `json:"snippet"`
	Summary     string  `json:"summary"`
	SiteName    *string `json:"site_name"`
	Date        *string `json:"date"` // RFC 3339 UTC
	Authority   int     `json:"authority"`
	RerankScore float64 `json:"rerank_score"`
}

// WebSearch 联网搜索：query 截断到官方上限，count 钳制到 1~10（官方 1~50，助手场景
// 取小值控制延迟——按成功请求计费，条数不影响价格）；oriQuestion 为用户原始问题
// （帮助理解搜索意图，超限截断）。need_content 恒 false：summary（约 600~800 字符）
// 足够模型阅读，正文拉全会显著增大延迟。
func (c *Client) WebSearch(ctx context.Context, query string, count int, oriQuestion string) ([]SearchResult, error) {
	query = truncateRunes(strings.TrimSpace(query), SearchQueryMax)
	if query == "" {
		return nil, fmt.Errorf("搜索词不能为空")
	}
	if count < 1 {
		count = 8
	}
	if count > 10 {
		count = 10
	}
	body := map[string]any{"query": query, "count": count, "need_content": false}
	if oriQuestion = truncateRunes(strings.TrimSpace(oriQuestion), SearchQueryMax); oriQuestion != "" {
		body["ori_question"] = oriQuestion
	}
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodPost, PathWebSearch, body, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// WebFetch 抓取网页正文（Markdown）。url 必须为含 host 的 http/https 地址。
func (c *Client) WebFetch(ctx context.Context, url string) (title, content string, err error) {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", "", fmt.Errorf("仅支持 http/https 网页地址")
	}
	var resp struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := c.doJSON(ctx, http.MethodPost, PathWebFetch, map[string]any{"url": url}, &resp); err != nil {
		return "", "", err
	}
	return resp.Title, resp.Content, nil
}

// ProbeModels 连通性探测：GET /models 只读免费接口，验证鉴权有效性（异于语音平台的
// 消耗型探活）。
func (c *Client) ProbeModels(ctx context.Context) error {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	return c.doJSON(ctx, http.MethodGet, PathModels, nil, &resp)
}

// doJSON 统一请求出口：Bearer 鉴权 + JSON 编解码；非 2xx 按官方 OpenAI 风格错误体转译。
// 各端点均为同步请求：搜索秒级返回，网页抓取随页面体积增长，60s 足够。
func (c *Client) doJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	var reader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(body, resp.StatusCode)
	}
	if respBody == nil {
		return nil
	}
	if err := json.Unmarshal(body, respBody); err != nil {
		return fmt.Errorf("解析 DeepSeek 响应失败: %w", err)
	}
	return nil
}

// decodeError 官方错误体为 OpenAI 风格 {"error":{"message","type","code"}}；
// 无错误详情时按 HTTP 状态 + 截断原文呈现。
func decodeError(body []byte, status int) error {
	var shape struct {
		Err *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &shape)
	if shape.Err != nil && shape.Err.Message != "" {
		return fmt.Errorf("DeepSeek API 错误: %s", shape.Err.Message)
	}
	if len(body) > 200 {
		body = body[:200]
	}
	if len(body) == 0 {
		return fmt.Errorf("DeepSeek API HTTP %d", status)
	}
	return fmt.Errorf("DeepSeek API HTTP %d: %s", status, body)
}

// truncateRunes 按 rune 截断字符串。
func truncateRunes(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max])
}
