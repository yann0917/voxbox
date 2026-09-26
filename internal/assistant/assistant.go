// Package assistant 悬浮 AI 助手的大模型对话客户端：智谱/千问/小米三家共用 OpenAI 兼容
// chat/completions 流式协议，凭证直接复用各语音平台的 API Key（config.yaml 同名段），
// 不新增设置卡。模型目录是后端单一事实来源：前端渲染与 chat 的 model 白名单都以此为准。
package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
)

// Provider 平台标识：与语音 provider、config.yaml 键、凭证卡同名，凭证一一对应。
type Provider string

const (
	ProviderZhipu   Provider = "zhipu"
	ProviderQianwen Provider = "qianwen"
	ProviderXiaomi  Provider = "xiaomi"
)

// Model 平台下的可选模型。
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Platform 模型目录的平台分组；Enabled 随当前凭证状态标注，前端据此禁用未配置平台的模型组。
type Platform struct {
	Provider Provider `json:"provider"`
	Label    string   `json:"label"`
	Enabled  bool     `json:"enabled"`
	Models   []Model  `json:"models"`
}

// catalog 模型清单（2026-09-26 真机 /models 列表校准）：每平台两款，快档在前作默认。
var catalog = []Platform{
	{Provider: ProviderZhipu, Label: "智谱", Models: []Model{
		{ID: "glm-5.3-flash", Label: "GLM-5.3 Flash"},
		{ID: "glm-5.3", Label: "GLM-5.3"},
	}},
	{Provider: ProviderQianwen, Label: "千问", Models: []Model{
		{ID: "qwen3.8-flash", Label: "Qwen3.8 Flash"},
		{ID: "qwen3.8-max", Label: "Qwen3.8 Max"},
	}},
	{Provider: ProviderXiaomi, Label: "小米", Models: []Model{
		{ID: "mimo-v2.6-flash", Label: "MiMo v2.6 Flash"},
		{ID: "mimo-v2.6-pro", Label: "MiMo v2.6 Pro"},
	}},
}

// CatalogWith 按当前配置标注各平台凭证可用性后的模型目录。
func CatalogWith(cfg *config.Config) []Platform {
	out := make([]Platform, len(catalog))
	for i, p := range catalog {
		p.Enabled = apiKeyOf(cfg, p.Provider) != ""
		out[i] = p
	}
	return out
}

// Label 平台显示名；未知平台原样返回标识。
func Label(p Provider) string {
	for _, c := range catalog {
		if c.Provider == p {
			return c.Label
		}
	}
	return string(p)
}

// KnownProvider 是否为目录内平台。
func KnownProvider(p Provider) bool {
	for _, c := range catalog {
		if c.Provider == p {
			return true
		}
	}
	return false
}

// apiKeyOf 平台 API Key（config 原子快照，与语音工具同源）。
func apiKeyOf(cfg *config.Config, p Provider) string {
	switch p {
	case ProviderZhipu:
		return cfg.Zhipu.APIKey
	case ProviderQianwen:
		return cfg.Qianwen.APIKey
	case ProviderXiaomi:
		return cfg.Xiaomi.APIKey
	}
	return ""
}

// errNoCredOf 未配置凭证：包装各平台哨兵（failErr 据此映射业务码 4），附配置指引。
func errNoCredOf(p Provider) error {
	guide := fmt.Sprintf("请在设置页或 voxbox config set %s.api_key 配置", p)
	switch p {
	case ProviderZhipu:
		return fmt.Errorf("%w：%s", zhipu.ErrNoCred, guide)
	case ProviderQianwen:
		return fmt.Errorf("%w：%s", qianwen.ErrNoCred, guide)
	case ProviderXiaomi:
		return fmt.Errorf("%w：%s", xiaomi.ErrNoCred, guide)
	}
	return fmt.Errorf("平台 %s 不支持大模型对话", p)
}

// CheckCredential 预检平台凭证，未配置时返回可映射业务码 4 的哨兵错误。
func CheckCredential(cfg *config.Config, p Provider) error {
	if apiKeyOf(cfg, p) == "" {
		return errNoCredOf(p)
	}
	return nil
}

// ModelAllowed model 是否属于该平台的目录白名单（防任意模型参数注入）。
func ModelAllowed(p Provider, model string) bool {
	for _, c := range catalog {
		if c.Provider != p {
			continue
		}
		for _, m := range c.Models {
			if m.ID == model {
				return true
			}
		}
	}
	return false
}

// endpoint 各平台 OpenAI 兼容 chat/completions 入口：智谱/小米与语音调用同域同鉴权；
// 千问走 DashScope 兼容模式（maas.qianwenaiapi.com/compatible-mode，2026-09-26 真机校准）。
func endpoint(p Provider) string {
	switch p {
	case ProviderZhipu:
		return zhipu.BaseURL + "/paas/v4/chat/completions"
	case ProviderQianwen:
		return qianwen.BaseURL + "/compatible-mode/v1/chat/completions"
	case ProviderXiaomi:
		return xiaomi.BaseURL + "/v1/chat/completions"
	}
	return ""
}

// systemPrompt 服务端注入的系统提示：限定助手角色与产品边界，客户端不可覆盖。
const systemPrompt = "你是 voxbox 的内置 AI 助手。voxbox 是一个多引擎语音工作台，提供语音合成、语音识别、" +
	"人声分离、播客生成、音频后期、音频剪辑、机器翻译、语音妙记、字幕工坊等工具，已接入火山引擎、千问、小米、智谱平台。" +
	"回答默认使用简体中文，简洁直接；涉及 voxbox 使用的问题给出具体页面路径；不确定的功能不要编造。"

// Message 对话消息（客户端只允许 user/assistant 两种角色，system 由服务端注入）。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// apiClient 不设整体超时：流式响应生命周期由请求上下文控制（服务端硬上限 + 客户端断开取消）。
var apiClient = &http.Client{}

// chatRequest OpenAI 兼容请求体。
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	// EnableThinking 关闭思考模式（千问兼容模式 wire，2026-09-26 真机校准）：悬浮助手以
	// 快答为先。仅千问置位；智谱/小米的同类参数未经真机验证，不发送。
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// streamChunk OpenAI 兼容流式分片；Err 承接个别平台在流内下发的错误体。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Err *apiErrorBody `json:"error"`
}

// apiErrorBody 三家错误体同形 {"error":{"code","message"}}；code 字符串/数字不定 → any。
type apiErrorBody struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

// Stream 发起流式对话，逐段回调增量正文（delta.content；reasoning_content 思考通道不回调，
// 思考期表现为短暂等待）。messages 由调用方完成角色与长度约束，system 提示在此统一注入。
// 整体硬上限 3 分钟：正常问答远低于此，上游卡死时兜底。
func Stream(ctx context.Context, cfg *config.Config, p Provider, model string, messages []Message, onDelta func(string)) error {
	if err := CheckCredential(cfg, p); err != nil {
		return err
	}
	if !ModelAllowed(p, model) {
		return fmt.Errorf("平台 %s 不支持模型 %s", Label(p), model)
	}
	raw, err := json.Marshal(buildChatRequest(p, model, messages))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(p), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKeyOf(cfg, p))
	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求%s平台失败: %w", Label(p), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return decodeProviderError(p, body, resp.StatusCode)
	}
	return scanStream(ctx, p, resp.Body, onDelta)
}

// scanStream 逐行解析 SSE：正常 data 行转增量回调，[DONE] 终止。个别平台对流式请求
// 直接回 JSON 错误体（无 data: 行，如余额不足）——把非 SSE 行累积起来，流结束后按错误体解析。
func scanStream(ctx context.Context, p Provider, r io.Reader, onDelta func(string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var rawLines []string
	sawData := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			if strings.TrimSpace(line) != "" {
				rawLines = append(rawLines, line)
			}
			continue
		}
		sawData = true
		rawLines = nil
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "[DONE]" {
			return nil
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue // 心跳/注释等无法解析的行直接忽略
		}
		if chunk.Err != nil && chunk.Err.Message != "" {
			return fmt.Errorf("%s API 错误: %s", Label(p), chunk.Err.Message)
		}
		if len(chunk.Choices) > 0 {
			if delta := chunk.Choices[0].Delta.Content; delta != "" {
				onDelta(delta)
			}
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err() // 客户端断开/整体超时：调用方按取消处理，不再包装
		}
		return fmt.Errorf("读取%s流式响应失败: %w", Label(p), err)
	}
	if !sawData && len(rawLines) > 0 {
		return decodeProviderError(p, []byte(strings.Join(rawLines, "\n")), http.StatusOK)
	}
	return nil
}

// decodeProviderError 三家错误体统一转译：{"error":{...}} 优先，兼容 DashScope 裸
// {"code","message"}；无错误详情时按 HTTP 状态 + 截断原文呈现。
func decodeProviderError(p Provider, body []byte, status int) error {
	var shape struct {
		Err     *apiErrorBody `json:"error"`
		Code    any           `json:"code"`
		Message string        `json:"message"`
	}
	_ = json.Unmarshal(body, &shape)
	msg := ""
	switch {
	case shape.Err != nil && shape.Err.Message != "":
		msg = shape.Err.Message
	case shape.Message != "":
		msg = shape.Message
	}
	if msg != "" {
		return fmt.Errorf("%s API 错误: %s", Label(p), msg)
	}
	if len(body) > 200 {
		body = body[:200]
	}
	if len(body) == 0 {
		return fmt.Errorf("%s API HTTP %d", Label(p), status)
	}
	return fmt.Errorf("%s API HTTP %d: %s", Label(p), status, body)
}

// buildChatRequest 组装请求：system 提示前置 + 客户端消息原样；千问附带关闭思考模式。
func buildChatRequest(p Provider, model string, messages []Message) chatRequest {
	msgs := make([]chatMessage, 0, len(messages)+1)
	msgs = append(msgs, chatMessage{Role: "system", Content: systemPrompt})
	for _, m := range messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Content})
	}
	req := chatRequest{Model: model, Messages: msgs, Stream: true}
	if p == ProviderQianwen {
		off := false
		req.EnableThinking = &off
	}
	return req
}
