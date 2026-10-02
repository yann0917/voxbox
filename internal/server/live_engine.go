// live_engine.go 实时字幕引擎抽象:统一 volcengine bigmodel_async 流式客户端与
// audiocpp live 中转(含 ≤9min 轮转拼接)两种会话形态,供 /api/ws/live relay 驱动与测试注入。
package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
)

// liveSegment 分句形状(与 volcengine.ASRSegment 及任务 Summary.segments 的 JSON 契约一致)。
type liveSegment struct {
	Text    string `json:"text"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Speaker string `json:"speaker,omitempty"`
}

// liveUpdate 引擎增量(REPLACE 快照语义):
//   - Committed 已提交文本(火山=definite 分句累积;本地=全部 delta 拼接,即提交语义);
//   - Unstable 尾部进行中文本(火山专属;本地恒空);
//   - Segments 已提交分句(火山专属,含时间戳/说话人;本地无时间戳为 nil)。
type liveUpdate struct {
	Committed string
	Unstable  string
	Segments  []liveSegment
}

// liveResult 会话终态全量结果。DurationMS 火山取服务端 audio_info,本地按已喂音频字节
// 折算(含静音续命帧)。Degraded=true 表示会话异常中断但已保留部分识别文本(final 可
// 保存,内容可能不完整);零文本的异常才以错误收束。
type liveResult struct {
	Text       string
	DurationMS int64
	Segments   []liveSegment
	Degraded   bool
}

// liveEngine 一次实时识别会话(一实例一次会话):Open→Send…→Finish→Wait 收束。
// Updates 随会话终止关闭;终止后 Wait 立即返回(引擎内部终态已定)。
type liveEngine interface {
	Open(ctx context.Context) error
	Send(pcm []byte) error
	Finish() error
	Updates() <-chan liveUpdate
	Wait(ctx context.Context) (liveResult, error)
	Close() error
}

// volcLiveEngine 火山 bigmodel_async 适配:增量快照与终态直映射(字段一一对应)。
type volcLiveEngine struct {
	c *volcengine.ASRAsyncClient
}

func newVolcLiveEngine(cred volcengine.SpeechCred, opts volcengine.ASRAsyncOptions) *volcLiveEngine {
	return &volcLiveEngine{c: volcengine.NewASRAsyncClient(cred, opts)}
}

func (e *volcLiveEngine) Open(ctx context.Context) error { return e.c.Open(ctx) }
func (e *volcLiveEngine) Send(pcm []byte) error          { return e.c.Send(pcm) }
func (e *volcLiveEngine) Finish() error                  { return e.c.Finish() }
func (e *volcLiveEngine) Close() error                   { return e.c.Close() }

// Updates 转发增量快照(独立 goroutine + 缓冲通道:与底层客户端的背压通道解耦,
// 消费方停读时逐级背压至火山服务端,不丢弃;会话终止经 Done 解除转发阻塞防泄漏)。
func (e *volcLiveEngine) Updates() <-chan liveUpdate {
	out := make(chan liveUpdate, 16)
	go func() {
		defer close(out)
		for u := range e.c.Updates() {
			select {
			case out <- volcUpdateToLive(u):
			case <-e.c.Done():
				return
			}
		}
	}()
	return out
}

func (e *volcLiveEngine) Wait(ctx context.Context) (liveResult, error) {
	r, err := e.c.Wait(ctx)
	if err != nil {
		return liveResult{}, err
	}
	return liveResult{Text: r.Text, DurationMS: r.DurationMS, Segments: volcSegmentsToLive(r.Segments)}, nil
}

func volcUpdateToLive(u volcengine.ASRAsyncUpdate) liveUpdate {
	return liveUpdate{
		Committed: u.CommittedText,
		Unstable:  u.UnstableText,
		Segments:  volcSegmentsToLive(u.Segments),
	}
}

func volcSegmentsToLive(segs []volcengine.ASRSegment) []liveSegment {
	if len(segs) == 0 {
		return nil
	}
	out := make([]liveSegment, 0, len(segs))
	for _, s := range segs {
		out = append(out, liveSegment{Text: s.Text, StartMS: s.StartMS, EndMS: s.EndMS, Speaker: s.Speaker})
	}
	return out
}

// liveRotateEvery 本地会话轮转周期:服务端 total_timeout 600s 上限内留足余量(真机实测
// total 超时会话被服务端终止),到期收束当前会话(done 全量)并无缝开下一路,文本拼接。
const liveRotateEvery = 9 * time.Minute

// localLiveAudioBuffer 轮转期音频缓冲上限(chunk 数):轮转收束+重开约 1-2s,实时音频
// 320ms/块远够;超出即阻塞 Send 背压至客户端(浏览器 socket 攒包),不丢弃音频。
const localLiveAudioBuffer = 512

// liveSilenceKeepAlive 静音续命周期:定期向当前会话喂静音帧重置 audiocpp live 的 30s
// idle 计时(会议停顿是核心场景,>30s 无音频服务端即断流)。须明显小于 30s 阈值;
// 静音不产生识别文本,计入时长(口径=会话覆盖的音频时间)。
const liveSilenceKeepAlive = 15 * time.Second

// liveSilenceFrame 320ms 静音帧(s16le mono 16k = 10240B 零字节)。
var liveSilenceFrame = make([]byte, 32000*320/1000)

// localLiveEngine audiocpp live 中转适配:单会话 ≤liveRotateEvery,到期无缝轮转新会话,
// 文本拼接(committed 前缀=已收束会话 done 全量之和);拼接点有一处接缝(前会话句尾与
// 新会话句首间无停顿语义),记账于任务报告。
type localLiveEngine struct {
	tts          *localruntime.TTSRuntime
	modelID      string // 目录 asr 条目 id(内部推导 -stream 声明)
	opts         localruntime.LiveASROptions
	rotateEvery  time.Duration
	silenceEvery time.Duration // 静音续命周期(测试注入缩短)

	ctx     context.Context
	cancel  context.CancelFunc
	audio   chan []byte
	updates chan liveUpdate
	done    chan struct{} // run goroutine 收敛

	finishOnce sync.Once

	mu     sync.Mutex
	result liveResult
	err    error
}

func newLocalLiveEngine(tts *localruntime.TTSRuntime, modelID string, opts localruntime.LiveASROptions) *localLiveEngine {
	return &localLiveEngine{
		tts: tts, modelID: modelID, opts: opts,
		rotateEvery:  liveRotateEvery,
		silenceEvery: liveSilenceKeepAlive,
		audio:        make(chan []byte, localLiveAudioBuffer),
		updates:      make(chan liveUpdate, 16),
		done:         make(chan struct{}),
	}
}

// Open 记录生命周期并启动会话 goroutine(健康检查+首会话建立在其中进行,失败经 Wait 透出)。
func (e *localLiveEngine) Open(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)
	go e.run()
	return nil
}

func (e *localLiveEngine) Send(pcm []byte) error {
	select {
	case e.audio <- pcm:
		return nil
	case <-e.done: // run 已收敛(错误路径):不再接收音频,解除 relay 读循环阻塞
		return errors.New("本地实时识别会话已终止,不能再发送音频")
	case <-e.ctx.Done():
		return errors.New("本地实时识别会话已关闭,不能再发送音频")
	}
}

// Finish 结束音频输入:关闭音频队列,run goroutine 收束当前会话并产出终态。幂等。
func (e *localLiveEngine) Finish() error {
	e.finishOnce.Do(func() { close(e.audio) })
	return nil
}

func (e *localLiveEngine) Updates() <-chan liveUpdate { return e.updates }

// Wait 阻塞至 run goroutine 收敛,返回拼接全量(与会话错误)。updates 关闭后调用立即返回。
func (e *localLiveEngine) Wait(ctx context.Context) (liveResult, error) {
	select {
	case <-e.done:
	case <-ctx.Done():
		return liveResult{}, ctx.Err()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.result, e.err
}

func (e *localLiveEngine) Close() error {
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

// sessResult 单会话收束结果(会话泵 → run 主循环)。
type sessResult struct {
	text string // done 全量(缺失时回落 delta 累积)
	err  error
}

// run 会话主循环:开一路 live 会话→喂音频(实时增量转播+静音续命)→到期轮转→
// Finish 收束全量。每路会话一个事件泵 goroutine(建会话即启动,delta 实时外送);
// 全部退出路径收敛 done 并关闭 updates(relay 泵的 range 自然结束,Wait 随即返回);
// 异常退出只要有文本即降级交付(finishWith),不丢用户已说的内容。
func (e *localLiveEngine) run() {
	defer close(e.done)
	defer close(e.updates)

	var (
		committed string // 已收束会话的文本拼接(committed 前缀)
		fedBytes  int    // 已喂音频字节(时长折算,含静音续命帧)
	)
	// finishWith 收敛终态:有文本(含已收束会话前缀)即降级交付——Wait 返回结果而非
	// 错误,用户可保存已说的内容(30s idle 断流/引擎崩溃等异常不丢文本);零文本错误
	// 才经 Wait 透出(relay 走 error 帧)。
	finishWith := func(text string, err error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if text != "" {
			e.result = liveResult{Text: text, DurationMS: int64(fedBytes) / 32, Degraded: err != nil}
			return
		}
		if err != nil && e.err == nil {
			e.err = err
		}
		e.result = liveResult{DurationMS: int64(fedBytes) / 32}
	}
	// startSession 建一路会话并启动其事件泵:delta → 累积转播(带 committed 前缀),
	// 会话流终止后投递 sessResult。事件消费与转播同 goroutine:relay 停读时背压链路
	// 直达 audiocpp server,不丢文本。
	startSession := func(prefix string) (*localruntime.LiveASRSession, <-chan sessResult, error) {
		sess, err := e.tts.StartLiveASR(e.ctx, e.modelID, e.opts)
		if err != nil {
			return nil, nil, err
		}
		res := make(chan sessResult, 1)
		go func() {
			var sb strings.Builder
			var final string
			var perr error
			for ev := range sess.Updates() {
				if ev.Err != nil {
					perr = ev.Err
					continue
				}
				if ev.Final != "" {
					final = ev.Final
					continue
				}
				sb.WriteString(ev.Delta)
				select {
				case e.updates <- liveUpdate{Committed: prefix + sb.String()}:
				case <-e.ctx.Done():
				}
			}
			text := final
			if text == "" {
				text = sb.String()
			}
			// 零文本且有会话错误(含连接中断)→ 错误收束;有文本时错误降级(尽力交付);
			// 零文本正常收束(纯静音/立即停止)不造错误:final{text:"",duration} 由
			// save 端「无可保存内容」拒绝,正常空会话不应按错误断连。
			if text == "" && perr == nil {
				if _, werr := sess.Wait(e.ctx); werr != nil {
					perr = werr
				}
			}
			res <- sessResult{text: text, err: perr}
		}()
		return sess, res, nil
	}
	// collect 收束当前会话:发终止 chunk → 等会话泵结果(ctx 取消时兜底 30s,防悬挂)。
	collect := func(sess *localruntime.LiveASRSession, resCh <-chan sessResult) (string, error) {
		_ = sess.Finish() // 已死会话(管道断)无害
		select {
		case r := <-resCh:
			return r.text, r.err
		case <-time.After(30 * time.Second):
			_ = sess.Close()
			return "", errors.New("等待本地识别会话收束超时")
		}
	}

	sess, resCh, err := startSession(committed)
	if err != nil {
		e.mu.Lock()
		if e.err == nil {
			e.err = err
		}
		e.mu.Unlock()
		return
	}
	timer := time.NewTimer(e.rotateEvery)
	defer timer.Stop()
	silence := time.NewTicker(e.silenceEvery)
	defer silence.Stop()

	// writePCM 喂一块音频:会话已死(服务端断流/引擎崩溃)时收束并降级保留已有文本,
	// 引擎继续等待 stop(客户端 stop 后经 Wait 拿到降级 final 可保存)。返回是否继续。
	writePCM := func(pcm []byte) bool {
		if _, err := sess.Write(pcm); err != nil {
			txt, perr := collect(sess, resCh)
			if perr == nil {
				perr = err
			}
			finishWith(committed+txt, perr)
			return false
		}
		return true
	}

	for {
		select {
		case pcm, ok := <-e.audio:
			if !ok {
				// 音频输入结束:收束当前会话产出全量并收敛。
				txt, perr := collect(sess, resCh)
				finishWith(committed+txt, perr)
				return
			}
			if !writePCM(pcm) {
				return
			}
			fedBytes += len(pcm)

		case <-silence.C:
			// 静音续命:重置服务端 idle 计时(会议停顿场景);会话已死则同音频路径收敛。
			if !writePCM(liveSilenceFrame) {
				return
			}
			fedBytes += len(liveSilenceFrame)

		case <-timer.C:
			// 轮转:收束当前会话(done 全量)→ 开新会话继续。音频在 e.audio 缓冲,
			// 客户端侧无感(轮转约 1-2s,缓冲富余);文本按 committed 前缀拼接。
			txt, perr := collect(sess, resCh)
			if perr != nil {
				finishWith(committed+txt, perr)
				return
			}
			committed += txt
			sess, resCh, err = startSession(committed)
			if err != nil {
				finishWith(committed, err) // 已收束文本降级保留
				return
			}
			timer.Reset(e.rotateEvery)

		case <-e.ctx.Done():
			_ = sess.Close()
			return
		}
	}
}
