package server

// 录音笔记加工层（/api/refine）：asr/minutes 成功任务的转写交给大模型二次加工
// （纪要/待办/日程/自定义指令），SSE 协议与助手 chat、提示词 apply 一致——预检
// 错误走 JSON 包络，流开始后错误走 data: {"error":...} 事件，终止 {"done":true}。
// 结果按模式合并进任务 Summary.refined（summary/todos/events/custom 各占一键，
// 同模式重跑覆盖旧值）；说话人渲染以 speaker_names 为最终事实源（见 speakers.go）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	Mode        string `json:"mode"`        // summary | todos | events | custom
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
		fail(c, CodeBadRequest, "参数错误：mode 须为 summary/todos/events/custom")
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
	transcript := transcriptFromSummary(sum)
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
	if err := s.persistRefined(t, req.Mode, out.String()); err != nil {
		if c.Request.Context().Err() == nil {
			writeEvent(gin.H{"error": gin.H{"code": CodeInternal, "message": "加工结果保存失败: " + err.Error()}})
		}
		return
	}
	writeEvent(gin.H{"done": true})
}

// persistRefined 加工结果按模式合并进 Summary.refined：重读任务取最新 Summary
// （保住流式期间并发发生的 speaker_names 改名），同模式覆盖旧值。todos/events
// 解析失败不报错给前端（流已正常结束）：原文存入对应键并记账 warning。
func (s *Server) persistRefined(t *store.Task, mode, out string) error {
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
	default: // summary | custom：自由文本
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
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	var arr []any
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, false
	}
	return arr, true
}

// transcriptFromSummary 把 Summary.segments 渲染成逐行转写：HH:MM:SS 说话人N：文本
// （speaker_names 有改名用改名，无编号说话人的行不补前缀；说话人编号取 speaker 键，
// 千问 ASR 的同名段用 speaker_id）；妙记 segments 的 text 自带「说话人N：」前缀且
// 无说话人键——原样保留不解析。无 segments（或全部空文本）时回落 Summary.text 整段
// 全文（本地 R2T2 / 智谱等纯文本识别任务无时间戳，不伪造时间码前缀）；两者皆空返回空串。
func transcriptFromSummary(sum map[string]any) string {
	segs, _ := sum["segments"].([]any)
	names, _ := sum["speaker_names"].(map[string]any)
	var b strings.Builder
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
		b.WriteString(clockMS(startMS))
		b.WriteByte(' ')
		spk, _ := seg["speaker"].(string)
		if spk == "" {
			spk, _ = seg["speaker_id"].(string) // 千问 ASR segments 的说话人键
		}
		if spk != "" {
			name := "说话人" + spk
			if renamed, ok := names[spk].(string); ok && renamed != "" {
				name = renamed
			}
			b.WriteString(name)
			b.WriteString("：")
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	if out := strings.TrimRight(b.String(), "\n"); out != "" {
		return out
	}
	text, _ := sum["text"].(string)
	return strings.TrimSpace(text)
}

// clockMS 毫秒时间码 → HH:MM:SS（超一小时录音自然进位）。
func clockMS(ms float64) string {
	if ms < 0 {
		ms = 0
	}
	sec := int64(ms / 1000)
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}
