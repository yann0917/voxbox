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
// mem 非空时在用户提示注入跨批记忆(场景摘要+术语表);askMemory 控制响应协议:
// 首轮要求 wrapper(translations+summary+glossary),补翻轮保持裸锚定。
func buildAnchoredPrompt(lines []string, srcLang, tgtLang string, mem *TranslationMemory, askMemory bool) (string, string) {
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
	spec := `{"<编号>":{"src":"该编号原行逐字回显","tr":"译文"}}`
	memoryRule := ""
	if askMemory {
		spec = `{"translations":{"<编号>":{"src":"该编号原行逐字回显","tr":"译文"}},"summary":"用一句话中文概括当前场景/话题/说话人语气,不超过100字","glossary":{"原文术语":"统一译法"}}`
		memoryRule = `
5. summary 概括本批所处场景供后续批次衔接;glossary 只收需要全片译法一致的人物名、地名、称谓、专名等(普通词汇不要),没有合适词条时给空对象 {}。`
	}
	system := fmt.Sprintf(`你是专业的字幕翻译引擎,把编号字幕行逐条翻译成%s。
规则:
1. 逐条独立翻译,严禁合并、拆分或增删条目。
2. 字幕文体:口语自然、简洁;单条译文长度尽量贴近原文,便于字幕显示。
3. 专有名词、数字、代码保持原样;纯语气词可省略。
4. 输出严格 JSON,不要任何解释、注释或代码围栏:
%s%s`, tgtName, spec, memoryRule)
	if disambig != "" {
		system += "\n" + disambig
	}
	var b strings.Builder
	appendMemoryContext(&b, mem)
	for i, l := range lines {
		fmt.Fprintf(&b, "%d. %s\n", i+1, l)
	}
	return system, b.String()
}

// appendMemoryContext 把跨批记忆写进用户提示(编号行之前),供模型衔接上下文。
func appendMemoryContext(b *strings.Builder, mem *TranslationMemory) {
	if mem == nil {
		return
	}
	if mem.Summary != "" {
		fmt.Fprintf(b, "前文场景摘要(仅供衔接上下文,不要翻译):%s\n", mem.Summary)
	}
	terms := injectGlossary(mem.Glossary)
	if len(terms) > 0 {
		b.WriteString("术语表(沿用以下译法保持全片一致;上下文明显不符时可给出更恰当译法):\n")
		for _, t := range terms {
			fmt.Fprintf(b, "- %s → %s\n", t.Src, t.Dst)
		}
	}
	b.WriteString("\n")
}

// aiParsed 解析产物:Entries 为批内编号条目;Simple 标记旧协议 {id:译文};
// Summary/Glossary 仅 wrapper 协议(带跨批记忆的首轮)回传,其余形态为空。
type aiParsed struct {
	Entries  map[string]subtitle.EchoEntry
	Simple   bool
	Summary  string
	Glossary map[string]string
}

// parseAIResponse 宽松解析 AI 响应,形态探测顺序:wrapper(translations+summary+
// glossary)→ 锚定(map[id]EchoEntry)→ 旧协议(map[id]string,simple=true)。
// 支持围栏/前置说明/think 块/尾逗号,零第三方依赖。
func parseAIResponse(raw string) (*aiParsed, error) {
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
		return nil, fmt.Errorf("AI 响应中没有 JSON 对象")
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
	// wrapper 协议:未知顶层字段被忽略,裸锚定/旧协议在此 decode 后 translations 为空,
	// 自然落到后面的形态探测。
	var wrapper struct {
		Translations map[string]subtitle.EchoEntry `json:"translations"`
		Summary      string                        `json:"summary"`
		Glossary     map[string]string             `json:"glossary"`
	}
	if err := decode(text, &wrapper); err == nil && len(wrapper.Translations) > 0 && hasEcho(wrapper.Translations) {
		return &aiParsed{Entries: wrapper.Translations, Summary: wrapper.Summary, Glossary: wrapper.Glossary}, nil
	}
	var anchored map[string]subtitle.EchoEntry
	if err := decode(text, &anchored); err == nil && len(anchored) > 0 && hasEcho(anchored) {
		return &aiParsed{Entries: anchored}, nil
	}
	var simple map[string]string
	if err := decode(text, &simple); err != nil {
		return nil, fmt.Errorf("AI 响应 JSON 解析失败: %w", err)
	}
	out := make(map[string]subtitle.EchoEntry, len(simple))
	for k, v := range simple {
		out[k] = subtitle.EchoEntry{Tr: v}
	}
	return &aiParsed{Entries: out, Simple: true}, nil
}

// hasEcho 至少一条非空(回显或译文),用于排除「shape 可解析但全空」的退化形态
// (旧协议 {id:译文} 会解码成全零 EchoEntry,须放行到 simple 分支)。
func hasEcho(m map[string]subtitle.EchoEntry) bool {
	for _, e := range m {
		if e.SrcEcho != "" || e.Tr != "" {
			return true
		}
	}
	return false
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

// aiTranslateLines AI 源翻译一批(引擎按 20 条/批喂):首轮锚定请求(带跨批记忆时
// 用 wrapper 协议)→ 回显校验 → 疑似复制判定 → 单轮定点补翻(仅问题条目,批内编号
// 重排,协议同首轮但不请求记忆)→ 对齐输出。首轮解析成功即 Learn(摘要入会话记忆,
// 术语经 store 落库)。
func aiTranslateLines(ctx context.Context, cfg *config.Config, provider, model string,
	lines []string, srcLang, tgtLang string, mem *TranslationMemory) ([]string, AIDiag, error) {
	provider, model, err := resolveAIModel(cfg, provider, model)
	if err != nil {
		return nil, AIDiag{}, err
	}
	var diag AIDiag

	// 首轮:锚定协议整批请求
	system, user := buildAnchoredPrompt(lines, srcLang, tgtLang, mem, mem != nil)
	raw, err := aiCall(ctx, cfg, provider, model, system, user)
	if err != nil {
		return nil, diag, err
	}
	parsed, err := parseAIResponse(raw)
	if err != nil {
		return nil, diag, err
	}
	if mem != nil && !parsed.Simple {
		mem.Learn(parsed.Summary, glossaryTerms(parsed.Glossary))
	}
	verdict := validateAIResp(lines, parsed.Entries, parsed.Simple)
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

	// 单轮定点补翻:只重问问题条目(错位+疑似复制),批内编号重排,
	// 带同一份记忆上下文但不请求 summary/glossary(响应保持裸锚定)
	sub := make([]string, len(repairIdx))
	for k, idx := range repairIdx {
		sub[k] = lines[idx]
	}
	system2, user2 := buildAnchoredPrompt(sub, srcLang, tgtLang, mem, false)
	raw2, err := aiCall(ctx, cfg, provider, model, system2, user2)
	if err != nil {
		// 补翻失败不放大:保留首轮结果,问题条目无可信译文 → 计数 Untranslated
		diag.Untranslated += len(repairIdx)
		return out, diag, nil
	}
	parsed2, err := parseAIResponse(raw2)
	if err != nil {
		diag.Untranslated += len(repairIdx)
		return out, diag, nil
	}
	verdict2 := validateAIResp(sub, parsed2.Entries, parsed2.Simple)
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

// glossaryTerms 把 wrapper 响应的 glossary 对象转成术语切片(零依赖解析产物)。
func glossaryTerms(m map[string]string) []GlossaryTerm {
	if len(m) == 0 {
		return nil
	}
	out := make([]GlossaryTerm, 0, len(m))
	for k, v := range m {
		out = append(out, GlossaryTerm{Src: k, Dst: v})
	}
	return out
}
