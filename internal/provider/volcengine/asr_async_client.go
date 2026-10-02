package volcengine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

// asrAsyncPath 大模型流式语音识别（bigmodel_async 双向流式）WebSocket 路径。
// 鉴权/资源 ID/帧协议与 nostream 同构（sauc 包复用）；差异在会话形态：边喂音频边收增量响应。
const asrAsyncPath = "/api/v3/sauc/bigmodel_async"

// asrAsyncUpdateBuffer 增量更新通道缓冲。消费方（relay）不及时读时按背压阻塞读循环，
// 不丢弃更新；此时须调用 Close 解除阻塞（stopped 逃逸），否则读循环滞留在通道发送上。
const asrAsyncUpdateBuffer = 64

// ASRAsyncOptions 实时字幕会话选项。
//   - Language 语言，空则不传（服务端自动识别；enable_speaker_info 生效条件为 language 为空或 zh-CN）；
//   - Hotwords 热词，逗号/分号分隔（自动打包为协议要求的 corpus.context JSON 串，见 hotwordsContextJSON）；
//   - Speaker 说话人分离（true → enable_speaker_info；分句结果经顶层 speaker_id/additions 兜底映射）。
type ASRAsyncOptions struct {
	Language string
	Hotwords string
	Speaker  bool
}

// ASRAsyncUpdate 单帧增量更新（每帧 REPLACE 语义：均为当前全量快照，非增量追加）。
//   - Segments 全部 definite=true 分句（含 speaker 映射），顺序即时间序；
//   - CommittedText definite 分句文本累积（已提交、服务端承诺不回改）；
//   - UnstableText definite=false 的尾部进行中文本（下一帧可能变化）。
type ASRAsyncUpdate struct {
	Segments      []ASRSegment
	CommittedText string
	UnstableText  string
}

// ASRAsyncResult 会话最终结果（IsLastPackage 帧，二遍识别修正后的全量）。
type ASRAsyncResult struct {
	Text       string
	DurationMS int64
	Segments   []ASRSegment
}

// ASRAsyncClient 火山引擎大模型流式语音识别（sauc bigmodel_async 双向流式）WebSocket 客户端。
// 与 nostream 的差异：调用方按实时节奏反复 Send 音频分片，同一连接上持续接收增量识别响应。
//
// 生命周期：一实例一次会话——Open 至多调用一次（含失败尝试，重试请新建实例）。任何退出路径
// （正常终帧/错误帧/ctx 取消/Close/会话建立失败）都保证：连接关闭、ctx watcher 收敛、
// updates/done 通道收尾——Send/Finish 转为报错、Wait 返回终态、range Updates 自然结束，无悬挂。
//
// 并发模型：
//   - 写方向（Send/Finish/Close/shutdownConn/markOpenedFailed）经 writeMu 串行化，
//     gorilla WS 单写者约束成立；
//   - 读方向仅 readLoop 一个 goroutine 读连接（conn/ctx 在 readLoop 存续期内只读）；
//   - 跨 goroutine 数据仅：updates 通道（增量快照）、done 关闭 + endMu 保护终态、
//     openCalled/closedByUser/ended 原子位；
//   - ctx 取消联动关连接解除读写阻塞（nostream 同款）。
type ASRAsyncClient struct {
	cred      SpeechCred
	wsBaseURL string
	opts      ASRAsyncOptions

	// Open 赋值一次后只读（readLoop 存续期内无改写）；通道构造期创建。
	conn    *websocket.Conn
	ctx     context.Context
	updates chan ASRAsyncUpdate
	done    chan struct{}
	stopped chan struct{} // closeStopped 关闭：读循环 updates 发送的逃逸信号（setEnd/Close 共用）

	openCalled   atomic.Bool // Open 已调用（含失败尝试；一实例一次会话）
	closedByUser atomic.Bool // Close 已调用（读循环区分主动放弃与异常断开）

	writeMu  sync.Mutex  // 串行化 Send/Finish/Close/shutdownConn 的连接写与状态变更
	seq      int32       // 下一个音频包序号（首个音频包 = 2，1 隐式归 full request，nostream 同款）
	pending  []byte      // 持有的最后一包：Finish 时以负 seq 上 wire，保证每个字节恰好发送一次
	finished bool        // Finish 已成功执行（写失败不置位，可重试）
	ended    atomic.Bool // 会话已终态（错误帧/连接断开/手动关闭），Send/Finish 据此快速失败

	endMu    sync.Mutex
	endErr   error
	endFinal ASRAsyncResult

	closeStoppedOnce sync.Once // stopped 单次关闭（setEnd 与 Close 共享幂等）

	closeOnce sync.Once
	ctxClosed chan struct{} // 关闭即收敛 ctx watcher；关闭方持 writeMu 且关闭后置 nil
	closeErr  error
}

// NewASRAsyncClient 生产构造（资源 ID 固定 asrResourceID，与 nostream 同值，时长计费）。
func NewASRAsyncClient(cred SpeechCred, opts ASRAsyncOptions) *ASRAsyncClient {
	return NewASRAsyncClientWithURL(cred, asrWSBaseURL, opts)
}

// NewASRAsyncClientWithURL 供测试注入 mock 地址（如 ws://127.0.0.1:port）。
func NewASRAsyncClientWithURL(cred SpeechCred, wsBaseURL string, opts ASRAsyncOptions) *ASRAsyncClient {
	return &ASRAsyncClient{
		cred:      cred,
		wsBaseURL: strings.TrimRight(wsBaseURL, "/"),
		opts:      opts,
		updates:   make(chan ASRAsyncUpdate, asrAsyncUpdateBuffer),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		seq:       2,
	}
}

// Open 拨号并发起会话：auth 头 + X-Api-Connect-Id → full client request（pcm 16k 单声道 16bit，
// enable_nonstream 二遍识别 + show_utterances 恒开）→ 同步读一帧 ack → 启动接收循环。
// 音频由后续 Send 提供（s16le mono 16k 原始 PCM）。失败路径统一收敛（markOpenedFailed）。
func (c *ASRAsyncClient) Open(ctx context.Context) error {
	if !c.openCalled.CompareAndSwap(false, true) {
		return errors.New("ASR 会话已打开过：一实例仅支持一次会话，重试请新建实例")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrResourceID)
	headers.Set("X-Api-Connect-Id", uuid.NewString())

	conn, httpResp, err := websocket.DefaultDialer.DialContext(ctx, c.wsBaseURL+asrAsyncPath, headers)
	if err != nil {
		c.markOpenedFailed() // 拨号失败同样收敛通道终态：Wait/Updates/Done 不悬挂
		if httpResp != nil {
			defer httpResp.Body.Close()
			if httpResp.StatusCode == http.StatusUnauthorized {
				return fmt.Errorf("%w: 火山 ASR 鉴权失败(HTTP 401)，请检查语音凭证配置", ErrAuth)
			}
			if httpResp.StatusCode == http.StatusForbidden {
				return fmt.Errorf("连接火山实时识别 WebSocket 失败(HTTP 403): %w，可能未开通流式语音识别2.0服务", err)
			}
			return fmt.Errorf("连接火山实时识别 WebSocket 失败(HTTP %d): %w", httpResp.StatusCode, err)
		}
		return fmt.Errorf("连接火山实时识别 WebSocket 失败: %w", err)
	}
	c.conn = conn
	c.ctx = ctx

	// ctx 取消联动：ctx 结束时关闭连接，解除读写阻塞；ctxClosed 关闭收敛本 goroutine
	// （nostream 先例）。channel 以局部变量捕获，避免 watcher 与置 nil 方的数据竞争。
	ctxClosed := make(chan struct{})
	c.ctxClosed = ctxClosed
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-ctxClosed:
		}
	}()

	payload := sauc.AsrRequestPayload{
		User: sauc.UserMeta{Uid: "voxbox"},
		Audio: sauc.AudioMeta{
			Format:   "pcm",
			Codec:    "raw",
			Rate:     sauc.DefaultSampleRate,
			Bits:     16,
			Channel:  1,
			Language: c.opts.Language,
		},
		Request: sauc.RequestMeta{
			ModelName:         "bigmodel",
			EnableITN:         true,
			EnablePUNC:        true,
			EnableLID:         true,
			EnableNonstream:   true, // 二遍识别：流式出增量，结束出修正全量
			ShowUtterances:    true,
			EnableSpeakerInfo: c.opts.Speaker,
		},
	}
	if c.opts.Hotwords != "" {
		payload.Request.Corpus.Context = hotwordsContextJSON(c.opts.Hotwords)
	}
	if err := writeASRFrame(conn, sauc.NewFullClientRequest(payload)); err != nil {
		c.markOpenedFailed()
		return fmt.Errorf("发送 ASR 请求失败: %w", asrCtxErr(ctx, err))
	}

	// 同步读一帧 ack。
	msg, err := readASRFrame(conn)
	if err != nil {
		c.markOpenedFailed()
		return fmt.Errorf("等待 ASR 确认帧失败: %w", asrCtxErr(ctx, err))
	}
	if err := checkASRResponse(sauc.ParseResponse(msg)); err != nil {
		c.markOpenedFailed()
		return err
	}

	go c.readLoop()
	return nil
}

// markOpenedFailed Open 失败（拨号/请求/ack 任一阶段）统一收敛：关 ctx watcher 与连接、
// 置终态并关闭 updates/done——保证调用方 defer Close、Wait、range Updates 均不挂死不 panic。
// openCalled 的 CAS 保证至多执行一次（通道恰好关闭一次）。调用方已持 writeMu。
func (c *ASRAsyncClient) markOpenedFailed() {
	if c.ctxClosed != nil {
		close(c.ctxClosed)
		c.ctxClosed = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.setEnd(ASRAsyncResult{}, errors.New("ASR 会话未建立"))
	close(c.updates)
	close(c.done)
}

// Updates 返回增量更新通道。每帧 REPLACE 快照（非追加）；通道随会话终止关闭。
// 消费方停读时缓冲满将阻塞读循环（背压至服务端），调用 Close 可解除并终止会话。
func (c *ASRAsyncClient) Updates() <-chan ASRAsyncUpdate {
	return c.updates
}

// Done 返回会话终止信号：Wait 可取终态（正常完成或错误）后关闭。
func (c *ASRAsyncClient) Done() <-chan struct{} {
	return c.done
}

// Send 推送一帧音频（s16le mono 16k 原始 PCM，建议 ≤200ms/包按实时节奏）。
// 最后一包持有在内存（pending），由 Finish 以负 seq 发出——与官方「最后一包 seq 取负」一致，
// 且不重复发送任何字节、调用方无需预知哪包是最后一包。会话终止后调用返回错误。
func (c *ASRAsyncClient) Send(pcm []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("ASR 会话未打开")
	}
	if c.finished {
		return errors.New("ASR 会话已结束，不能再发送音频")
	}
	if c.ended.Load() {
		return errors.New("ASR 会话已终止，不能再发送音频")
	}
	if len(pcm) == 0 {
		return nil
	}
	if c.pending != nil {
		if err := writeASRFrame(c.conn, sauc.NewAudioOnlyRequest(int(c.seq), c.pending)); err != nil {
			return fmt.Errorf("发送 ASR 音频分片失败(seq %d): %w", c.seq, asrCtxErr(c.ctx, err))
		}
		c.seq++
	}
	c.pending = pcm
	return nil
}

// Finish 结束音频输入：以负 seq 发出最后一包（无任何 Send 时发空负载负 seq 包），
// 服务端随后返回剩余增量与 IsLastPackage 终帧。写失败不置位（可重试）；重复调用幂等返回 nil。
func (c *ASRAsyncClient) Finish() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("ASR 会话未打开")
	}
	if c.finished {
		return nil
	}
	if c.ended.Load() {
		return errors.New("ASR 会话已终止，无法发送终止帧")
	}
	last := c.pending
	if last == nil {
		last = []byte{}
	}
	seq := -c.seq
	if err := writeASRFrame(c.conn, sauc.NewAudioOnlyRequest(int(seq), last)); err != nil {
		return fmt.Errorf("发送 ASR 终止帧失败(seq %d): %w", seq, asrCtxErr(c.ctx, err))
	}
	c.finished = true
	return nil
}

// Wait 阻塞至收到 IsLastPackage 终帧（或会话出错/关闭/ctx 取消），返回二遍识别修正后的
// 全量结果。Open 前/失败后调用立即返回错误不悬挂；ctx 只控制本次等待，提前返回用 Close。
func (c *ASRAsyncClient) Wait(ctx context.Context) (ASRAsyncResult, error) {
	if !c.openCalled.Load() {
		return ASRAsyncResult{}, errors.New("ASR 会话未打开")
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		return ASRAsyncResult{}, fmt.Errorf("等待 ASR 识别结果已取消: %w", ctx.Err())
	}
	c.endMu.Lock()
	defer c.endMu.Unlock()
	return c.endFinal, c.endErr
}

// Close 立即终止会话（幂等）：关闭连接解除读阻塞，并触发读循环的增量发送逃逸——消费方停读
// Updates 且缓冲打满时也能退出，终态经 Wait 以错误返回。正常结束路径读循环自会关闭连接并
// 收敛 ctx watcher（shutdownConn），Close 作上层兜底/提前放弃入口。
func (c *ASRAsyncClient) Close() error {
	c.closeOnce.Do(func() {
		c.closedByUser.Store(true)
		c.writeMu.Lock()
		if c.ctxClosed != nil {
			close(c.ctxClosed)
			c.ctxClosed = nil
		}
		if c.conn != nil {
			c.closeErr = c.conn.Close()
		}
		c.writeMu.Unlock()
		c.closeStopped() // 解除读循环在 updates 发送上的阻塞（背压场景逃逸）
	})
	return c.closeErr
}

// readLoop 接收循环：增量帧 → updates 通道（REPLACE 快照）；终帧（IsLastPackage）→ 终态并收尾。
// 任何错误（含 ctx 联动/手动关闭联动的连接断开）都置终态错误后退出。defer LIFO 顺序：
// shutdownConn（关 conn 收敛 watcher）→ close(updates) → close(done)，Wait 返回后消费方
// range 自然结束。
func (c *ASRAsyncClient) readLoop() {
	defer close(c.done)
	defer close(c.updates)
	defer c.shutdownConn()
	for {
		msg, err := readASRFrame(c.conn)
		if err != nil {
			if c.closedByUser.Load() {
				c.setEnd(ASRAsyncResult{}, errors.New("ASR 会话已手动关闭"))
			} else {
				c.setEnd(ASRAsyncResult{}, fmt.Errorf("读取火山实时识别响应失败: %w", asrCtxErr(c.ctx, err)))
			}
			return
		}
		resp := sauc.ParseResponse(msg)
		if err := checkASRResponse(resp); err != nil {
			c.setEnd(ASRAsyncResult{}, err)
			return
		}
		if resp.IsLastPackage {
			if resp.PayloadMsg == nil {
				c.setEnd(ASRAsyncResult{}, fmt.Errorf("火山实时识别最终响应缺少识别结果"))
				return
			}
			c.setEnd(ASRAsyncResult{
				Text:       resp.PayloadMsg.Result.Text,
				DurationMS: int64(resp.PayloadMsg.AudioInfo.Duration),
				Segments:   utteranceToSegments(resp.PayloadMsg.Result.Utterances),
			}, nil)
			return
		}
		if resp.PayloadMsg != nil && len(resp.PayloadMsg.Result.Utterances) > 0 {
			select {
			case c.updates <- buildASRAsyncUpdate(resp.PayloadMsg.Result.Utterances):
			case <-c.stopped: // Close 触发的逃逸（消费方停读背压）：置终态错误后退出，Wait 不悬挂
				c.setEnd(ASRAsyncResult{}, errors.New("ASR 会话已手动关闭，增量更新被放弃"))
				return
			}
		}
	}
}

// shutdownConn 读循环退出统一收尾：关闭连接并收敛 ctx watcher goroutine（nostream 先例：
// 会话结束两件都做）。持 writeMu 与 Close/Send/Finish 的连接操作互斥；连接已关时幂等无害。
func (c *ASRAsyncClient) shutdownConn() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.ctxClosed != nil {
		close(c.ctxClosed)
		c.ctxClosed = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// setEnd 记录会话终态并关闭停止信号（正常终帧与错误路径统一入口，解除 updates 发送阻塞）。
func (c *ASRAsyncClient) setEnd(final ASRAsyncResult, err error) {
	c.ended.Store(true) // Send/Finish 快速失败，先于通道关闭可见
	c.endMu.Lock()
	c.endFinal = final
	c.endErr = err
	c.endMu.Unlock()
	c.closeStopped()
}

// closeStopped 幂等关闭 stopped（setEnd 与 Close 共享同一次关闭），解除读循环在
// updates 发送上的阻塞。
func (c *ASRAsyncClient) closeStopped() {
	c.closeStoppedOnce.Do(func() { close(c.stopped) })
}

// buildASRAsyncUpdate utterances→增量快照：definite 分句映射 segments（复用 nostream 的
// speaker 映射，顶层 speaker_id 优先、additions.speaker_id 兜底）并累积文本；
// definite=false 归入 unstable。每帧全量替换，调用方直接覆盖本地状态即可。
func buildASRAsyncUpdate(us []sauc.UtteranceLike) ASRAsyncUpdate {
	definite := make([]sauc.UtteranceLike, 0, len(us))
	var unstable []string
	for _, u := range us {
		if u.Definite {
			definite = append(definite, u)
		} else {
			unstable = append(unstable, u.Text)
		}
	}
	var committed strings.Builder
	for _, u := range definite {
		committed.WriteString(u.Text)
	}
	return ASRAsyncUpdate{
		Segments:      utteranceToSegments(definite),
		CommittedText: committed.String(),
		UnstableText:  strings.Join(unstable, ""),
	}
}
