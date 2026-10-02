// audiocpp_live.go 本地实时识别:audiocpp_server /v1/audio/transcriptions/live 会话客户端。
// 与离线 Transcribe 同一常驻 server(lazy_load/健康检查/崩溃自愈);差异在会话形态:
// chunked 请求体推原始 PCM,同一连接上读 SSE 增量(delta 逐段追加,done 全量,[DONE] 终止)。
package localruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// streamingASRModelID 离线 asr 条目 id → streaming 声明 id(startLocked 装配时同规则增补)。
func streamingASRModelID(catalogID string) string { return catalogID + "-stream" }

// liveASRIdleTimeout 服务端空闲断流阈值(真机实测 30s):仅文档/注释口径,客户端不主动触发。
const liveASRIdleTimeout = 30 * time.Second

// liveASRUpdateBuffer 增量事件通道缓冲。消费方(relay)不及时读时背压阻塞 SSE 读循环
// (不丢弃文本);Close 可解除阻塞并终止会话。
const liveASRUpdateBuffer = 64

// LiveASROptions 一次本地实时识别会话选项。
//   - Language 强制语种码(zh/en/...);空=不透传,服务端自动检测;
//   - Hotwords 热词/上下文(逗号分隔自由文本),经 prompt 查询参数 URL 编码透传。
type LiveASROptions struct {
	Language string
	Hotwords string
}

// LiveASREvent SSE 单事件解析结果。Delta(增量追加)/Final(done 全量,非空即终)互斥;
// Err 非 nil 表示会话出错(服务端 error 事件或连接异常)。
type LiveASREvent struct {
	Delta string
	Final string
	Err   error
}

// LiveASRSession 一路 /v1/audio/transcriptions/live 会话:调用方 Write 推 PCM,
// Updates 收增量,Finish 发 HTTP 终止 chunk 结束输入并等待 done,Wait 取全量文本。
//
// 生命周期:StartLiveASR 至多成功一次;Finish 后 Write 报错;Close 幂等立即放弃。
// 结束必须发终止 chunk(直接断连=服务端 30s 空闲超时报错,无 done 全量)——Finish 即此事。
type LiveASRSession struct {
	base    string
	ctx     context.Context // 会话生命周期(StartLiveASR 传入;取消联动断连)
	cancel  context.CancelFunc
	pw      *io.PipeWriter
	resp    *http.Response
	updates chan LiveASREvent
	stopped chan struct{} // Close 触发:读循环在 updates 发送上的逃逸信号

	finishOnce sync.Once // 终止 chunk 只发一次
	closeOnce  sync.Once

	mu         sync.Mutex // 保护以下可变字段(读循环与 Wait/Close 并发)
	finishSent bool
	finalText  string // done 事件全量;未收到 done 时回落累积文本
	accum      string // delta 累积(append-only 拼接,真机实测 = done 全量)
	sessionErr error
	ended      bool // 读循环已收敛(终态确定)
}

// NewURLTTSRuntime 直连既有 audiocpp_server 的测试构造(BaseURL 缝):不管理子进程、
// 不查模型目录。供 server 层单测/真机冒烟注入引擎地址。
func NewURLTTSRuntime(base string) *TTSRuntime {
	return &TTSRuntime{
		BaseURL:        base,
		healthInterval: 20 * time.Millisecond,
		procAttr:       func(cmd *exec.Cmd) error { return cmd.Start() },
	}
}

// LiveASRModelID 解析本地实时识别可用的 asr 条目:已安装条目里 confucius4_r2t2 家族
// (当前唯一支持 streaming 的家族)取目录序首个,返回其目录 id(StartLiveASR 内部推导
// streaming 声明 id)。未安装时报可提示错误。
func (t *TTSRuntime) LiveASRModelID() (string, error) {
	if t.models == nil {
		return "", errors.New("本地模型管理器未初始化")
	}
	for _, e := range t.models.List() {
		if e.Entry.Kind == "asr" && e.Entry.Family == "confucius4_r2t2" && t.models.Installed(e.Entry.ID) {
			return e.Entry.ID, nil
		}
	}
	return "", errors.New("本地实时识别模型未安装:请到设置页下载 Confucius4-R2T2")
}

// StartLiveASR 开一路本地实时识别会话:引擎健康就绪 → POST live(查询参数声明模型与
// 音频格式)→ 同连接 SSE 读循环后台运行。catalogModelID 是目录 asr 条目 id(如
// r2t2-q8_0),实际下发 streaming 声明 id(<id>-stream)。pcm 由后续 Write 推送
// (s16le mono 16k 原始 PCM,任意分块)。
func (t *TTSRuntime) StartLiveASR(ctx context.Context, catalogModelID string, opts LiveASROptions) (*LiveASRSession, error) {
	if catalogModelID == "" {
		return nil, errors.New("本地实时识别模型未指定")
	}
	base, err := t.ensureHealth(ctx)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	q := url.Values{}
	q.Set("model", streamingASRModelID(catalogModelID))
	q.Set("sample_rate", "16000")
	q.Set("channels", "1")
	q.Set("sample_format", "s16le")
	if opts.Language != "" {
		q.Set("language", opts.Language)
	}
	if opts.Hotwords != "" {
		q.Set("prompt", opts.Hotwords)
	}
	req, err := http.NewRequestWithContext(sctx, http.MethodPost,
		base+"/v1/audio/transcriptions/live?"+q.Encode(), pr)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("连接本地实时识别引擎失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("本地实时识别引擎响应异常: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	s := &LiveASRSession{
		base:    base,
		ctx:     sctx,
		cancel:  cancel,
		pw:      pw,
		resp:    resp,
		updates: make(chan LiveASREvent, liveASRUpdateBuffer),
		stopped: make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

// Write 推送一段 PCM(s16le mono 16k 原始字节)。单写者约定:调用方(relay)串行调用;
// Finish/Close 之后返回错误(管道已关闭)。
func (s *LiveASRSession) Write(pcm []byte) (int, error) {
	return s.pw.Write(pcm)
}

// Updates 返回增量事件通道;会话终态(完成/出错/关闭)后关闭。
func (s *LiveASRSession) Updates() <-chan LiveASREvent { return s.updates }

// Finish 结束音频输入:关闭请求体管道(HTTP 终止 chunk),服务端 flush done 全量 +
// [DONE] 后收尾。幂等;之后 Write 报错。不等待结果——用 Wait 或 range Updates。
func (s *LiveASRSession) Finish() error {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.finishSent = true
		s.mu.Unlock()
		// CloseWithError(nil) 即正常终止 chunk;读循环侧收到 EOF 属预期收尾。
		_ = s.pw.Close()
	})
	return nil
}

// Wait 阻塞至会话终态,返回全量文本(done 事件优先,未收到时回落 delta 累积)。
// Finish 后调用是正常收束路径;Close 提前放弃则返回错误。ctx 只控制本次等待。
func (s *LiveASRSession) Wait(ctx context.Context) (string, error) {
	s.mu.Lock()
	ended := s.ended
	s.mu.Unlock()
	if !ended {
		select {
		case <-s.updates: // 已关闭 → 终态已定
		case <-ctx.Done():
			return "", fmt.Errorf("等待本地识别结果已取消: %w", ctx.Err())
		}
		// 排空残余事件后再读终态(关闭的通道先吐完缓冲元素)
		for range s.updates {
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionErr != nil {
		return "", s.sessionErr
	}
	if s.finalText != "" {
		return s.finalText, nil
	}
	return s.accum, nil
}

// Close 立即放弃会话(幂等):取消 ctx 联动断连、关闭管道与响应体,解除读循环在
// updates 发送上的背压阻塞。正常收束路径(Finish→Wait)无需调用;relay 清理兜底用。
func (s *LiveASRSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopped)
		s.cancel()
		_ = s.pw.CloseWithError(errors.New("本地实时识别会话已关闭"))
		if s.resp != nil && s.resp.Body != nil {
			_ = s.resp.Body.Close()
		}
	})
	return nil
}

// liveSSEResponse live 端点 SSE data 行的 JSON 载荷(type 在载荷内,无 event: 行,真机实测)。
type liveSSEResponse struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Text  string `json:"text"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readLoop 逐行读 SSE:data: [DONE] → 收尾;transcript.text.delta → 累积并发事件;
// transcript.text.done → 记录全量;error → 记录错误并终止(服务端 error 事件后会话即终,
// 真机实测 error 后连接随之关闭)。连接 EOF:已 Finish 视为正常收尾(真机实测长会话
// [DONE] 可能随连接关闭丢失,done 事件或累积文本即结果),未 Finish 视为异常断流报错。
// 退出统一收敛:置终态 → 关闭 updates。
func (s *LiveASRSession) readLoop() {
	defer s.finishReadLoop()
	sc := bufio.NewReaderSize(s.resp.Body, 64<<10)
	var pending string // 半行残包(sse 帧 ≠ TCP 段对齐)
	handle := func(line string) {
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			return // event:/注释/空行:live 端点 type 在载荷内,忽略
		}
		data = strings.TrimSpace(data)
		switch {
		case data == "[DONE]":
			s.setEnded(nil)
		case data == "":
		default:
			var ev liveSSEResponse
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				return // 非 JSON 行忽略(未来可能加心跳/注释帧)
			}
			switch ev.Type {
			case "transcript.text.delta":
				s.mu.Lock()
				s.accum += ev.Delta
				s.mu.Unlock()
				s.emit(LiveASREvent{Delta: ev.Delta})
			case "transcript.text.done":
				s.mu.Lock()
				s.finalText = ev.Text
				s.mu.Unlock()
				s.emit(LiveASREvent{Final: ev.Text})
			case "error":
				msg := "本地实时识别引擎错误"
				if ev.Error != nil && ev.Error.Message != "" {
					msg = ev.Error.Message
				}
				s.emit(LiveASREvent{Err: errors.New(msg)})
				s.setEnded(errors.New(msg))
			}
		}
	}
	for {
		if s.isEnded() { // error 事件等已终态:不再读,立即收敛(updates 关闭,Wait 返回)
			return
		}
		line, err := sc.ReadString('\n')
		if line != "" {
			pending += strings.TrimRight(line, "\r\n")
			if strings.HasSuffix(line, "\n") {
				handle(pending)
				pending = ""
			}
		}
		if err != nil {
			if err != io.EOF {
				s.emit(LiveASREvent{Err: fmt.Errorf("读取本地实时识别响应失败: %w", err)})
				s.setEnded(fmt.Errorf("读取本地实时识别响应失败: %w", err))
				return
			}
			// EOF:半行残包先处理,再按是否已 Finish 裁定正常/异常
			handle(pending)
			s.mu.Lock()
			finished := s.finishSent
			final := s.finalText
			s.mu.Unlock()
			if finished || final != "" {
				s.setEnded(nil)
			} else {
				s.setEnded(errors.New("本地实时识别连接中断(未收到识别结果)"))
			}
			return
		}
	}
}

// isEnded 终态查询(读循环在 error 事件后快速退出用)。
func (s *LiveASRSession) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// emit 发送事件:消费方停读且缓冲满时背压阻塞;Close/stopped 触发逃逸放弃发送。
func (s *LiveASRSession) emit(ev LiveASREvent) {
	select {
	case s.updates <- ev:
	case <-s.stopped:
	case <-s.ctx.Done():
	}
}

// setEnded 记录会话终态错误(可空)并标记收敛;finalText/sessionErr 已有值时不覆盖。
func (s *LiveASRSession) setEnded(err error) {
	s.mu.Lock()
	if err != nil && s.sessionErr == nil {
		s.sessionErr = err
	}
	s.ended = true
	s.mu.Unlock()
}

// finishReadLoop 读循环统一收尾:关响应体、收敛 ctx、关闭 updates(range 自然结束)。
func (s *LiveASRSession) finishReadLoop() {
	if s.resp != nil && s.resp.Body != nil {
		_ = s.resp.Body.Close()
	}
	s.cancel()
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	close(s.updates)
}
