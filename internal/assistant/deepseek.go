// DeepSeek 专用链路：OpenAI Responses 风格 /responses 流式协议，以及基于函数工具的
// 联网回路——服务端声明 web_search/web_fetch 两个 function 工具，模型发起调用后由
// 本地执行（转调 DeepSeek 网页搜索/抓取 API），结果作为 function_call_output 回传续流。
// API 无状态：多轮上下文（含工具调用项）由客户端在 input 里全量回传。
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
	"github.com/yann0917/voxbox/internal/provider/deepseek"
)

// deepseekBaseURL 生产指向官方入口；测试替换为 httptest 地址后整条链路（流式对话 +
// 工具执行）同源重定向。
var deepseekBaseURL = deepseek.BaseURL

// maxWebRounds 联网工具轮数上限：三轮检索对问答足够，防模型循环刷搜索；轮数用尽后
// 请求不再携带工具（tool_choice=none）强制收尾。
const maxWebRounds = 3

// webFetchMaxRunes 网页正文注入上下文的 rune 上限（与 chat 上下文封顶同数量级）。
const webFetchMaxRunes = 20000

// webSystemNote 挂载联网工具时追加在系统提示之后的工具使用指引。
const webSystemNote = "\n\n你可以使用 web_search（联网搜索）与 web_fetch（抓取网页正文）两个工具获取实时信息：" +
	"涉及时效性内容（新闻、价格、版本、赛事等）或自己不确定的事实时主动检索；" +
	"web_search 的 query 支持 site:、-site:、引号、lang:、after:/before: 等搜索语法；" +
	"回答中引用网络信息时注明来源链接。"

// webTools 服务端声明的联网函数工具（OpenAI Responses 风格：name/description/parameters 平铺）。
var webTools = []responsesTool{
	{
		Type: "function", Name: "web_search",
		Description: "联网搜索，返回网页结果的标题、链接、摘要与发布时间。需要时效性信息或不确定的事实时调用。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "搜索词，1~128 字符"},
				"count": map[string]any{"type": "integer", "description": "返回条数（1~10，默认 8）"},
			},
			"required": []string{"query"},
		},
	},
	{
		Type: "function", Name: "web_fetch",
		Description: "抓取指定网页的正文（Markdown）。已知具体网址需要阅读全文时调用。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "要抓取的 http/https 网页地址"},
			},
			"required": []string{"url"},
		},
	},
}

// streamResponsesWeb 联网增强入口：凭证/模型校验后进入带工具回路的 /responses 流。
func streamResponsesWeb(ctx context.Context, cfg *config.Config, p Provider, model, system string, messages []Message, onDelta func(string), onStatus func(string)) error {
	if err := CheckCredential(cfg, p); err != nil {
		return err
	}
	if !ModelAllowed(p, model) {
		return fmt.Errorf("平台 %s 不支持模型 %s", Label(p), model)
	}
	return streamResponses(ctx, cfg, p, model, system+webSystemNote, messages, onDelta, true, onStatus)
}

// streamResponses /responses 流式实现：循环执行「请求 → 收集 function_call → 执行工具 →
// 回传续流」，无工具调用即结束。整体硬上限 3 分钟（多轮共享），上游卡死时兜底。
func streamResponses(ctx context.Context, cfg *config.Config, p Provider, model, system string, messages []Message, onDelta func(string), withTools bool, onStatus func(string)) error {
	input := make([]responsesItem, 0, len(messages)+2*maxWebRounds)
	for _, m := range messages {
		input = append(input, responsesItem{Role: m.Role, Content: m.Content})
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for round := 0; ; round++ {
		// 前 maxWebRounds 轮携带工具；轮数用尽后不再携带（tool_choice=none）强制文本收尾。
		var tools []responsesTool
		var toolChoice *string
		if withTools {
			if round < maxWebRounds {
				tools = webTools
			} else {
				none := "none"
				toolChoice = &none
			}
		}
		calls, err := streamResponsesRound(ctx, cfg, p, model, system, input, tools, toolChoice, onDelta)
		if err != nil {
			return err
		}
		if len(calls) == 0 {
			return nil
		}
		ori := lastUserQuestion(messages)
		for _, c := range calls {
			out := executeWebTool(ctx, cfg, c.Name, c.Arguments, ori, onStatus)
			input = append(input,
				// 回传调用项供 API 配对（官方多轮组装口径），再紧跟工具结果
				responsesItem{Type: "function_call", CallID: c.CallID, Name: c.Name, Arguments: c.Arguments},
				responsesItem{Type: "function_call_output", CallID: c.CallID, Output: out},
			)
		}
	}
}

// streamResponsesRound 单轮流式：返回本轮模型发起的函数调用（response.output_item.done
// 事件携带完整参数）。正文增量实时回调，思考通道（reasoning）与 web_search_call 等其余
// 事件忽略——工具执行期由 onStatus 通报进度。tools/toolChoice 均可空（纯净请求）。
func streamResponsesRound(ctx context.Context, cfg *config.Config, p Provider, model, system string, input []responsesItem, tools []responsesTool, toolChoice *string, onDelta func(string)) ([]responsesOutputItem, error) {
	req := responsesRequest{
		Model:        model,
		Input:        input,
		Instructions: system,
		Stream:       true,
		Tools:        tools,
		ToolChoice:   toolChoice,
	}
	// 快档压低思考强度快答（官方 effort 档位）；v4-pro 旗舰默认思考不干预。
	if model == "deepseek-flash" {
		req.Reasoning = &responsesReasoning{Effort: "low"}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, deepseekBaseURL+"/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKeyOf(cfg, p))
	resp, err := apiClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求%s平台失败: %w", Label(p), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, decodeProviderError(p, body, resp.StatusCode)
	}
	var calls []responsesOutputItem
	err = scanResponsesStream(ctx, resp.Body, onDelta, func(item responsesOutputItem) {
		if item.CallID != "" {
			calls = append(calls, item)
		}
	})
	return calls, err
}

// responsesRequest /responses 请求体（OpenAI Responses 风格）。
type responsesRequest struct {
	Model        string              `json:"model"`
	Input        []responsesItem     `json:"input"`
	Instructions string              `json:"instructions,omitempty"`
	Stream       bool                `json:"stream"`
	Tools        []responsesTool     `json:"tools,omitempty"`
	ToolChoice   *string             `json:"tool_choice,omitempty"`
	Reasoning    *responsesReasoning `json:"reasoning,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort"`
}

// responsesItem input 项：message（Type 空，content 用官方支持的纯字符串形态）/
// function_call（回传模型发起的调用）/ function_call_output（工具结果），字段互斥。
type responsesItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// responsesEvent SSE data 载荷：事件类型在 data 内（event: 行与之一致，以 data 为准）。
// 关注四类：正文增量、函数调用项完成、终止（completed/incomplete/failed）。
type responsesEvent struct {
	Type     string               `json:"type"`
	Delta    string               `json:"delta"`
	Item     *responsesOutputItem `json:"item"`
	Response *responsesResponse   `json:"response"`
	Error    *apiErrorBody        `json:"error"`
}

type responsesOutputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Status    string `json:"status"`
}

type responsesResponse struct {
	Status            string        `json:"status"`
	Error             *apiErrorBody `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

// scanResponsesStream 逐行解析 Responses SSE（event: + data: 行，无 [DONE]，终止于
// completed/incomplete/failed 事件）。非 data 行累积兜底：上游直接回 JSON 错误体时
// 流结束后按错误体解析（scanStream 同款防御）。
func scanResponsesStream(ctx context.Context, r io.Reader, onDelta func(string), onCall func(responsesOutputItem)) error {
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
		var ev responsesEvent
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue // 心跳/注释等无法解析的行直接忽略
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				onDelta(ev.Delta)
			}
		case "response.output_item.done":
			// function_call 项在此携带完整 call_id/name/arguments（增量事件不积累）
			if ev.Item != nil && ev.Item.Type == "function_call" {
				onCall(*ev.Item)
			}
		case "response.completed":
			return nil
		case "response.incomplete":
			// 截断（max_output_tokens/内容审查）也按正常收尾：已产出的正文保留
			return nil
		case "response.failed":
			if ev.Error != nil && ev.Error.Message != "" {
				return fmt.Errorf("DeepSeek API 错误: %s", ev.Error.Message)
			}
			if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
				return fmt.Errorf("DeepSeek API 错误: %s", ev.Response.Error.Message)
			}
			return fmt.Errorf("DeepSeek API 错误: 响应生成失败")
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err() // 客户端断开/整体超时：调用方按取消处理，不再包装
		}
		return fmt.Errorf("读取%s流式响应失败: %w", Label(ProviderDeepseek), err)
	}
	if !sawData && len(rawLines) > 0 {
		return decodeProviderError(ProviderDeepseek, []byte(strings.Join(rawLines, "\n")), http.StatusOK)
	}
	return nil // 上游未发终止事件直接断流：正常收尾不算错误
}

// executeWebTool 执行一次联网工具调用，返回注入回模型的结果文本。失败以文本说明回传
// （模型可自行调整后重试或作答），不中断整个流。
func executeWebTool(ctx context.Context, cfg *config.Config, name, arguments, oriQuestion string, onStatus func(string)) string {
	status := func(s string) {
		if onStatus != nil {
			onStatus(s)
		}
	}
	client := deepseek.NewClient(apiKeyOf(cfg, ProviderDeepseek), deepseekBaseURL)
	switch name {
	case "web_search":
		var args struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil || strings.TrimSpace(args.Query) == "" {
			return "工具调用失败：web_search 参数需为 {\"query\": \"搜索词\"} 形式的 JSON"
		}
		status("正在联网搜索：" + webTruncate(strings.TrimSpace(args.Query), 48))
		results, err := client.WebSearch(ctx, args.Query, args.Count, oriQuestion)
		if err != nil {
			return "搜索失败：" + err.Error()
		}
		if len(results) == 0 {
			return "未搜索到相关结果，请调整搜索词。"
		}
		var b strings.Builder
		for i, r := range results {
			fmt.Fprintf(&b, "%d. %s\n%s\n", i+1, r.Title, r.URL)
			if r.SiteName != nil && *r.SiteName != "" {
				b.WriteString("来源：" + *r.SiteName)
				if r.Date != nil && *r.Date != "" {
					b.WriteString(" · " + *r.Date)
				}
				b.WriteString("\n")
			}
			summary := r.Summary
			if summary == "" {
				summary = r.Snippet
			}
			if summary != "" {
				b.WriteString(summary + "\n")
			}
			b.WriteString("\n")
		}
		return b.String()
	case "web_fetch":
		var args struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "工具调用失败：web_fetch 参数需为 {\"url\": \"网页地址\"} 形式的 JSON"
		}
		status("正在读取网页：" + webTruncate(strings.TrimSpace(args.URL), 48))
		title, content, err := client.WebFetch(ctx, args.URL)
		if err != nil {
			return "抓取失败：" + err.Error()
		}
		if title != "" {
			title = "# " + title + "\n\n"
		}
		return title + webTruncate(content, webFetchMaxRunes)
	default:
		return "未知工具：" + name + "，可用工具仅 web_search 与 web_fetch。"
	}
}

// lastUserQuestion 最近一条用户消息（截到搜索 API 的 ori_question 上限），帮助搜索
// 接口理解触发意图；无用户消息时返回空。
func lastUserQuestion(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// webTruncate 按 rune 截断（状态文案/网页正文封顶用）。
func webTruncate(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max])
}
