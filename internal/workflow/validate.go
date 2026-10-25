package workflow

import (
	"fmt"
	"strings"

	"github.com/yann0917/voxbox/internal/provider"
)

// ValidateWithTools 结构+工具面校验：在 Validate 之上核对工具存在性、
// 可编排性、端口与产物 kind 匹配、必填参数有值或是输入槽引用。
// reg 为 nil 时跳过工具面（仅结构校验，模板常量的离线测试用）。
func (g *Graph) ValidateWithTools(reg *provider.Registry) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if reg == nil {
		return nil
	}
	for _, n := range g.Nodes {
		tool, ok := reg.Get(n.Provider, n.Tool)
		if !ok {
			return fmt.Errorf("节点 %s: 工具 %s.%s 不存在", n.Key, n.Provider, n.Tool)
		}
		spec := SpecOf(n.Provider, n.Tool)
		if spec == nil {
			return fmt.Errorf("节点 %s: 工具 %s.%s 暂不支持编排", n.Key, n.Provider, n.Tool)
		}
		if !spec.Chainable {
			return fmt.Errorf("节点 %s: 工具 %s.%s 不支持编排", n.Key, n.Provider, n.Tool)
		}
		for port, ref := range n.InputsFrom {
			inPort, ok := spec.PortOf(port)
			if !ok {
				return fmt.Errorf("节点 %s: 端口 %s 不存在（该工具输入端口: %s）",
					n.Key, port, joinPortKeys(spec.In))
			}
			up := findNode(g, ParseUpstreamRefMust(ref))
			if up == nil {
				continue // Validate 已查过存在性
			}
			upSpec := SpecOf(up.Provider, up.Tool)
			if upSpec == nil || !inPort.Compatible(upSpec.OutKinds) {
				return fmt.Errorf("节点 %s: 端口 %s（%s）与上游 %s 的产物类型不匹配",
					n.Key, port, inPort.Label, up.Key)
			}
		}
		// 必填参数：缺值即配置不全（值是输入槽引用也算已满足，run 时展开，
		// 展开后引擎 validate 兜底）
		for _, ps := range tool.ParamSpecs() {
			if !ps.Required {
				continue
			}
			v, ok := n.Params[ps.Key]
			if !ok || v == nil || fmt.Sprint(v) == "" {
				return fmt.Errorf("节点 %s: 缺少必填参数 %s（可引用输入槽 {{inputs.xxx}}）", n.Key, ps.Label)
			}
		}
	}
	return nil
}

func joinPortKeys(ports []InPort) string {
	keys := make([]string, len(ports))
	for i, p := range ports {
		keys[i] = p.Key
	}
	return strings.Join(keys, "/")
}

func findNode(g *Graph, key string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].Key == key {
			return &g.Nodes[i]
		}
	}
	return nil
}

// ParseUpstreamRefMust 供校验链内部复用（合法性已由 Validate 保证）。
func ParseUpstreamRefMust(v string) string {
	up, _ := ParseUpstreamRef(v)
	return up.Node
}
