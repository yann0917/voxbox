// 播客 v3 WebSocket 客户端：事件流驱动（逐轮文本实时上抛）+ 断点续传。
// 帧协议见 podframe.go；payload/retry 约定见官方文档 6561/1668014。
package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// podWSBaseURL 生产环境播客 v3 WebSocket 服务地址。
	podWSBaseURL = "wss://openspeech.bytedance.com"
	// podWSPath 播客 v3 WebSocket 路径。
	podWSPath = "/api/v3/sami/podcasttts"
	// podAppKey 播客 v3 协议族固定 App Key（官方文档指定值，与账号无关）。
	podAppKey = "aGjiRDfUWi"
	// podResourceID 播客服务资源 ID（官方文档 Request Headers 必填项）。
	podResourceID = "volc.service_type.10050"
	// podReadTimeout 每次读帧前设置的超时：单轮对话合成可能耗时数分钟。
	podReadTimeout = 5 * time.Minute
	// podWriteTimeout 每次写帧前设置的超时。
	podWriteTimeout = 30 * time.Second
	// podMaxAttempts 生成总尝试次数：首连 + 最多 2 次断点续传。
	podMaxAttempts = 3
	// podDefaultFormat audio_config.format 缺省值；podSampleRate 固定采样率。
	podDefaultFormat = "mp3"
	podSampleRate    = 24000
	// StartSession payload 的 action 取值：0 文本/网页，3 对话稿。
	podActionText   = 0
	podActionScript = 3
)

// PodcastTurn 对话稿单轮（action=3 模式）。
type PodcastTurn struct{ Speaker, Text string }

// PodcastRequest 播客生成请求：InputText/URL/Script 三选一，同时提供时按 Script > URL > InputText 分派。
type PodcastRequest struct {
	InputText string        // action=0 文本（与 URL/Script 三选一）
	URL       string        // 网页模式（action=0 + input_info）
	Script    []PodcastTurn // action=3 对话稿
	Speakers  [2]string     // 恰好两个非空音色 ID，顺序即 A/B 说话人
	Format    string        // mp3|ogg_opus|pcm|aac，空回退 mp3
	HeadMusic bool          // 是否生成开头音乐
}

// PodcastRound 一轮对话（360 事件收集，362 事件补时长；round_id=-1 为开头音乐、9999 为结尾音频）。
type PodcastRound struct {
	RoundID   int
	Speaker   string
	Text      string
	DurationS float64
}

// PodcastResult 生成结果：Audio 为 361 分片按序拼接；AudioURL 来自 363（1 小时有效，可能为空）。
type PodcastResult struct {
	Audio    []byte
	Format   string
	Rounds   []PodcastRound
	AudioURL string
	Usage    map[string]int64
}

// PodcastClient 火山引擎语音播客 v3 WebSocket 客户端。
type PodcastClient struct {
	cred      SpeechCred
	wsBaseURL string
}

func NewPodcastClient(cred SpeechCred) *PodcastClient {
	return NewPodcastClientWithURL(cred, podWSBaseURL)
}

// NewPodcastClientWithURL 供测试注入 mock 地址（如 ws://127.0.0.1:port），生产用 NewPodcastClient。
func NewPodcastClientWithURL(cred SpeechCred, wsBaseURL string) *PodcastClient {
	return &PodcastClient{cred: cred, wsBaseURL: strings.TrimRight(wsBaseURL, "/")}
}

// podAccum 跨连接尝试累积的生成状态（断点续传不重置已收到的音频与轮次）。
type podAccum struct {
	audio              *bytes.Buffer
	roundStartAudioLen int // 最近一轮 360 到来时的音频长度：续传重发该轮时据此截断已收的不完整分片
	rounds             []PodcastRound
	usage              map[string]int64
	audioURL           string
	lastFinishedRound  int // 最近一次 362 对应的轮次 ID（round_id=-1 的开头音乐不计）；0 表示尚无完成轮次
}

// podFatalError 不可续传的致命错误（服务端错误帧等）：重连同参数注定失败，Generate 遇到立即终止。
type podFatalError struct{ err error }

func (e *podFatalError) Error() string { return e.err.Error() }
func (e *podFatalError) Unwrap() error { return e.err }

// Generate 生成双人播客（同步调用，数分钟级）。
// onRound 在收到 360 轮次事件时回调（轮次文本实时上抛供对话流进度展示），可为 nil。
// 连接中断（未收到 152）自动以同一 X-Api-Request-Id 携带 retry_info 续传，
// 首连 + 最多 2 次续传共 3 次，全部失败返回最后一次错误；服务端错误帧与 ctx 取消立即终止不重试。
func (c *PodcastClient) Generate(ctx context.Context, req PodcastRequest, onRound func(PodcastRound)) (PodcastResult, error) {
	if err := c.cred.ValidatePodcast(); err != nil {
		return PodcastResult{}, err
	}
	if err := validatePodSpeakers(req.Speakers); err != nil {
		return PodcastResult{}, err
	}
	// X-Api-Request-Id 即续传任务 ID：整个生成过程（含重连）必须复用同一值。
	taskID := uuid.NewString()
	acc := &podAccum{audio: &bytes.Buffer{}}
	lastRound := 0
	var lastErr error
	for attempt := 1; attempt <= podMaxAttempts; attempt++ {
		var retry *podRetryInfo
		if attempt > 1 { // 重连携带断点，服务端从 last_finished_round_id 之后继续
			retry = &podRetryInfo{RetryTaskID: taskID, LastFinishedRoundID: lastRound}
		}
		done, lr, err := c.generateOnce(ctx, req, taskID, retry, onRound, acc)
		lastRound = lr
		if done {
			return PodcastResult{
				Audio:    acc.audio.Bytes(),
				Format:   podAudioFormat(req.Format),
				Rounds:   acc.rounds,
				AudioURL: acc.audioURL,
				Usage:    acc.usage,
			}, nil
		}
		if ctx.Err() != nil {
			return PodcastResult{}, fmt.Errorf("播客生成已取消: %w", ctx.Err())
		}
		var fatal *podFatalError
		if errors.As(err, &fatal) { // 服务端错误帧：重连同参数注定失败，立即终止不再重连
			return PodcastResult{}, fmt.Errorf("播客生成失败: %w", err)
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("生成未完成")
	}
	return PodcastResult{}, fmt.Errorf("播客生成失败（含重连共尝试 %d 次）: %w", podMaxAttempts, lastErr)
}

// generateOnce 执行一次完整连接：Dial → StartSession → 读循环分派 → 152 后发 FinishConnection 等 52。
// 返回 done=是否收到 152（合成完成）；lastFinishedRound 为退出时的断点轮次（与 acc 累积值一致）；
// 连接中断、payload 非法等返回 err，由 Generate 决定是否续传；服务端错误帧返回 *podFatalError，不续传。
func (c *PodcastClient) generateOnce(ctx context.Context, req PodcastRequest, taskID string, retry *podRetryInfo, onRound func(PodcastRound), acc *podAccum) (done bool, lastFinishedRound int, err error) {
	payload, err := buildPodStartPayload(req, retry)
	if err != nil {
		return false, acc.lastFinishedRound, err
	}
	// 官方语义（文档 1668014）：第一次 StartSession 的 session_id 就是任务的 task_id，
	// 续传 retry_task_id 以首连 session_id 检索任务——故每次连接 session_id 统一取 taskID，
	// 保证 retry_task_id 与首连 session_id 恒等（X-Api-Request-Id 同值）。
	sessionID := taskID
	headers := http.Header{}
	headers.Set("X-Api-App-Id", c.cred.AppID)
	headers.Set("X-Api-Access-Key", c.cred.AccessToken)
	headers.Set("X-Api-App-Key", podAppKey)
	headers.Set("X-Api-Resource-Id", podResourceID)
	headers.Set("X-Api-Request-Id", taskID)
	headers.Set("X-Api-Connect-Id", uuid.NewString()) // 每次连接尝试独立

	conn, httpResp, err := websocket.DefaultDialer.DialContext(ctx, c.wsBaseURL+podWSPath, headers)
	if err != nil {
		if httpResp != nil {
			defer httpResp.Body.Close()
			if httpResp.StatusCode == http.StatusUnauthorized {
				return false, acc.lastFinishedRound, fmt.Errorf("%w: 火山播客鉴权失败(HTTP 401)，请检查语音凭证配置", ErrAuth)
			}
			return false, acc.lastFinishedRound, fmt.Errorf("连接火山播客 WebSocket 失败(HTTP %d): %w", httpResp.StatusCode, err)
		}
		return false, acc.lastFinishedRound, fmt.Errorf("连接火山播客 WebSocket 失败: %w", err)
	}
	defer conn.Close()

	// ctx 取消联动：ctx 结束时关闭连接解除读阻塞；ctxClosed 防止 goroutine 泄漏（同 asr_client）。
	ctxClosed := make(chan struct{})
	defer close(ctxClosed)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-ctxClosed:
		}
	}()

	if err := writePodFrame(conn, BuildStartSession(sessionID, payload)); err != nil {
		return false, acc.lastFinishedRound, fmt.Errorf("发送播客 StartSession 失败: %w", podCtxErr(ctx, err))
	}

	for {
		frame, err := readPodFrame(conn)
		if err != nil {
			return false, acc.lastFinishedRound, fmt.Errorf("读取播客响应失败: %w", podCtxErr(ctx, err))
		}
		if frame.ErrCode != 0 {
			return false, acc.lastFinishedRound, &podFatalError{fmt.Errorf("火山播客服务返回错误(code %d): %s", frame.ErrCode, frame.ErrMsg)}
		}
		switch frame.Event {
		case EventSessionStarted: // 150 会话建立，无业务数据
		case EventRoundStart: // 360 轮次文本
			round, err := parsePodRoundMeta(frame.Payload)
			if err != nil {
				return false, acc.lastFinishedRound, fmt.Errorf("解析播客轮次事件失败: %w", err)
			}
			// 断点续传中断在轮次中间时服务端会重发未完成轮次（retry_info 只记已完成轮次），
			// 此时替换而非追加，避免对话稿出现重复轮次；并先截断断线前已收的该轮不完整分片，
			// 否则重发分片与其重复拼接，最终音频 = 部分 + 完整。
			if n := len(acc.rounds); n > 0 && acc.rounds[n-1].RoundID == round.RoundID && acc.rounds[n-1].DurationS == 0 {
				acc.rounds[n-1] = round
				acc.audio.Truncate(acc.roundStartAudioLen)
			} else {
				acc.rounds = append(acc.rounds, round)
				acc.roundStartAudioLen = acc.audio.Len()
			}
			if onRound != nil {
				onRound(round)
			}
		case EventRoundResponse: // 361 音频分片
			acc.audio.Write(frame.Audio)
		case EventRoundEnd: // 362 轮次结束：正常形态为时长秒，is_error 变体为该轮生成失败
			var end podRoundEndPayload
			if err := json.Unmarshal(frame.Payload, &end); err != nil {
				return false, acc.lastFinishedRound, fmt.Errorf("解析播客轮次结束事件失败: %w", err)
			}
			if end.IsError { // 轮次错误（如内容审核拦截）重连同参数注定失败：立即终止不计入完成轮次
				return false, acc.lastFinishedRound, &podFatalError{fmt.Errorf("播客轮次生成失败: %s", end.ErrorMsg)}
			}
			if n := len(acc.rounds); n > 0 {
				acc.rounds[n-1].DurationS = end.AudioDuration
				if id := acc.rounds[n-1].RoundID; id >= 0 { // round_id=-1 的开头音乐不计入续传断点
					acc.lastFinishedRound = id
				}
			}
		case EventUsageResponse: // 154 用量（可能多条，按 key 累计）
			var u podUsagePayload
			if err := json.Unmarshal(frame.Payload, &u); err != nil {
				return false, acc.lastFinishedRound, fmt.Errorf("解析播客用量事件失败: %w", err)
			}
			if acc.usage == nil {
				acc.usage = map[string]int64{}
			}
			for k, v := range u.Usage {
				acc.usage[k] += v
			}
		case EventPodcastEnd: // 363 播客结束（audio_url 兜底下载用）
			var end podEndPayload
			if err := json.Unmarshal(frame.Payload, &end); err != nil {
				return false, acc.lastFinishedRound, fmt.Errorf("解析播客结束事件失败: %w", err)
			}
			acc.audioURL = end.MetaInfo.AudioURL
		case EventSessionFinished: // 152 合成完成 → 回 FinishConnection，等 52 确认
			if err := writePodFrame(conn, BuildFinishConnection(sessionID)); err != nil {
				return false, acc.lastFinishedRound, fmt.Errorf("发送播客 FinishConnection 失败: %w", podCtxErr(ctx, err))
			}
			for {
				ack, err := readPodFrame(conn)
				if err != nil {
					return false, acc.lastFinishedRound, fmt.Errorf("等待播客连接关闭确认失败: %w", podCtxErr(ctx, err))
				}
				if ack.ErrCode != 0 {
					return false, acc.lastFinishedRound, &podFatalError{fmt.Errorf("火山播客服务返回错误(code %d): %s", ack.ErrCode, ack.ErrMsg)}
				}
				if ack.Event == EventConnectionFinished {
					return true, acc.lastFinishedRound, nil
				}
				// 152 与 52 之间可能仍有迟到帧（154/363 等），忽略继续等确认。
			}
		default:
			// 未知 event 忽略，保持对协议扩展的前向兼容。
		}
	}
}

// podStartPayload 上行 StartSession payload（action 分派 + audio_config + speaker_info，retry_info 仅续传时携带）。
type podStartPayload struct {
	InputText    string         `json:"input_text,omitempty"` // action=0 文本模式；URL/Script 模式不携带
	Action       int            `json:"action"`
	UseHeadMusic bool           `json:"use_head_music"`
	AudioConfig  podAudioConfig `json:"audio_config"`
	SpeakerInfo  podSpeakerInfo `json:"speaker_info"`
	InputInfo    *podInputInfo  `json:"input_info,omitempty"` // 网页模式
	NlpTexts     []podNlpText   `json:"nlp_texts,omitempty"`  // 对话稿模式（action=3）
	RetryInfo    *podRetryInfo  `json:"retry_info,omitempty"` // 仅断线续传时携带
}

type podAudioConfig struct {
	Format     string  `json:"format"`
	SampleRate int     `json:"sample_rate"`
	SpeechRate float64 `json:"speech_rate"`
}

type podSpeakerInfo struct {
	RandomOrder bool     `json:"random_order"`
	Speakers    []string `json:"speakers"`
}

type podInputInfo struct {
	InputURL string `json:"input_url"`
}

type podNlpText struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// podRetryInfo 断点续传信息：retry_task_id 即本次生成的 X-Api-Request-Id。
type podRetryInfo struct {
	RetryTaskID         string `json:"retry_task_id"`
	LastFinishedRoundID int    `json:"last_finished_round_id"`
}

// buildPodStartPayload 构造 StartSession payload，按输入类型分派 action：
// Script 非空 → 3（nlp_texts）；URL 非空 → 0（input_info）；否则 0 + input_text。
func buildPodStartPayload(req PodcastRequest, retry *podRetryInfo) ([]byte, error) {
	if err := validatePodSpeakers(req.Speakers); err != nil {
		return nil, err
	}
	p := podStartPayload{
		Action:       podActionText,
		UseHeadMusic: req.HeadMusic,
		AudioConfig:  podAudioConfig{Format: podAudioFormat(req.Format), SampleRate: podSampleRate},
		SpeakerInfo:  podSpeakerInfo{Speakers: []string{req.Speakers[0], req.Speakers[1]}},
		RetryInfo:    retry,
	}
	switch {
	case len(req.Script) > 0:
		p.Action = podActionScript
		p.NlpTexts = make([]podNlpText, 0, len(req.Script))
		for i, turn := range req.Script {
			if turn.Speaker == "" || turn.Text == "" {
				return nil, fmt.Errorf("对话稿第 %d 轮缺少 speaker 或 text", i+1)
			}
			p.NlpTexts = append(p.NlpTexts, podNlpText{Speaker: turn.Speaker, Text: turn.Text})
		}
	case req.URL != "":
		p.InputInfo = &podInputInfo{InputURL: req.URL}
	default:
		p.InputText = req.InputText
	}
	return json.Marshal(p)
}

// validatePodSpeakers 播客必须恰好两个非空音色（顺序即 A/B 说话人）。
func validatePodSpeakers(sp [2]string) error {
	if sp[0] == "" || sp[1] == "" {
		return fmt.Errorf("speakers 需要恰好 2 个非空音色 ID（顺序即 A/B 说话人）")
	}
	return nil
}

// podAudioFormat 空格式回退 mp3（枚举 mp3|ogg_opus|pcm|aac）。
func podAudioFormat(f string) string {
	if f == "" {
		return podDefaultFormat
	}
	return f
}

// parsePodRoundMeta 解析 360 事件 payload：{"text_type","speaker","round_id","text"}。
func parsePodRoundMeta(data []byte) (PodcastRound, error) {
	var m struct {
		RoundID int    `json:"round_id"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return PodcastRound{}, err
	}
	return PodcastRound{RoundID: m.RoundID, Speaker: m.Speaker, Text: m.Text}, nil
}

// podRoundEndPayload 362 事件 payload（两种形态：正常为时长/起止秒；
// is_error=true 时携带 error_msg 表示该轮生成失败，如内容审核拦截）。
type podRoundEndPayload struct {
	AudioDuration float64 `json:"audio_duration"`
	EndTime       float64 `json:"end_time"`
	StartTime     float64 `json:"start_time"`
	IsError       bool    `json:"is_error"`
	ErrorMsg      string  `json:"error_msg"`
}

// podUsagePayload 154 事件 payload。
type podUsagePayload struct {
	Usage map[string]int64 `json:"usage"`
}

// podEndPayload 363 事件 payload（audio_url 1 小时有效）。
type podEndPayload struct {
	MetaInfo struct {
		AudioURL string `json:"audio_url"`
	} `json:"meta_info"`
}

func writePodFrame(conn *websocket.Conn, frame []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(podWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

// readPodFrame 读一帧（读超时 podReadTimeout）并解析。
func readPodFrame(conn *websocket.Conn) (PodFrame, error) {
	if err := conn.SetReadDeadline(time.Now().Add(podReadTimeout)); err != nil {
		return PodFrame{}, err
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return PodFrame{}, err
	}
	return ParsePodFrame(msg)
}

// podCtxErr ctx 已取消时优先返回取消错误（连接可能已被联动关闭），否则返回原错误（同 asr_client）。
func podCtxErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("播客生成已取消: %w", ctxErr)
	}
	return err
}
