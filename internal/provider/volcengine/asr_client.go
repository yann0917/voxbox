package volcengine

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

const (
	// asrWSBaseURL 生产环境 ASR WebSocket 服务地址。
	asrWSBaseURL = "wss://openspeech.bytedance.com"
	// asrNostreamPath 大模型录音文件识别（非流式）WebSocket 路径。
	asrNostreamPath = "/api/v3/sauc/bigmodel_nostream"
	// asrResourceID 流式语音识别 2.0 资源 ID（时长计费；SpeechCred 无此字段，YAGNI 不做可选项）。
	asrResourceID = "volc.seedasr.sauc.duration"
	// asrReadTimeout 每次读帧前设置的超时。服务端在收完音频到返回最终结果之间静默，
	// 时长与音频长度正相关（nostream 单次返回），须显著大于 500MB 上限对应的处理时间。
	asrReadTimeout = 10 * time.Minute
	// asrWriteTimeout 每次写帧前设置的超时。
	asrWriteTimeout = 30 * time.Second
	// asrWavChunkIntervalMS wav 音频分片时长（对照官方 demo，按 200ms 音频量分片但不做实时节流）。
	asrWavChunkIntervalMS = 200
	// asrDefaultChunkSize 非 wav 音频固定 32KB 每片。
	asrDefaultChunkSize = 32 * 1024
)

// ASRNostreamReq 录音文件识别请求。
type ASRNostreamReq struct {
	Audio    []byte // 完整音频（mp3/wav/ogg/pcm 原样直发）
	Format   string // mp3|wav|ogg|pcm（未知扩展名由 Tool 层拦截；wav 原样发）
	Language string // 默认 zh-CN，空则不传
	Hotwords string // 可选，直传（Request.Corpus.Context，官方结构 jsonstring）
}

// ASRSegment 分句时间戳。json tag 与前端/CLI 契约一致（summary.segments 小写下划线）：
// 无 tag 时序列化为 Go 字段名（Text/StartMS），曾导致 ASR 页分句文本渲染为空。
type ASRSegment struct {
	Text    string `json:"text"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// ASRNostreamResp 识别结果。
type ASRNostreamResp struct {
	Text       string
	DurationMS int64
	Segments   []ASRSegment
}

// ASRClient 火山引擎大模型录音文件识别（sauc bigmodel nostream）WebSocket 客户端。
// 鉴权用 SpeechCred（新版 X-Api-Key 或老版 AppID+AccessToken），资源 ID 固定 asrResourceID。
type ASRClient struct {
	cred      SpeechCred
	wsBaseURL string
}

func NewASRClient(cred SpeechCred) *ASRClient {
	return NewASRClientWithURL(cred, asrWSBaseURL)
}

// NewASRClientWithURL 供测试注入 mock 地址（如 ws://127.0.0.1:port），生产用 NewASRClient。
func NewASRClientWithURL(cred SpeechCred, wsBaseURL string) *ASRClient {
	return &ASRClient{cred: cred, wsBaseURL: strings.TrimRight(wsBaseURL, "/")}
}

// Recognize 同步识别完整音频：
//  1. Dial（鉴权头 + X-Api-Connect-Id）→ 发 full client request → 同步读一帧 ack；
//  2. 全速分片发送音频（不做实时 ticker）：wav 按 ReadWavInfo 算每 200ms 字节数，非 wav 固定 32KB，
//     seq 从 2 递增，最后一包 seq 取负；音频原样直发（wav 含头，与官方 demo 一致）；
//  3. 接收循环直到 IsLastPackage，Code != 0 或 PayloadMsg.Error 非空报中文错误（含 code）。
func (c *ASRClient) Recognize(ctx context.Context, req ASRNostreamReq) (ASRNostreamResp, error) {
	audio, chunkSize := c.prepareAudio(req)
	if len(audio) == 0 {
		return ASRNostreamResp{}, fmt.Errorf("音频数据为空，无法识别")
	}

	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, asrResourceID)
	headers.Set("X-Api-Connect-Id", uuid.NewString())

	conn, httpResp, err := websocket.DefaultDialer.DialContext(ctx, c.wsBaseURL+asrNostreamPath, headers)
	if err != nil {
		if httpResp != nil {
			defer httpResp.Body.Close()
			if httpResp.StatusCode == http.StatusUnauthorized {
				return ASRNostreamResp{}, fmt.Errorf("%w: 火山 ASR 鉴权失败(HTTP 401)，请检查语音凭证配置", ErrAuth)
			}
			if httpResp.StatusCode == http.StatusForbidden {
				return ASRNostreamResp{}, fmt.Errorf("连接火山 ASR WebSocket 失败(HTTP 403): %w，可能未开通流式语音识别2.0服务", err)
			}
			return ASRNostreamResp{}, fmt.Errorf("连接火山 ASR WebSocket 失败(HTTP %d): %w", httpResp.StatusCode, err)
		}
		return ASRNostreamResp{}, fmt.Errorf("连接火山 ASR WebSocket 失败: %w", err)
	}
	defer conn.Close()

	// ctx 取消联动：ctx 结束时关闭连接，解除读写阻塞；ctxClosed 防止 goroutine 泄漏。
	ctxClosed := make(chan struct{})
	defer close(ctxClosed)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-ctxClosed:
		}
	}()

	// 1. full client request。
	payload := sauc.AsrRequestPayload{
		User: sauc.UserMeta{Uid: "voxbox"},
		Audio: sauc.AudioMeta{
			Format:   req.Format,
			Codec:    "raw",
			Rate:     sauc.DefaultSampleRate,
			Bits:     16,
			Channel:  1,
			Language: req.Language,
		},
		Request: sauc.RequestMeta{ModelName: "bigmodel", EnableITN: true, ShowUtterances: true},
	}
	if req.Hotwords != "" {
		payload.Request.Corpus.Context = req.Hotwords
	}
	if err := writeASRFrame(conn, sauc.NewFullClientRequest(payload)); err != nil {
		return ASRNostreamResp{}, fmt.Errorf("发送 ASR 请求失败: %w", asrCtxErr(ctx, err))
	}

	// 2. 同步读一帧 ack。
	msg, err := readASRFrame(conn)
	if err != nil {
		return ASRNostreamResp{}, fmt.Errorf("等待 ASR 确认帧失败: %w", asrCtxErr(ctx, err))
	}
	if err := checkASRResponse(sauc.ParseResponse(msg)); err != nil {
		return ASRNostreamResp{}, err
	}

	// 3. 全速分片发送音频，最后一包 seq 取负。
	for offset, seq := 0, 2; offset < len(audio); offset += chunkSize {
		end := offset + chunkSize
		if end > len(audio) {
			end = len(audio)
		}
		select {
		case <-ctx.Done():
			return ASRNostreamResp{}, fmt.Errorf("ASR 识别已取消: %w", ctx.Err())
		default:
		}
		s := seq
		if end == len(audio) {
			s = -s
		}
		if err := writeASRFrame(conn, sauc.NewAudioOnlyRequest(s, audio[offset:end])); err != nil {
			return ASRNostreamResp{}, fmt.Errorf("发送 ASR 音频分片失败(seq %d): %w", s, asrCtxErr(ctx, err))
		}
		seq++
	}

	// 4. 接收循环，直到 IsLastPackage。
	for {
		msg, err := readASRFrame(conn)
		if err != nil {
			return ASRNostreamResp{}, fmt.Errorf("读取火山 ASR 响应失败: %w", asrCtxErr(ctx, err))
		}
		resp := sauc.ParseResponse(msg)
		if err := checkASRResponse(resp); err != nil {
			return ASRNostreamResp{}, err
		}
		if !resp.IsLastPackage {
			continue // 中间结果帧，继续等最终包
		}
		if resp.PayloadMsg == nil {
			return ASRNostreamResp{}, fmt.Errorf("火山 ASR 最终响应缺少识别结果")
		}
		out := ASRNostreamResp{
			Text:       resp.PayloadMsg.Result.Text,
			DurationMS: int64(resp.PayloadMsg.AudioInfo.Duration),
		}
		for _, u := range resp.PayloadMsg.Result.Utterances {
			out.Segments = append(out.Segments, ASRSegment{
				Text:    u.Text,
				StartMS: int64(u.StartTime),
				EndMS:   int64(u.EndTime),
			})
		}
		return out, nil
	}
}

// prepareAudio 返回待发送音频与分片大小：wav 按 ReadWavInfo 算每 200ms 字节数（声道×位宽×采样率×200/1000），
// 非 wav 固定 32KB；音频一律原样直发（含 wav 头，与官方 demo 一致）。
// WAV 头解析失败（非标准块布局等）时回退 32KB 分片，不阻断识别（服务端按 format=wav 自行解封装）。
func (c *ASRClient) prepareAudio(req ASRNostreamReq) ([]byte, int) {
	chunkSize := asrDefaultChunkSize
	if req.Format == "wav" {
		nchannels, sampwidth, framerate, _, _, err := sauc.ReadWavInfo(req.Audio)
		if err == nil {
			if size := nchannels * sampwidth * framerate * asrWavChunkIntervalMS / 1000; size > 0 {
				chunkSize = size
			}
		}
	}
	return req.Audio, chunkSize
}

// checkASRResponse 校验服务端帧：Code != 0 或 PayloadMsg.Error 非空返回中文错误（含 code）。
func checkASRResponse(resp *sauc.AsrResponse) error {
	if resp == nil {
		return fmt.Errorf("火山 ASR 响应解析失败")
	}
	if resp.Code != 0 {
		detail := ""
		if resp.PayloadMsg != nil && resp.PayloadMsg.Error != "" {
			detail = ": " + resp.PayloadMsg.Error
		}
		return fmt.Errorf("火山 ASR 服务返回错误(code %d)%s", resp.Code, detail)
	}
	if resp.PayloadMsg != nil && resp.PayloadMsg.Error != "" {
		return fmt.Errorf("火山 ASR 识别失败: %s", resp.PayloadMsg.Error)
	}
	return nil
}

// asrCtxErr ctx 已取消时优先返回取消错误（连接可能已被联动关闭），否则返回原错误。
func asrCtxErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("ASR 识别已取消: %w", ctxErr)
	}
	return err
}

func writeASRFrame(conn *websocket.Conn, frame []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(asrWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func readASRFrame(conn *websocket.Conn) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(asrReadTimeout)); err != nil {
		return nil, err
	}
	_, msg, err := conn.ReadMessage()
	return msg, err
}
