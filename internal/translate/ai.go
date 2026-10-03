package translate

// AI 源:回显锚定协议(移植自 SmartSub design D4/D5)。
// 请求:逐条编号字幕行;要求严格 JSON {"<编号>":{"src":"原行回显","tr":"译文"}}。
// 校验:回显与原文相似度 <0.9 判错位 → 单轮定点补翻;译文≈原文(疑似复制)并入补翻;
// 补翻后仍复制的保留译文并计数(不静默丢弃)。
// 解析:宽松容错(剥围栏/剥 think/截取首尾大括号/去尾逗号),零第三方依赖。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/subtitle"
)

const echoThreshold = 0.9

// assistantMessage 内部消息形状(与 assistant.Message 解耦,便于缝注入)。
type assistantMessage struct{ Role, Content string }

// aiStreamCompose 流式底层缝:生产=assistant.StreamCompose 的薄适配。
var aiStreamCompose = func(ctx context.Context, cfg *config.Config, provider, model, system string,
	messages []assistantMessage, onDelta func(string)) error {
	msgs := make([]assistant.Message, len(messages))
	for i, m := range messages {
		msgs[i] = assistant.Message{Role: m.Role, Content: m.Content}
	}
	return assistant.StreamCompose(ctx, cfg, assistant.Provider(provider), model, system, msgs, onDelta)
}

// AIDiag AI 源诊断计数(引擎聚合进 Stats)。
type AIDiag struct {
	EchoChecked  int
	Misaligned   int
	Repaired     int
	Untranslated int
}

// buildAnchoredPrompt 组装系统/用户提示;目标为中文时明示简/繁(issue #332 教训:
// 清单名「中文（简体）/中文（繁体）」对模型不够醒目,系统提示中再消歧一次)。
func buildAnchoredPrompt(lines []string, srcLang, tgtLang string) (string, string) {
	tgtName := tgtLang
	for _, l := range volcengine.MTLanguages() {
		if l.Code == tgtLang {
			tgtName = l.Name
			break
		}
	}
	disambig := ""
	switch tgtLang {
	case "zh":
		disambig = "目标语言为简体中文,务必使用简体字形输出,严禁输出繁体。"
	case "zh-Hant":
		disambig = "目标语言为繁体中文(Traditional Chinese),务必使用繁体字形输出,严禁输出简体。"
	}
	system := fmt.Sprintf(`你是专业的字幕翻译引擎,把编号字幕行逐条翻译成%s。
规则:
1. 逐条独立翻译,严禁合并、拆分或增删条目。
2. 字幕文体:口语自然、简洁;单条译文长度尽量贴近原文,便于字幕显示。
3. 专有名词、数字、代码保持原样;纯语气词可省略。
4. 输出严格 JSON,不要任何解释、注释或代码围栏:
{"<编号>":{"src":"该编号原行逐字回显","tr":"译文"}}`, tgtName)
	if disambig != "" {
		system += "\n" + disambig
	}
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%d. %s\n", i+1, l)
	}
	return system, b.String()
}

// parseAIResponse 宽松解析 AI 响应:锚定协议(map[id]EchoEntry)优先,
// 旧协议 map[id]string 兜底(simple=true)。支持围栏/前置说明/think 块/尾逗号。
func parseAIResponse(raw string) (map[string]subtitle.EchoEntry, bool, error) {
	text := strings.TrimSpace(raw)
	// 剥 <think>…</think>(含未闭合:从 <think> 起整段视为思考内容丢弃)
	if i := strings.Index(text, "<think>"); i >= 0 {
		if j := strings.Index(text[i:], "</think>"); j >= 0 {
			text = text[:i] + text[i+j+len("</think>"):]
		} else {
			text = text[:i]
		}
	}
	lo := strings.Index(text, "{")
	hi := strings.LastIndex(text, "}")
	if lo < 0 || hi <= lo {
		return nil, false, fmt.Errorf("AI 响应中没有 JSON 对象")
	}
	text = text[lo : hi+1]
	decode := func(s string, v any) error {
		if err := json.Unmarshal([]byte(s), v); err != nil {
			// 尾逗号修复后重试
			fixed := strings.ReplaceAll(s, ",}", "}")
			fixed = strings.ReplaceAll(fixed, ",]", "]")
			return json.Unmarshal([]byte(fixed), v)
		}
		return nil
	}
	var anchored map[string]subtitle.EchoEntry
	if err := decode(text, &anchored); err == nil && len(anchored) > 0 {
		hasEcho := false
		for _, e := range anchored {
			if e.SrcEcho != "" || e.Tr != "" {
				hasEcho = true
				break
			}
		}
		if hasEcho {
			return anchored, false, nil
		}
	}
	var simple map[string]string
	if err := decode(text, &simple); err != nil {
		return nil, false, fmt.Errorf("AI 响应 JSON 解析失败: %w", err)
	}
	out := make(map[string]subtitle.EchoEntry, len(simple))
	for k, v := range simple {
		out[k] = subtitle.EchoEntry{Tr: v}
	}
	return out, true, nil
}

// validateAIResp 按协议形态校验:锚定协议走 ValidateEcho(阈值 0.9),
// 旧协议 {id:译文} 走 ValidateSimple。resp 键均为批内 1 起编号。
func validateAIResp(lines []string, resp map[string]subtitle.EchoEntry, simple bool) subtitle.EchoVerdict {
	srcs := make(map[int]string, len(lines))
	for i, l := range lines {
		srcs[i] = l
	}
	if simple {
		sm := make(map[string]string, len(resp))
		for k, e := range resp {
			sm[k] = e.Tr
		}
		return subtitle.ValidateSimple(srcs, sm)
	}
	return subtitle.ValidateEcho(srcs, resp, echoThreshold)
}

// aiCall 单次流式调用:增量正文累积为完整响应文本。
func aiCall(ctx context.Context, cfg *config.Config, provider, model, system, user string) (string, error) {
	var b strings.Builder
	msgs := []assistantMessage{{Role: "user", Content: user}}
	err := aiStreamCompose(ctx, cfg, provider, model, system, msgs, func(delta string) {
		b.WriteString(delta)
	})
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

// resolveAIModel 补齐 provider/model:任一为空时经默认大模型解析补齐;
// 两者全空且解析失败时透出含 ErrNoCred 包装的哨兵错误。仅给定其一且解析失败时
// 保留原值,由底层 StreamCompose 报出更具体的平台/模型错误。
func resolveAIModel(cfg *config.Config, provider, model string) (string, string, error) {
	if provider != "" && model != "" {
		return provider, model, nil
	}
	p, m, err := assistant.ResolveDefault(cfg)
	if err != nil {
		if provider == "" && model == "" {
			return "", "", err
		}
		return provider, model, nil
	}
	if provider == "" {
		provider = string(p)
	}
	if model == "" {
		model = m
	}
	return provider, model, nil
}

// aiTranslateLines AI 源翻译一批(引擎按 20 条/批喂):首轮锚定请求 → 回显校验 →
// 疑似复制判定 → 单轮定点补翻(仅问题条目,批内编号重排)→ 对齐输出。
func aiTranslateLines(ctx context.Context, cfg *config.Config, provider, model string,
	lines []string, srcLang, tgtLang string) ([]string, AIDiag, error) {
	provider, model, err := resolveAIModel(cfg, provider, model)
	if err != nil {
		return nil, AIDiag{}, err
	}
	var diag AIDiag

	// 首轮:锚定协议整批请求
	system, user := buildAnchoredPrompt(lines, srcLang, tgtLang)
	raw, err := aiCall(ctx, cfg, provider, model, system, user)
	if err != nil {
		return nil, diag, err
	}
	resp, simple, err := parseAIResponse(raw)
	if err != nil {
		return nil, diag, err
	}
	verdict := validateAIResp(lines, resp, simple)
	diag.EchoChecked += verdict.EchoChecked
	diag.Misaligned = len(verdict.Flagged)

	out := make([]string, len(lines))
	for i, tr := range verdict.Accepted {
		out[i] = tr
	}

	// 疑似复制判定(首轮已接受条目):并入待补翻集
	copySet := map[int]bool{}
	for i := range lines {
		if _, ok := verdict.Accepted[i]; ok && subtitle.IsSuspectedCopy(lines[i], out[i], tgtLang) {
			copySet[i] = true
		}
	}
	repairIdx := append([]int{}, verdict.Flagged...)
	for i := range copySet {
		repairIdx = append(repairIdx, i)
	}
	sort.Ints(repairIdx)

	if len(verdict.Flagged) == 0 {
		// 无错位条目:疑似复制不再单开一轮(同提示重问大概率复发),保留首轮译文并计数
		diag.Untranslated = len(copySet)
		return out, diag, nil
	}

	// 单轮定点补翻:只重问问题条目(错位+疑似复制),批内编号重排,协议同首轮
	sub := make([]string, len(repairIdx))
	for k, idx := range repairIdx {
		sub[k] = lines[idx]
	}
	system2, user2 := buildAnchoredPrompt(sub, srcLang, tgtLang)
	raw2, err := aiCall(ctx, cfg, provider, model, system2, user2)
	if err != nil {
		// 补翻失败不放大:保留首轮结果,问题条目无可信译文 → 计数 Untranslated
		diag.Untranslated += len(repairIdx)
		return out, diag, nil
	}
	resp2, simple2, err := parseAIResponse(raw2)
	if err != nil {
		diag.Untranslated += len(repairIdx)
		return out, diag, nil
	}
	verdict2 := validateAIResp(sub, resp2, simple2)
	diag.EchoChecked += verdict2.EchoChecked
	fixed := 0
	for k, g := range repairIdx {
		tr2, ok := verdict2.Accepted[k]
		if !ok || subtitle.IsSuspectedCopy(lines[g], tr2, tgtLang) {
			continue // 未修复/补翻后仍复制:保留首轮译文,计数 Untranslated
		}
		out[g] = tr2
		fixed++
	}
	diag.Repaired += fixed
	diag.Untranslated += len(repairIdx) - fixed
	return out, diag, nil
}
