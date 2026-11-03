// live_minutes.go 实时纪要:火山会话的增量分句交给默认大模型滚动整理成结构化会议
// 纪要(cicada Notes 口径)——45s 周期增量更新(发当前纪要 JSON + 上下文与新行),
// 引擎终态后一次终稿全量重写;快照经 minutes 帧实时下发,保存任务时嵌入
// Summary.refined.minutes。生成失败静默停更(不打扰字幕流),下一轮重试;
// 无默认大模型凭证/非火山引擎不启用(本地引擎无时间戳分句,纪要无从回链)。
package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yann0917/voxbox/internal/assistant"
	"github.com/yann0917/voxbox/internal/refine"
)

// liveMinutesEvery 实时纪要增量周期:与识别延迟叠加约 1 分钟刷新一次纪要,
// 首个话题在首批分句后约 45s 出现。包级变量供测试注入缩短。
var liveMinutesEvery = 45 * time.Second

// liveMinutesGen 单个实时字幕会话的纪要生成器:run 单 goroutine 串行消费
// (周期 tick 与终稿请求天然不重叠),observe 由引擎泵并发喂行集。
type liveMinutesGen struct {
	srv     *Server
	p       assistant.Provider
	model   string
	finalCh chan struct{} // 终稿重写请求(缓冲 1,重复请求合并)

	mu       sync.Mutex
	lines    []refineLine   // 累积转写行(引擎 segments 快照整体替换)
	notes    map[string]any // 当前纪要(已 annotate:evidence→t);nil=尚无
	notesRaw string         // 当前纪要 JSON(增量 prompt 回显)
	covered  int            // 增量已覆盖的行数
	final    bool           // 终稿已产出(此后不再增量)
}

// newLiveMinutesGen 按默认大模型凭证创建;未配置凭证返回 nil(实时纪要静默关闭)。
func newLiveMinutesGen(s *Server) *liveMinutesGen {
	p, model, err := assistant.ResolveDefault(s.svc.Config())
	if err != nil {
		log.Printf("[live] 实时纪要未启用:%v", err)
		return nil
	}
	return &liveMinutesGen{srv: s, p: p, model: model, finalCh: make(chan struct{}, 1)}
}

// observe 引擎增量入列:segments 为 REPLACE 全量快照,整体替换行集(空文本行剔除)。
// 说话人用编号展示名(live 会话无改名,回链展示与历史详情改名后的行文不保证一致)。
func (g *liveMinutesGen) observe(u liveUpdate) {
	if len(u.Segments) == 0 {
		return
	}
	lines := make([]refineLine, 0, len(u.Segments))
	for _, s := range u.Segments {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		name := ""
		if s.Speaker != "" {
			name = "说话人" + s.Speaker
		}
		lines = append(lines, refineLine{text: s.Text, startMS: float64(s.StartMS), name: name, timed: true})
	}
	g.mu.Lock()
	g.lines = lines
	g.mu.Unlock()
}

// requestFinal 请求终稿全量重写(引擎终态后行集定格;重复请求合并)。
func (g *liveMinutesGen) requestFinal() {
	select {
	case g.finalCh <- struct{}{}:
	default:
	}
}

// snapshot 当前纪要快照(终稿未及完成时为最后增量版);尚无纪要返回 nil。
func (g *liveMinutesGen) snapshot() map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.notes
}

// run 生成主循环:45s 增量 tick + 终稿请求;ctx 取消即退出(连接收束)。
func (g *liveMinutesGen) run(ctx context.Context, push func(any) bool) {
	ticker := time.NewTicker(liveMinutesEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.tick(ctx, false, push)
		case <-g.finalCh:
			g.tick(ctx, true, push)
		}
	}
}

// tick 一次生成:增量=当前纪要 + 上下文 2 行 + 新行;终稿=全量重写(不回显旧纪要——
// 长会话增量回显会让模型漂移,与 cicada final 口径一致)。失败静默:不推错误帧、
// 保留旧纪要,增量下轮重试。
func (g *liveMinutesGen) tick(ctx context.Context, final bool, push func(any) bool) {
	g.mu.Lock()
	if !final && g.final { // 终稿已产出,不再增量
		g.mu.Unlock()
		return
	}
	if !final && g.covered >= len(g.lines) { // 无新行不调模型
		g.mu.Unlock()
		return
	}
	lines := append([]refineLine(nil), g.lines...)
	covered := g.covered
	notesRaw := g.notesRaw
	g.mu.Unlock()

	var system, user string
	if final {
		system, user = refine.BuildMessages(refine.ModeMinutes, "", numberedTranscript(lines, 0))
	} else {
		from := covered
		if from > 2 {
			from -= 2 // 上下文 2 行,新行衔接更稳
		} else {
			from = 0
		}
		system, user = refine.BuildMinutesIncremental(notesRaw, numberedTranscript(lines, from))
	}

	cfg := g.srv.svc.Config()
	if err := assistant.CheckCredential(cfg, g.p); err != nil {
		log.Printf("[live] 实时纪要跳过:%v", err)
		return
	}
	var out strings.Builder
	if err := refineStream(ctx, cfg, g.p, g.model, system,
		[]assistant.Message{{Role: "user", Content: user}}, func(delta string) {
			out.WriteString(delta)
		}); err != nil {
		log.Printf("[live] 实时纪要生成失败(静默,保留旧纪要):%v", err)
		return
	}
	obj, ok := parseJSONObject(out.String())
	if !ok {
		log.Printf("[live] 实时纪要输出非合法 JSON,本轮丢弃")
		return
	}
	annotated := annotateMinutes(obj, lineStartTimes(lines))

	g.mu.Lock()
	if !final {
		carryTimes(g.notes, annotated) // 模型回显旧条目缺证据时继承原时间(首次听到不后移)
	} else {
		g.final = true
	}
	g.notes = annotated
	raw, _ := json.Marshal(annotated)
	g.notesRaw = string(raw)
	g.covered = len(lines)
	g.mu.Unlock()

	push(liveMinutesMsg{Type: "minutes", Final: final, Notes: annotated})
}

// carryTimes 增量更新的时间继承:模型回显旧条目但未给有效 evidence(无 t)时沿用
// 旧 t;两侧都有 t 取更早值(「首次听到」的时间不后移)。条目按数组下标对位——
// 纪要滚动更新下标基本稳定,错位只损失回链精度不影响内容。
func carryTimes(oldNotes, newNotes map[string]any) {
	if oldNotes == nil || newNotes == nil {
		return
	}
	for _, key := range []string{"topics", "conclusions", "todos"} {
		oldArr, _ := oldNotes[key].([]any)
		newArr, _ := newNotes[key].([]any)
		for i := range newArr {
			nm, ok := newArr[i].(map[string]any)
			if !ok {
				continue
			}
			var om map[string]any
			if i < len(oldArr) {
				om, _ = oldArr[i].(map[string]any)
			}
			if key == "topics" {
				nPts, _ := nm["points"].([]any)
				var oPts []any
				if om != nil {
					oPts, _ = om["points"].([]any)
				}
				for j := range nPts {
					np, ok := nPts[j].(map[string]any)
					if !ok {
						continue
					}
					var op map[string]any
					if j < len(oPts) {
						op, _ = oPts[j].(map[string]any)
					}
					carryT(op, np)
				}
			}
			carryT(om, nm)
		}
	}
}

// carryT 单条目的 t 继承(规则见 carryTimes)。
func carryT(oldItem, newItem map[string]any) {
	if oldItem == nil || newItem == nil {
		return
	}
	if nt, ok := newItem["t"].(int64); ok {
		if ot, ok2 := oldItem["t"].(int64); ok2 && ot > 0 && ot < nt {
			newItem["t"] = ot
		}
		return
	}
	if ot, ok := oldItem["t"].(int64); ok {
		newItem["t"] = ot
	}
}
