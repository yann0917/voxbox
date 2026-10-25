// Package workflow 定义工作流编排：图模型、校验、输入展开与执行器。
// 图 = 工具节点 DAG；每个节点是一次普通任务提交（复用 task 引擎的校验、
// 落库、WS 进度与历史），节点间传递上游产物（引擎 artifact_input 语义的
// 服务端直通版）。执行器为入度驱动的通用 DAG 调度：条件分支（多出边带条件）
// 只需新增节点类型，图模型与其余执行器不变。
package workflow

import (
	"fmt"
	"strings"
)

// GraphVersion 当前图 schema 版本；Graph.Version 高于它拒绝（低版本向前兼容解释）。
const GraphVersion = 1

// Graph 工作流编排图。Version 是 schema 迁移钩子：将来分支/循环节点演进时
// 借此识别旧图并迁移，历史图不因字段扩展失效。
type Graph struct {
	Version int         `json:"version,omitempty"`
	Inputs  []InputSlot `json:"inputs,omitempty"` // 运行时输入槽声明（模板要填的空）
	Nodes   []Node      `json:"nodes"`
}

// InputSlot 运行时输入槽：string/text 进节点参数（{{inputs.key}} 引用），
// file 进引擎文件通道（前端先传 /api/uploads，值=file_id）。
type InputSlot struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // string|text|file
	Required    bool   `json:"required,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

// Node 一个工具节点。InputsFrom 即边集合（引擎 Files 键→上游节点引用），
// 天然支持扇出（同一上游喂多个下游）与多输入（mix 的 audio+audio2）。
type Node struct {
	Key        string            `json:"key"`
	Provider   string            `json:"provider"`
	Tool       string            `json:"tool"`
	Params     map[string]any    `json:"params,omitempty"`
	InputsFrom map[string]string `json:"inputs_from,omitempty"` // "audio": "dl" / "sep:track=voice" / "sep:2"
	X          float64           `json:"x,omitempty"`
	Y          float64           `json:"y,omitempty"`
}

// NodeRun 运行记录里一个节点的编排状态（store.WorkflowRun.Nodes 的元素）。
// 节点任务本体在 tasks 表，这里只挂 task id 与产物归属。
type NodeRun struct {
	Key         string   `json:"key"`
	TaskID      string   `json:"task_id,omitempty"`
	Status      string   `json:"status"` // pending|running|done|failed|skipped|canceled
	ArtifactIDs []string `json:"artifact_ids,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// 节点/运行状态常量（节点状态与任务状态同名对齐，skipped 为编排层独有）。
const (
	NodePending  = "pending"
	NodeRunning  = "running"
	NodeDone     = "done"
	NodeFailed   = "failed"
	NodeSkipped  = "skipped"
	NodeCanceled = "canceled"
)

// RunStatus 运行级状态。
const (
	RunPending  = "pending"
	RunRunning  = "running"
	RunDone     = "done"
	RunFailed   = "failed"
	RunCanceled = "canceled"
)

// 上游引用语法 "node[:select]"，select 三形态：空=按端口 kind 挑第一个产物；
// "2"=第 N 个产物（1 起）；"voice"=按产物 meta.track 匹配（分离链的人声/伴奏）。
type UpstreamRef struct {
	Node   string
	Select string
}

// ParseUpstreamRef 解析 InputsFrom 值。
func ParseUpstreamRef(v string) (UpstreamRef, error) {
	if v == "" {
		return UpstreamRef{}, fmt.Errorf("上游引用为空")
	}
	node, sel, _ := strings.Cut(v, ":")
	node = strings.TrimSpace(node)
	if node == "" {
		return UpstreamRef{}, fmt.Errorf("上游引用非法: %q", v)
	}
	return UpstreamRef{Node: node, Select: strings.TrimSpace(sel)}, nil
}

// Expand 把 inputs 注入图：校验必填槽、深拷贝各节点 params 并展开
// {{inputs.key}} 引用。返回展开后的节点参数副本与 file 型槽取值
// （slotKey→file_id，供执行器进文件通道），不改原图。
func (g *Graph) Expand(inputs map[string]any) (map[string]map[string]any, map[string]string, error) {
	vals := map[string]string{}
	fileVals := map[string]string{}
	for _, slot := range g.Inputs {
		v, ok := inputs[slot.Key]
		if !ok || strings.TrimSpace(toExpandString(v)) == "" {
			if slot.Required {
				return nil, nil, fmt.Errorf("缺少必填输入: %s", slot.Label)
			}
			continue
		}
		if slot.Type == "file" {
			fileVals[slot.Key] = toExpandString(v) // file_id，执行器解析进文件通道
			continue
		}
		vals[slot.Key] = toExpandString(v)
	}
	out := make(map[string]map[string]any, len(g.Nodes))
	for _, n := range g.Nodes {
		p, err := expandParams(n.Params, vals)
		if err != nil {
			return nil, nil, fmt.Errorf("节点 %s: %w", n.Key, err)
		}
		out[n.Key] = p
	}
	return out, fileVals, nil
}

// toExpandString 输入值统一转字符串（file 槽是 file_id，文本槽是原文）。
func toExpandString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// expandParams 深拷贝并展开：字符串值内的 {{inputs.key}} 逐个替换，
// 未声明的引用报错（写错槽名在提交前暴露，而不是运行中参数为空难排查）。
func expandParams(params map[string]any, vals map[string]string) (map[string]any, error) {
	if params == nil {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		ev, err := expandValue(v, vals)
		if err != nil {
			return nil, fmt.Errorf("参数 %s: %w", k, err)
		}
		out[k] = ev
	}
	return out, nil
}

func expandValue(v any, vals map[string]string) (any, error) {
	switch t := v.(type) {
	case string:
		return expandString(t, vals)
	case map[string]any:
		return expandParams(t, vals)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			ev, err := expandValue(item, vals)
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	default:
		return v, nil
	}
}

func expandString(s string, vals map[string]string) (any, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(s)
			break
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("引用未闭合: %q", s)
		}
		end += start
		b.WriteString(s[:start])
		ref := strings.TrimSpace(s[start+2 : end])
		key, ok := strings.CutPrefix(ref, "inputs.")
		if !ok || key == "" {
			return nil, fmt.Errorf("未知引用: {{%s}}", ref)
		}
		v, ok := vals[key]
		if !ok {
			return nil, fmt.Errorf("输入槽 %s 未提供", key)
		}
		b.WriteString(v)
		s = s[end+2:]
	}
	return b.String(), nil
}

// Validate 结构校验：key 唯一、上游引用存在且无环。kind 匹配与端口合法性
// 需要 ports 表与注册表，由 ValidateWithTools 做（graph 保持无依赖可测）。
func (g *Graph) Validate() error {
	if g.Version > GraphVersion {
		return fmt.Errorf("工作流图版本过新: %d（当前支持 ≤%d）", g.Version, GraphVersion)
	}
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Key == "" {
			return fmt.Errorf("存在未命名节点")
		}
		if seen[n.Key] {
			return fmt.Errorf("节点 key 重复: %s", n.Key)
		}
		seen[n.Key] = true
	}
	slotKeys := map[string]bool{}
	for _, s := range g.Inputs {
		if s.Key == "" || slotKeys[s.Key] {
			return fmt.Errorf("输入槽 key 为空或重复: %q", s.Key)
		}
		slotKeys[s.Key] = true
		switch s.Type {
		case "string", "text", "file":
		default:
			return fmt.Errorf("输入槽 %s 类型非法: %s", s.Key, s.Type)
		}
	}
	for _, n := range g.Nodes {
		for port, ref := range n.InputsFrom {
			// file 输入槽引用（inputs.<key>）：校验槽存在且为 file 型，不是上游节点
			if slotKey, isSlot := strings.CutPrefix(ref, "inputs."); isSlot {
				found := false
				for _, s := range g.Inputs {
					if s.Key == slotKey && s.Type == "file" {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("节点 %s 端口 %s 引用了未声明的文件输入槽: %s", n.Key, port, slotKey)
				}
				continue
			}
			up, err := ParseUpstreamRef(ref)
			if err != nil {
				return fmt.Errorf("节点 %s 端口 %s: %w", n.Key, port, err)
			}
			if !seen[up.Node] {
				return fmt.Errorf("节点 %s 引用了不存在的上游: %s", n.Key, up.Node)
			}
		}
	}
	if cycle := findCycle(g.Nodes); cycle != "" {
		return fmt.Errorf("流程存在循环: %s", cycle)
	}
	return nil
}

// findCycle DFS 找环，返回环上任意一个节点 key（提示用途）。
// file 输入槽引用（inputs.*）不是节点边，不参与环。
func findCycle(nodes []Node) string {
	deps := map[string][]string{}
	for _, n := range nodes {
		for _, ref := range n.InputsFrom {
			if strings.HasPrefix(ref, "inputs.") {
				continue
			}
			up, err := ParseUpstreamRef(ref)
			if err != nil {
				continue
			}
			deps[n.Key] = append(deps[n.Key], up.Node)
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var cycle string
	var visit func(k string) bool
	visit = func(k string) bool {
		color[k] = gray
		for _, d := range deps[k] {
			switch color[d] {
			case gray:
				cycle = d
				return true
			case white:
				if visit(d) {
					return true
				}
			}
		}
		color[k] = black
		return false
	}
	for _, n := range nodes {
		if color[n.Key] == white && visit(n.Key) {
			return cycle
		}
	}
	return ""
}
