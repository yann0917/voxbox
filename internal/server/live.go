// live.go 实时字幕双向 WS 中转(/api/ws/live):浏览器边说边出字的音频与控制消息分流到
// 双引擎(火山 bigmodel_async 双向 WS / audiocpp live HTTP 中转),识别事件实时回推,
// 停止后可一键存为 asr 任务落入历史(见 live_save.go)。
//
// 消息协议(Task 3 前端契约):
//
//	客户端 → 服务端:
//	  文本帧  {"type":"start","engine":"volcengine"|"local","language":"zh-CN"?,
//	           "hotwords":"词1,词2"?,"speaker":false?,"minutes":false?}
//	                                                     开会(语言/热词/说话人/实时纪要可选)
//	  二进制帧 s16le mono 16k 原始 PCM 字节(任意分块,建议 ≤320ms/块)      推音频
//	  文本帧  {"type":"stop"}                                    结束输入,服务端回 final
//	  文本帧  {"type":"save"}                                    存为任务(final 后可用)
//	  文本帧  {"type":"keep_going"}                              继续记录(撤销静音自动收束倒计时)
//
//	服务端 → 客户端(均文本帧 JSON):
//	  {"type":"ready","engine":"..."}                            会话已建立
//	  {"type":"partial","committed":"…","unstable":"…"?,
//	   "segments":[{text,start_ms,end_ms,speaker}?]}             增量快照(REPLACE 覆盖);
//	                                                             本地引擎 committed=全部文本、无 unstable/segments
//	  {"type":"final","text":"…","duration_ms":n,"segments"?,"degraded":true?}
//	                                                             全量结果(stop 后二遍修正/本地 done);
//	                                                             degraded=true=会话异常中断但已保留部分文本
//	                                                             (本地断流与火山中途故障降级,内容可保存但可能不完整)
//	  {"type":"saved","task_id":"<uuid>"}                        已存任务(可多次 save,各生成新任务)
//	  {"type":"silence","countdown_ms":n}                        静音预告:有过语音后超时无新词下发,
//	                                                             倒计时结束仍无新词自动收束;新语音或
//	                                                             keep_going 撤销(全程无语音直接收束,不发此帧)
//	  {"type":"minutes","notes":{…},"final":true?}               实时纪要快照(火山会话+默认大模型可用),
//	                                                             final=true=定格后的终稿全量重写
//	  {"type":"error","message":"…"}                             错误;连接去留视阶段:start 构造/建立失败
//	                                                             与音频推送失败保留连接(可重试 start /
//	                                                             stop→save 降级),会话开启后的引擎终态
//	                                                             错误才终止连接关闭(前端重连开新会话)
//
// 一条连接一次会话:start→音频→stop→final→(save)*;再次 start 视为协议错误(前端重连开新会话)。
//
// 顺序与引擎差异语义(Task 3 前端注意):
//   - 消息按 type 分派即可,不要假设帧间顺序:ready 先于首个 partial 通常成立但无保证,
//     stop→final 之间也可能还有残余 partial(收束泵在 done 前外送尾段 delta);
//   - volcengine start 失败=同步 error(连接保留,可直接重试 start);
//     local 引擎故障(健康检查/建会话失败)发生在 start 之后(ready 已发)→异步 error +
//     连接关闭,前端须重连后再重试。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
)

// 实时字幕心跳:与 hub.go 同款(服务端周期 ping,浏览器协议层自动 pong 刷新读超时)。
const (
	livePingEvery = 30 * time.Second
	livePongWait  = 70 * time.Second // 须 > livePingEvery
)

// liveWaitFinalTimeout stop 后等待引擎终态的上限(二遍识别/done 收尾实测秒级,余量充分);
// 超时按错误收束,连接不再悬挂。
const liveWaitFinalTimeout = 90 * time.Second

// 实时字幕静音自动收束(cicada MeetingEndWatch 口径):信号是「词」而非声音——以引擎
// 增量文本为准,静音续命帧不产文本天然不计。全程无语音超阈值直接收束(nobodySpoke,
// 不发预告帧);有过语音后再超阈值先预告倒计时,期间新语音或 keep_going 撤销,到期
// 自动收束(已说内容照常定格保存)。阈值为包级变量供测试注入。
var (
	liveSilenceAfter     = 10 * time.Minute
	liveSilenceCountdown = 60 * time.Second
	liveSilenceCheck     = 5 * time.Second // 收束判定的轮询周期
)

// liveControlMsg 客户端控制消息。
type liveControlMsg struct {
	Type     string `json:"type"` // start|stop|save|keep_going
	Engine   string `json:"engine"`
	Language string `json:"language"`
	Hotwords string `json:"hotwords"`
	Speaker  bool   `json:"speaker"`
	Minutes  bool   `json:"minutes"` // 实时纪要(仅火山引擎+默认大模型可用时生效)
}

// 服务端 → 客户端消息形状。
type liveReadyMsg struct {
	Type   string `json:"type"`
	Engine string `json:"engine"`
}

type livePartialMsg struct {
	Type      string        `json:"type"`
	Committed string        `json:"committed"`
	Unstable  string        `json:"unstable,omitempty"`
	Segments  []liveSegment `json:"segments,omitempty"`
}

type liveFinalMsg struct {
	Type       string        `json:"type"`
	Text       string        `json:"text"`
	DurationMS int64         `json:"duration_ms"`
	Segments   []liveSegment `json:"segments,omitempty"`
	Degraded   bool          `json:"degraded,omitempty"` // 会话异常但已保留部分文本
}

type liveSavedMsg struct {
	Type   string `json:"type"`
	TaskID string `json:"task_id"`
}

type liveErrorMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// liveSilenceMsg 静音预告(自动收束倒计时)。
type liveSilenceMsg struct {
	Type        string `json:"type"`
	CountdownMS int64  `json:"countdown_ms"`
}

// liveMinutesMsg 实时纪要快照(final=true 为定格终稿)。
type liveMinutesMsg struct {
	Type  string         `json:"type"`
	Final bool           `json:"final,omitempty"`
	Notes map[string]any `json:"notes"`
}

// liveConnState 会话状态机:idle →(start)streaming →(stop)finishing →(final)final。
// error 后连接去留视阶段(与头部协议注释一致):start 构造/建立失败与音频推送失败保留
// 连接(可重试 start / stop→save 降级保存);会话开启后的引擎终态错误才终止连接关闭
// (前端按断连处理,重连开新会话)。
type liveConnState int

const (
	liveStateIdle liveConnState = iota
	liveStateStreaming
	liveStateFinishing
	liveStateFinal
)

// newLiveEngineFn 引擎构造缝(生产=按 engine 参数分派;单测注入 mock)。
type newLiveEngineFn func(p liveControlMsg) (eng liveEngine, engine, providerName string, err error)

// wsLive WebSocket 升级入口(requireAuth 已注入身份;Origin 校验复用 hub 的 up)。
func (s *Server) wsLive(c *gin.Context) {
	p := principalFrom(c)
	if p == nil {
		return
	}
	conn, err := up.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	s.serveLiveConn(conn, p.ID)
}

// liveFinal 引擎终态投递(pump → 主循环)。
type liveFinal struct {
	res liveResult
	err error
}

// serveLiveConn 单连接生命周期:读泵喂消息、引擎泵转播增量、主循环状态机收束。
func (s *Server) serveLiveConn(conn *websocket.Conn, userID string) {
	var (
		send      = make(chan []byte, 64)
		msgs      = make(chan liveInMsg, 16)
		finalCh   = make(chan liveFinal, 1)
		readDone  = make(chan struct{}) // 读 goroutine 退出(连接已死)
		writeDone = make(chan struct{}) // 写泵退出(残余帧已排空)
	)
	go func() {
		defer close(writeDone)
		liveWritePump(conn, send)
	}()
	go liveReadPump(conn, msgs, readDone)

	var (
		wg      sync.WaitGroup // 引擎泵
		genWG   sync.WaitGroup // 实时纪要生成(与引擎泵分账:gen 推帧可能晚于引擎泵退出)
		eng     liveEngine
		engine  string // volcengine|local(错误消息与 save 用)
		prov    string // 落库 provider
		state   = liveStateIdle
		result  liveResult
		started bool // 一连接一次会话(含失败尝试:Open 失败后不允许二次 start,防半开会话)

		// pendingFinal 引擎终态早于客户端 stop 受理时挂起(引擎终态只能由 Finish 触发,
		// 二者经不同通道到达存在时序差;stop 受理时补发 final,协议顺序恒为 stop→final)。
		pendingFinal *liveFinal

		// 静音自动收束状态(仅主循环读写;lastText 由引擎泵原子刷新)
		startedAt       time.Time
		lastText        atomic.Int64 // 最近一次新文本的 unix nano(0=全程无语音)
		silenceArmed    bool
		armAt           time.Time
		silenceDeadline time.Time

		// gen 实时纪要生成器(start 时按开关+引擎+凭证创建;nil=未启用)
		gen *liveMinutesGen
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		if eng != nil {
			eng.Close() // 幂等;引擎泵经 updates 关闭/ctx 逃逸收敛
		}
		wg.Wait()
		genWG.Wait() // 纪要流随 ctx 取消退出后再关通道,防 push 撞上已关闭的 send
		close(send)
		<-writeDone // 写泵排空残余帧(终态 error/final)后再关连接,防帧丢失
		_ = conn.Close()
	}()

	push := func(v any) bool {
		raw, err := json.Marshal(v)
		if err != nil {
			return true
		}
		select {
		case send <- raw:
			return true
		case <-time.After(writeWait): // 写泵阻塞(客户端停读)时快速放弃,避免主循环悬挂
			return false
		}
	}
	fail := func(msg string) {
		push(liveErrorMsg{Type: "error", Message: msg})
	}

	// settleFinishing 进入 finishing 并收敛在途终态(stop 受理与自动收束共用):
	// 引擎终态早于收束动作到达时（pendingFinal 挂起中），此处立即补发 final，
	// 协议顺序恒为 收束→final。
	settleFinishing := func() {
		state = liveStateFinishing
		if pendingFinal != nil {
			result = pendingFinal.res
			push(liveFinalMsg{Type: "final", Text: result.Text, DurationMS: result.DurationMS, Segments: result.Segments, Degraded: result.Degraded})
			state = liveStateFinal
			pendingFinal = nil
		}
	}

	// handleMsg 单条客户端消息处理(连接级终止只由 readDone/终态错误触发)。
	handleMsg := func(m liveInMsg) {
		switch m.msgType {
		case websocket.TextMessage:
			var ctrl liveControlMsg
			if err := json.Unmarshal(m.data, &ctrl); err != nil {
				fail("控制消息无法解析,须为 JSON 文本帧")
				return
			}
			switch ctrl.Type {
			case "start":
				if started {
					fail("本连接已开启过会话:停止后请断开重连再开始新会话")
					return
				}
				if state != liveStateIdle {
					fail("会话已在进行中")
					return
				}
				e, engName, provName, err := s.newLiveEngine(ctrl)
				if err != nil {
					fail(err.Error())
					return // 构造失败不占会话(未 Open),可重试 start
				}
				if err := e.Open(ctx); err != nil {
					e.Close()
					fail(err.Error())
					return
				}
				eng, engine, prov, started = e, engName, provName, true
				state = liveStateStreaming
				startedAt = time.Now()
				// 实时纪要:开关+火山引擎+默认大模型凭证三者齐备才启用(本地引擎无时间戳
				// 分句,纪要无从回链,不启用)
				if ctrl.Minutes && engName == "volcengine" {
					gen = newLiveMinutesGen(s)
				}
				if gen != nil {
					genWG.Add(1)
					go func() {
						defer genWG.Done()
						gen.run(ctx, push)
					}()
				}
				// 引擎泵:增量转播(顺带刷新静音信号、喂纪要行集);updates 关闭(会话终态)
				// 后请终稿重写并 Wait 取终态投递 finalCh。
				wg.Add(1)
				go func(e liveEngine) {
					defer wg.Done()
					prevText := 0
					for u := range e.Updates() {
						// 静音信号:合并文本长度增长才算「说了词」(unstable 抖动/静音续命帧不计)
						if n := len(u.Committed) + len(u.Unstable); n > prevText {
							prevText = n
							lastText.Store(time.Now().UnixNano())
						}
						if gen != nil {
							gen.observe(u)
						}
						if !push(livePartialMsg{Type: "partial", Committed: u.Committed, Unstable: u.Unstable, Segments: u.Segments}) {
							return
						}
					}
					if gen != nil {
						gen.requestFinal() // 引擎终态:行集定格,终稿全量重写(与 Wait 收束并行)
					}
					wctx, wcancel := context.WithTimeout(context.Background(), liveWaitFinalTimeout)
					defer wcancel()
					res, err := e.Wait(wctx)
					finalCh <- liveFinal{res: res, err: err}
				}(e)
				push(liveReadyMsg{Type: "ready", Engine: engName})

			case "stop":
				if state != liveStateStreaming {
					fail("当前状态不能停止(须先 start 并在会话中)")
					return
				}
				if err := eng.Finish(); err != nil {
					fail("结束音频输入失败:" + err.Error())
					return
				}
				settleFinishing()

			case "keep_going":
				// 撤销静音倒计时并重置静音计时(下一轮静音重新计满);不影响状态机
				if state == liveStateStreaming {
					silenceArmed = false
					lastText.Store(time.Now().UnixNano())
				}

			case "save":
				if state != liveStateFinal {
					fail("请先停止识别(final 之后)再保存")
					return
				}
				var notes map[string]any
				if gen != nil {
					notes = gen.snapshot() // 终稿未及完成时为最后增量版,任务详情可再加工
				}
				taskID, err := s.liveSave(userID, engine, prov, result, notes)
				if err != nil {
					fail("保存任务失败:" + err.Error())
					return
				}
				push(liveSavedMsg{Type: "saved", TaskID: taskID})

			default:
				fail("未知控制消息类型 " + ctrl.Type)
			}

		case websocket.BinaryMessage:
			if state != liveStateStreaming {
				fail("音频帧须在会话中(start 之后)发送")
				return
			}
			if err := eng.Send(m.data); err != nil {
				// 会话已不接受音频(引擎终止/本地降级收敛):报错但保留连接——引擎泵的
				// 终态投递与客户端 stop→save(降级文本)仍需这条连接,不在此关闭。
				fail("推送音频失败:" + err.Error())
				return
			}
		}
		return
	}

	silenceCheck := time.NewTicker(liveSilenceCheck)
	defer silenceCheck.Stop()

	for {
		// 控制消息优先排空:stop 与引擎终态可能同时就绪(select 随机),协议上 stop
		// 恒先于 final 被受理,否则 final 会抢在 finishing 之前把连接按意外终止收掉。
		select {
		case m := <-msgs:
			handleMsg(m)
			continue
		default:
		}
		select {
		case <-readDone: // 连接已断:清理即收敛
			return

		case <-silenceCheck.C: // 静音自动收束判定(仅会话中;详见 liveSilenceAfter 注释)
			if state == liveStateStreaming {
				now := time.Now()
				last := lastText.Load()
				switch {
				case last == 0 && now.Sub(startedAt) > liveSilenceAfter:
					// 全程无语音(nobodySpoke):直接收束,final 空文本由前端「无可保存内容」承接
					_ = eng.Finish()
					settleFinishing()
				case silenceArmed && last > 0 && time.Unix(0, last).After(armAt):
					// 倒计时期间来了新语音:撤销预告
					silenceArmed = false
				case silenceArmed && now.After(silenceDeadline):
					// 倒计时到期仍无新语音:自动收束(已说内容照常定格保存)
					silenceArmed = false
					_ = eng.Finish()
					settleFinishing()
				case !silenceArmed && last > 0 && now.Sub(time.Unix(0, last)) > liveSilenceAfter:
					// 静音超阈值:预告倒计时
					silenceArmed = true
					armAt = now
					silenceDeadline = now.Add(liveSilenceCountdown)
					push(liveSilenceMsg{Type: "silence", CountdownMS: liveSilenceCountdown.Milliseconds()})
				}
			}

		case fin := <-finalCh: // 引擎终态(stop 收束 或 会话中途结束)
			if fin.err != nil {
				fail("实时识别会话结束:" + fin.err.Error())
				return // 会话已终,连接关闭(前端重连开新会话)
			}
			if state == liveStateStreaming {
				// stop 尚未受理(在途):挂起终态,stop 到达时补发 final。
				pf := fin
				pendingFinal = &pf
				break
			}
			if state != liveStateFinishing {
				// 非 finishing 的成功终态(会话中途自行结束)=意外终止,连接收束。
				return
			}
			result = fin.res
			push(liveFinalMsg{Type: "final", Text: fin.res.Text, DurationMS: fin.res.DurationMS, Segments: fin.res.Segments, Degraded: fin.res.Degraded})
			state = liveStateFinal

		case m := <-msgs:
			handleMsg(m)
		}
	}
}

// liveInMsg 读泵投递的消息。
type liveInMsg struct {
	msgType int
	data    []byte
}

// liveReadPump 读循环:仅读取与投递,不做业务(业务在主循环,Wait 阻塞不影响读保活)。
// 读超时靠对端 pong 刷新(浏览器协议层自动应答),僵死连接 2 个周期内被清理。
func liveReadPump(conn *websocket.Conn, msgs chan<- liveInMsg, done chan<- struct{}) {
	defer close(done)
	_ = conn.SetReadDeadline(time.Now().Add(livePongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(livePongWait))
	})
	conn.SetReadLimit(1 << 20) // 单帧上限:音频分块与控制消息远小于此,防滥用
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		select {
		case msgs <- liveInMsg{msgType: mt, data: data}:
		case <-time.After(writeWait): // 主循环卡死时放弃消息并退出,读超时最终收口
			return
		}
	}
}

// liveWritePump 写循环:send 帧下发 + 周期 ping(浏览器自动 pong);写失败关连接唤醒读泵。
func liveWritePump(conn *websocket.Conn, send <-chan []byte) {
	ticker := time.NewTicker(livePingEvery)
	defer ticker.Stop()
	for {
		select {
		case raw, ok := <-send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				_ = conn.Close()
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = conn.Close() // 唤醒读泵,连接回收
				return
			}
		}
	}
}

// defaultNewLiveEngine 生产引擎构造:火山取语音凭证(设置页/配置文件热快照),本地解析
// 已安装的 confucius4_r2t2 条目(引擎未装/模型未装在此报可提示错误)。
func (s *Server) defaultNewLiveEngine(p liveControlMsg) (liveEngine, string, string, error) {
	switch p.Engine {
	case "volcengine":
		cfg := s.svc.Config()
		cred := volcengine.SpeechCred{
			AppID: cfg.Volc.Speech.AppID, AccessToken: cfg.Volc.Speech.AccessToken, APIKey: cfg.Volc.Speech.APIKey,
		}
		if err := cred.Validate(); err != nil {
			return nil, "", "", errors.New("火山引擎未配置:请在设置页填写语音凭证(" + err.Error() + ")")
		}
		return newVolcLiveEngine(cred, volcengine.ASRAsyncOptions{
			Language: p.Language, Hotwords: p.Hotwords, Speaker: p.Speaker,
		}), "volcengine", "volcengine", nil
	case "local":
		tts := s.svc.TTSRuntime()
		modelID, err := tts.LiveASRModelID()
		if err != nil {
			return nil, "", "", err
		}
		return newLocalLiveEngine(tts, modelID, localruntime.LiveASROptions{
			Language: p.Language, Hotwords: p.Hotwords,
		}), "local", "local", nil
	default:
		return nil, "", "", errors.New("未知引擎 " + p.Engine + "(支持 volcengine / local)")
	}
}
