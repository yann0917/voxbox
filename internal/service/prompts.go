package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/prompts"
	"github.com/yann0917/voxbox/internal/store"
)

// 提示词库：内置条目（internal/prompts）与用户自定义条目（SQLite）合并出列表；
// AI 写作（生成/润色）按条目组装系统提示后复用悬浮助手的流式管道，默认大模型
// 与助手共用 assistant.ResolveDefault 一个口径。

// Item 列表条目：内置与自定义的合并形态。Key 与 ID 二选一。
type Item struct {
	Source      string `json:"source"` // builtin|user
	Key         string `json:"key,omitempty"`
	ID          uint   `json:"id,omitempty"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Content     string `json:"content"`
}

// ListPrompts 内置在前（保持清单顺序），自定义按更新时间倒序在后。
func (s *Service) ListPrompts(userID string) ([]Item, error) {
	out := make([]Item, 0)
	for _, e := range prompts.Builtin() {
		out = append(out, Item{
			Source: "builtin", Key: e.Key, Name: e.Name, Category: e.Category,
			Description: e.Description, Kind: string(e.Kind), Content: e.Content,
		})
	}
	rows, err := s.db.ListPrompts(userID)
	if err != nil {
		return nil, err
	}
	for _, p := range rows {
		out = append(out, Item{
			Source: "user", ID: p.ID, Name: p.Name, Category: p.Category,
			Description: p.Description, Kind: p.Kind, Content: p.Content,
		})
	}
	return out, nil
}

const (
	promptMaxName = 20   // 名称 rune 上限
	promptMaxCat  = 8    // 分组 rune 上限
	promptMaxDesc = 60   // 说明 rune 上限
	promptMaxBody = 2000 // 提示词正文 rune 上限
)

// PromptInput 创建/更新条目的入参（更新时全字段覆盖，简单直接）。
type PromptInput struct {
	Name        string
	Category    string
	Description string
	Content     string
	Kind        string
}

func (in PromptInput) normalize() (PromptInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(in.Category)
	in.Description = strings.TrimSpace(in.Description)
	in.Content = strings.TrimSpace(in.Content)
	if in.Name == "" {
		return in, errors.New("名称不能为空")
	}
	if len([]rune(in.Name)) > promptMaxName {
		return in, fmt.Errorf("名称不能超过 %d 字", promptMaxName)
	}
	if in.Category == "" {
		return in, errors.New("分组不能为空")
	}
	if len([]rune(in.Category)) > promptMaxCat {
		return in, fmt.Errorf("分组不能超过 %d 字", promptMaxCat)
	}
	if len([]rune(in.Description)) > promptMaxDesc {
		return in, fmt.Errorf("说明不能超过 %d 字", promptMaxDesc)
	}
	if in.Content == "" {
		return in, errors.New("提示词内容不能为空")
	}
	if len([]rune(in.Content)) > promptMaxBody {
		return in, fmt.Errorf("提示词内容不能超过 %d 字", promptMaxBody)
	}
	if in.Kind == "" {
		in.Kind = string(prompts.KindGenerate)
	}
	if !prompts.Kind(in.Kind).Valid() {
		return in, fmt.Errorf("未知的提示词用途: %s", in.Kind)
	}
	return in, nil
}

func (s *Service) CreatePrompt(userID string, in PromptInput) (store.Prompt, error) {
	in, err := in.normalize()
	if err != nil {
		return store.Prompt{}, err
	}
	p := store.Prompt{
		UserID: userID, Name: in.Name, Category: in.Category,
		Description: in.Description, Content: in.Content, Kind: in.Kind,
	}
	if err := s.db.CreatePrompt(&p); err != nil {
		return store.Prompt{}, err
	}
	return p, nil
}

func (s *Service) UpdatePrompt(userID string, id uint, in PromptInput) (store.Prompt, error) {
	in, err := in.normalize()
	if err != nil {
		return store.Prompt{}, err
	}
	p, err := s.db.GetPrompt(id, userID)
	if err != nil {
		return store.Prompt{}, err
	}
	p.Name, p.Category, p.Description, p.Content, p.Kind = in.Name, in.Category, in.Description, in.Content, in.Kind
	if err := s.db.UpdatePrompt(p); err != nil {
		return store.Prompt{}, err
	}
	return *p, nil
}

func (s *Service) DeletePrompt(userID string, id uint) error {
	return s.db.DeletePrompt(id, userID)
}

// ApplyReq AI 写作请求：builtin key 与自定义 id 二选一；input 对生成类是主题/要点
// （可空=自拟），对润色类是要改写的原文。
type ApplyReq struct {
	Builtin string         `json:"builtin"`
	ID      uint           `json:"id"`
	Input   string         `json:"input"`
	Length  prompts.Length `json:"length"`
}

const (
	applyMaxInput  = 1000 // 生成主题/要点 rune 上限
	applyMaxSource = 8000 // 润色原文 rune 上限
)

// ResolvedApply 校验就绪的写作请求：系统提示与用户消息均已定形，只差发起流式调用。
type ResolvedApply struct {
	System string
	Input  string
}

// ResolveApply 解析条目（内置 key 或本人自定义 id）、拼装系统提示（含朗读约束与篇幅指令）
// 并校验输入。错误都是参数级的，HTTP 层按 JSON 包络返回；模型解析在流式阶段做。
func (s *Service) ResolveApply(userID string, req ApplyReq) (ResolvedApply, error) {
	req.Input = strings.TrimSpace(req.Input)
	var content string
	var kind prompts.Kind
	switch {
	case req.Builtin != "":
		e, ok := prompts.Get(req.Builtin)
		if !ok {
			return ResolvedApply{}, fmt.Errorf("未知的内置提示词: %s", req.Builtin)
		}
		content, kind = e.Content, e.Kind
	case req.ID > 0:
		p, err := s.db.GetPrompt(req.ID, userID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return ResolvedApply{}, errors.New("提示词不存在")
			}
			return ResolvedApply{}, err
		}
		if !prompts.Kind(p.Kind).Valid() {
			return ResolvedApply{}, fmt.Errorf("未知的提示词用途: %s", p.Kind)
		}
		content, kind = p.Content, prompts.Kind(p.Kind)
	default:
		return ResolvedApply{}, errors.New("参数错误：未指定提示词")
	}

	system, err := prompts.SystemPrompt(content, kind, req.Length)
	if err != nil {
		return ResolvedApply{}, err
	}
	input := req.Input
	switch kind {
	case prompts.KindPolish:
		if input == "" {
			return ResolvedApply{}, errors.New("参数错误：请提供要润色的文本")
		}
		if len([]rune(input)) > applyMaxSource {
			return ResolvedApply{}, fmt.Errorf("润色文本不能超过 %d 字", applyMaxSource)
		}
	case prompts.KindDialect:
		if input == "" {
			return ResolvedApply{}, errors.New("参数错误：请提供要改写成方言的文本")
		}
		if len([]rune(input)) > applyMaxSource {
			return ResolvedApply{}, fmt.Errorf("方言改写文本不能超过 %d 字", applyMaxSource)
		}
	default:
		if len([]rune(input)) > applyMaxInput {
			return ResolvedApply{}, fmt.Errorf("主题要点不能超过 %d 字", applyMaxInput)
		}
		if input == "" {
			input = "请自拟一个合适的主题完成创作。"
		}
	}
	return ResolvedApply{System: system, Input: input}, nil
}

// StreamApply 按「AI 默认大模型」发起流式生成/润色，逐段回调增量正文。无可用模型时
// 返回可映射业务码 4 的哨兵错误。
func (s *Service) StreamApply(ctx context.Context, r ResolvedApply, onDelta func(string)) error {
	cfg := s.cfg.Load()
	providerName, model, err := assistant.ResolveDefault(cfg)
	if err != nil {
		return err
	}
	return assistant.StreamCompose(ctx, cfg, providerName, model, r.System,
		[]assistant.Message{{Role: "user", Content: r.Input}}, onDelta)
}

// AssistantDefault 当前「AI 默认大模型」原始配置值（"provider:model"，空=自动）。
func (s *Service) AssistantDefault() string { return s.cfg.Load().Assistant.DefaultModel }

// SaveAssistantDefault 保存默认大模型：空串=恢复自动；非空必须是目录内
// "provider:model"。凭证不校验（可先选模型后配 key，用时时再回落）。
func (s *Service) SaveAssistantDefault(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		p, model, ok := func() (assistant.Provider, string, bool) {
			name, model, found := strings.Cut(ref, ":")
			return assistant.Provider(strings.TrimSpace(name)), strings.TrimSpace(model), found
		}()
		if !ok || !assistant.KnownProvider(p) || !assistant.ModelAllowed(p, model) {
			return fmt.Errorf("未知的模型: %s", ref)
		}
		ref = string(p) + ":" + model
	}
	if err := config.Set("assistant.default_model", ref); err != nil {
		return err
	}
	nc := *s.cfg.Load()
	nc.Assistant.DefaultModel = ref
	s.cfg.Store(&nc)
	return nil
}
