package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/xiaomi"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
	"github.com/yann0917/voxbox/internal/service"
)

type artifactOut struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Format     string `json:"format"`
	Size       int64  `json:"size"`
	DurationMS int64  `json:"duration_ms"`
	URL        string `json:"url,omitempty"` // 上游产物地址（火山分离轨的 TOS 签名 URL，24h 有效）
}

// artifactURLFromMeta 取产物 Meta 里的上游 url（转存型产物才会写，如火山分离各轨）。
func artifactURLFromMeta(meta string) string {
	if meta == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return ""
	}
	u, _ := m["url"].(string)
	return u
}

type jsonResult struct {
	TaskID    string         `json:"task_id"`
	Provider  string         `json:"provider"`
	Tool      string         `json:"tool"`
	Status    string         `json:"status"`
	CostMS    int64          `json:"cost_ms"`
	Artifacts []artifactOut  `json:"artifacts"`
	Summary   map[string]any `json:"summary,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// runToolCore 同步执行工具并组装 --json 契约结果（CLI 与 MCP server 共享的执行核心）。
// 契约见 docs/json-contract.md：artifacts[].path 恒为绝对路径；summary 为任务摘要 JSON。
// 调用方须已 StartEngine；_out 重定位由调用方在 params 中自行设置。
func runToolCore(ctx context.Context, svc *service.Service, providerName, toolName string, params map[string]any, files map[string]string) (jsonResult, error) {
	task, arts, err := svc.Engine().SubmitSync(ctx, providerName, toolName, params, files)
	if err != nil {
		return jsonResult{}, err
	}
	result := jsonResult{
		TaskID: task.ID, Provider: task.Provider, Tool: task.Tool,
		Status: string(task.Status), CostMS: task.CostMS,
		Artifacts: []artifactOut{},
	}
	for _, a := range arts {
		result.Artifacts = append(result.Artifacts, artifactOut{
			Kind: a.Kind, Path: absArtifactPath(svc.Config().DataDir, a.Path),
			Format: a.Format, Size: a.Size, DurationMS: a.DurationMS,
			URL: artifactURLFromMeta(a.Meta),
		})
	}
	// summary 从任务落库的 JSON 恢复（引擎在任务成功时序列化 TaskOutput.Summary）
	if task.Summary != "" {
		_ = json.Unmarshal([]byte(task.Summary), &result.Summary)
	}
	return result, nil
}

func eprintf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

// absArtifactPath 将产物相对路径解析为基于数据目录的绝对路径（Join 后 Clean）；
// 已是绝对路径则原样返回，保证 JSON 的 artifacts[].path 始终为绝对路径。
func absArtifactPath(dataDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dataDir, p)
}

// exitCodeFor 将执行错误映射为退出码：2 参数、4 凭证/未开通、3 任务失败。
func exitCodeFor(err error) int {
	switch {
	case err == nil:
		return 0
	case strings.Contains(err.Error(), "缺少必填参数"), strings.Contains(err.Error(), "缺少输入"),
		strings.Contains(err.Error(), "仅支持"),
		strings.Contains(err.Error(), "暂不支持"),     // ASR 音频格式错误（audioFormatOf）/ 播客 format 枚举
		strings.Contains(err.Error(), "speakers"), // 播客音色数量校验（「speakers 需要恰好 2 个」）
		strings.Contains(err.Error(), "对话稿"),      // 播客对话稿解析错误（「对话稿格式错误」）/ 输入互斥文案
		strings.Contains(err.Error(), "术语格式错误"),   // 翻译术语解析错误（mtParseTerms）
		// 长度/载荷超限属参数类（契约：长度超限 → 2，请求未被推理、不消耗配额）：
		// 机器翻译 45000130、长文本合成 10 万字符预检。
		strings.Contains(err.Error(), "载荷超限"), strings.Contains(err.Error(), "超出长度限制"):
		return 2
	case errors.Is(err, volcengine.ErrNoCred), errors.Is(err, volcengine.ErrAuth),
		errors.Is(err, volcengine.ErrNotGranted), // 资源未开通（如机器翻译缺 volc.speech.mt）→ 配置类问题，重试无意义
		errors.Is(err, qianwen.ErrNoCred),        // 千问凭证缺失 → 同为配置类问题
		errors.Is(err, xiaomi.ErrNoCred),         // 小米凭证缺失 → 同为配置类问题
		errors.Is(err, zhipu.ErrNoCred):          // 智谱凭证缺失 → 同为配置类问题
		return 4
	default:
		return 3
	}
}
