package server

// 录音笔记加工层（/api/refine）：asr/minutes 成功任务的转写交给大模型二次加工
// （纪要/待办/日程/导图/自定义指令），SSE 协议与助手 chat、提示词 apply 一致——预检
// 错误走 JSON 包络，流开始后错误走 data: {"error":...} 事件，终止 {"done":true}。
// 结果按模式合并进任务 Summary.refined（minutes/todos/events/custom 各占一键，
// 同模式重跑覆盖旧值）；minutes 的条目 evidence 转写行号在流结束后解析为时间戳 t
// 再落盘；说话人渲染以 speaker_names 为最终事实源（见 speakers.go）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/refine"
	"github.com/yann0917/voxbox/internal/store"
)

// refineStream 流式底层测试缝：生产即 assistant.StreamCompose（system 参数化的
// 导出入口，/api/assistant/chat 的默认提示词链路不受影响）；server 包测试替换为
// 假实现离线跑协议（与 auth_test 的 limiter 覆写同一做法）。
var refineStream = assistant.StreamCompose

// refineReq 加工请求：task_id 为任务 uuid（store.Task.ID string 主键）。
type refineReq struct {
	TaskID      string `json:"task_id"`
	Mode        string `json:"mode"`        // minutes | todos | events | mindmap | custom
	Instruction string `json:"instruction"` // mode=custom 时必填
	Provider    string `json:"provider"`
	Model       string `json:"model"`
}

func (s *Server) refine(c *gin.Context) {
	var req refineReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if !refine.ValidMode(req.Mode) {
		fail(c, CodeBadRequest, "参数错误：mode 须为 minutes/todos/events/mindmap/custom")
		return
	}
	if req.Mode == refine.ModeCustom && strings.TrimSpace(req.Instruction) == "" {
		fail(c, CodeBadRequest, "参数错误：custom 模式须提供 instruction")
		return
	}
	p := assistant.Provider(req.Provider)
	if !assistant.KnownProvider(p) {
		fail(c, CodeBadRequest, "参数错误：未知平台 "+req.Provider)
		return
	}
	if !assistant.ModelAllowed(p, req.Model) {
		fail(c, CodeBadRequest, fmt.Sprintf("平台 %s 不支持模型 %s", assistant.Label(p), req.Model))
		return
	}

	// 任务链校验：存在→属主→工具→状态→转写非空。segments 只在识别/妙记成功后
	// 存在，限定 status=succeeded 同时避免与引擎完成写回竞态；越权同报不存在。
	t, err := s.svc.DB().GetTask(req.TaskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, CodeNotFound, "任务不存在")
		} else {
			failErr(c, err)
		}
		return
	}
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if t.Tool != "asr" && t.Tool != "minutes" {
		fail(c, CodeBadRequest, "仅语音识别（asr）/语音妙记（minutes）任务支持加工")
		return
	}
	if t.Status != store.StatusSucceeded {
		fail(c, CodeBadRequest, "仅已成功完成的任务支持加工")
		return
	}
	var sum map[string]any
	_ = json.Unmarshal([]byte(t.Summary), &sum)
	lines := transcriptLines(sum)
	transcript := renderTranscriptLines(lines)
	var lineTimes []float64
	if req.Mode == refine.ModeMinutes {
		// 纪要模式喂行号转写（evidence 锚点），并行保留每行的开始时间供回链映射
		if len(lines) == 0 {
			fail(c, CodeBadRequest, "任务无转写内容，无法加工")
			return
		}
		transcript = numberedTranscript(lines, 0)
		lineTimes = lineStartTimes(lines)
	}
	if transcript == "" {
		fail(c, CodeBadRequest, "任务无转写内容，无法加工")
		return
	}
	// events 模式：相对时间（如下周三）以录音日期为基准，日期附在转写头部
	if req.Mode == refine.ModeEvents {
		transcript = "录音日期：" + t.CreatedAt.Format("2006-01-02") + "\n\n" + transcript
	}
	system, user := refine.BuildMessages(req.Mode, req.Instruction, transcript)

	cfg := s.svc.Config()
	if err := assistant.CheckCredential(cfg, p); err != nil {
		failErr(c, err)
		return
	}

	writeEvent := sseWriter(c)
	var out strings.Builder
	err = refineStream(c.Request.Context(), cfg, p, req.Model, system,
		[]assistant.Message{{Role: "user", Content: user}}, func(delta string) {
			out.WriteString(delta) // 断连后回调仍可能到达：照常累积，是否落盘由 err 分支决定
			// 客户端断开后写事件失败不中断循环：请求上下文取消会让上游读取尽快退出
			_ = writeEvent(gin.H{"delta": delta})
		})
	if err != nil {
		// 客户端主动中止属正常交互：不算错误、不再发事件、不落盘
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": assistantErrCode(err), "message": err.Error()}})
		}
		return
	}
	if err := s.persistRefined(t, req.Mode, out.String(), lineTimes); err != nil {
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": CodeInternal, "message": "加工结果保存失败: " + err.Error()}})
		}
		return
	}
	writeEvent(gin.H{"done": true})
}

// persistRefined 加工结果按模式合并进 Summary.refined：重读任务取最新 Summary
// （保住流式期间并发发生的 speaker_names 改名），同模式覆盖旧值。todos/events/
// minutes 解析失败不报错给前端（流已正常结束）：原文存入对应键并记账 warning。
// lineTimes 仅 minutes 模式使用（evidence 行号→时间戳映射表）。
func (s *Server) persistRefined(t *store.Task, mode, out string, lineTimes []float64) error {
	// 空白输出跳过落盘：流正常结束但模型未返回有效内容时，覆写会抹掉旧加工结果
	// 并空刷 updated_at；SSE 照常 done（前端「没有返回内容」提示由空流渲染承担）。
	if strings.TrimSpace(out) == "" {
		return nil
	}
	latest, err := s.svc.DB().GetTask(t.ID)
	if err != nil {
		return err
	}
	var sum map[string]any
	_ = json.Unmarshal([]byte(latest.Summary), &sum)
	if sum == nil {
		sum = map[string]any{} // 空/损坏 Summary 从空对象起步（同 speakers.go）
	}
	refined, _ := sum["refined"].(map[string]any)
	if refined == nil {
		refined = map[string]any{}
	}
	switch mode {
	case refine.ModeTodos, refine.ModeEvents:
		if arr, ok := parseJSONArray(out); ok {
			refined[mode] = arr
		} else {
			refined[mode] = out // 原文兜底，不丢数据
			log.Printf("[refine] task %s mode %s: 大模型输出非合法 JSON 数组，原文已存入 refined.%s", t.ID, mode, mode)
		}
	case refine.ModeMinutes:
		if obj, ok := parseJSONObject(out); ok {
			refined[mode] = annotateMinutes(obj, lineTimes)
		} else {
			refined[mode] = out
			log.Printf("[refine] task %s mode %s: 大模型输出非合法 JSON 对象，原文已存入 refined.%s", t.ID, mode, mode)
		}
	default: // mindmap | custom：自由文本
		refined[mode] = strings.TrimSpace(out)
	}
	refined["updated_at"] = time.Now().Format(time.RFC3339)
	sum["refined"] = refined
	b, err := json.Marshal(sum)
	if err != nil {
		return err
	}
	// 条件更新沿用 GetTask 已验证的属主（admin 可代加工、无主存量同路径），行内
	// user_id 二次把关防竞态——与说话人改名同一写入口
	return s.svc.DB().UpdateTaskSummary(t.ID, latest.UserID, string(b))
}

// parseJSONArray 解析大模型输出的 JSON 数组；容忍 markdown 代码栅栏包裹。
// 仅接受数组（契约即纯 JSON 数组），对象/标量一律算失败走原文兜底。
func parseJSONArray(s string) ([]any, bool) {
	s = stripJSONFence(s)
	if s == "" {
		return nil, false
	}
	var arr []any
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, false
	}
	return arr, true
}

// parseJSONObject 解析大模型输出的 JSON 对象；容忍 markdown 代码栅栏包裹。
// 仅接受对象（minutes 契约即纯 JSON 对象），数组/标量一律算失败走原文兜底。
func parseJSONObject(s string) (map[string]any, bool) {
	s = stripJSONFence(s)
	if s == "" {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return nil, false
	}
	return obj, true
}

// stripJSONFence 剥掉大模型输出外层的 markdown 代码栅栏（若存在）。
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	return s
}

// refineLine 一行转写：自由文本渲染、行号渲染与 evidence→时间戳映射共用。
type refineLine struct {
	text    string
	startMS float64
	name    string // 说话人展示名（speaker_names 覆盖优先；无说话人键的行如妙记前缀行为空）
	timed   bool   // 有无真实时间戳（segments 行有，Summary.text 回退行无）
}

// transcriptLines 把 Summary 渲染成转写行列表：segments 逐段（空文本跳过），说话人
// 展示名以 speaker_names 为覆盖（千问段取 speaker_id）；无 segments（或全部空文本）
// 回落 Summary.text 单行（无时间戳）。两者皆空返回 nil。
func transcriptLines(sum map[string]any) []refineLine {
	segs, _ := sum["segments"].([]any)
	names, _ := sum["speaker_names"].(map[string]any)
	lines := make([]refineLine, 0, len(segs))
	for _, raw := range segs {
		seg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text, _ := seg["text"].(string)
		if strings.TrimSpace(text) == "" {
			continue
		}
		startMS, _ := seg["start_ms"].(float64)
		spk, _ := seg["speaker"].(string)
		if spk == "" {
			spk, _ = seg["speaker_id"].(string) // 千问 ASR segments 的说话人键
		}
		name := ""
		if spk != "" {
			name = "说话人" + spk
			if renamed, ok := names[spk].(string); ok && renamed != "" {
				name = renamed
			}
		}
		lines = append(lines, refineLine{text: text, startMS: startMS, name: name, timed: true})
	}
	if len(lines) > 0 {
		return lines
	}
	if text, _ := sum["text"].(string); strings.TrimSpace(text) != "" {
		return []refineLine{{text: strings.TrimSpace(text)}}
	}
	return nil
}

// renderTranscriptLines 自由文本模式的逐行转写：HH:MM:SS 说话人N：文本（本地 R2T2/
// 智谱等纯文本任务无时间戳，不伪造时间码前缀；妙记 segments 的 text 自带说话人前缀
// 且无说话人键——原样保留不解析）。
func renderTranscriptLines(lines []refineLine) string {
	var b strings.Builder
	for _, l := range lines {
		if l.timed {
			b.WriteString(clockMS(l.startMS))
			b.WriteByte(' ')
		}
		if l.name != "" {
			b.WriteString(l.name)
			b.WriteString("：")
		}
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// transcriptFromSummary 把 Summary.segments 渲染成逐行转写（自由文本模式用）。
// 无 segments（或全部空文本）时回落 Summary.text 整段全文；两者皆空返回空串。
func transcriptFromSummary(sum map[string]any) string {
	lines := transcriptLines(sum)
	if lines == nil {
		return ""
	}
	return renderTranscriptLines(lines)
}

// numberedTranscript minutes 模式的行号转写（evidence 行号的锚点形态，与纪要提示词
// 声明一致）：[L1 00:00:01 张三] 文本；无说话人的行省略名字段，未计时行（纯文本
// 回退）无时间码段。from 为起始行下标（行号 = 下标+1，实时纪要增量时由此续接）。
func numberedTranscript(lines []refineLine, from int) string {
	var b strings.Builder
	for i := from; i < len(lines); i++ {
		l := lines[i]
		fmt.Fprintf(&b, "[L%d", i+1)
		if l.timed {
			fmt.Fprintf(&b, " %s", clockMS(l.startMS))
		}
		if l.name != "" {
			fmt.Fprintf(&b, " %s", l.name)
		}
		b.WriteString("] ")
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// lineStartTimes 行号（1 起）→ 开始时间（毫秒）的对齐表；未计时行为 -1（evidence
// 指向它时视为无效证据，不产 t）。
func lineStartTimes(lines []refineLine) []float64 {
	out := make([]float64, len(lines))
	for i, l := range lines {
		if l.timed {
			out[i] = l.startMS
		} else {
			out[i] = -1
		}
	}
	return out
}

// annotateMinutes 把纪要 JSON 各条目的 evidence 转写行号解析成 t（最早有效证据行的
// 开始时间，毫秒），解析完成后剔除 evidence——落盘契约只带 t。行号非法（越界/非
// 数字/指向未计时行）的证据忽略；全部无效则该条不带 t（前端降级为纯文本时间码）。
// lineTimes 由 lineStartTimes 生成，与行号对齐。
func annotateMinutes(obj map[string]any, lineTimes []float64) map[string]any {
	// setT 就地改写单条目：evidence→t，随后剔除 evidence
	setT := func(item map[string]any) {
		ev, _ := item["evidence"].([]any)
		best := -1.0
		for _, e := range ev {
			n := evidenceLineNo(e)
			if n >= 1 && n <= len(lineTimes) {
				if ms := lineTimes[n-1]; ms >= 0 && (best < 0 || ms < best) {
					best = ms
				}
			}
		}
		delete(item, "evidence")
		if best >= 0 {
			item["t"] = int64(best)
		}
	}
	for _, key := range []string{"topics", "conclusions", "todos"} {
		arr, _ := obj[key].([]any)
		for _, raw := range arr {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if key == "topics" {
				if pts, ok := item["points"].([]any); ok {
					for _, p := range pts {
						if pm, ok := p.(map[string]any); ok {
							setT(pm)
						}
					}
				}
			}
			setT(item)
		}
	}
	return obj
}

// evidenceLineNo 证据值→行号：接受 "L4"/"4"/数字形态，非法返回 0（无效证据）。
func evidenceLineNo(v any) int {
	switch x := v.(type) {
	case float64:
		if x >= 1 && x == math.Trunc(x) {
			return int(x)
		}
	case string:
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(x), "L"))
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}

// clockMS 毫秒时间码 → HH:MM:SS（超一小时录音自然进位）。
func clockMS(ms float64) string {
	if ms < 0 {
		ms = 0
	}
	sec := int64(ms / 1000)
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}
