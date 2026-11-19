package deepseek

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yann0917/voxbox/internal/provider"
)

// PathTTS 语音合成端点（HTTP 整段通道）。官方另有 WebSocket 流式通道（wss 同路径，
// 增量喂文本），voxbox 合成任务以完整文本提交、无增量喂入场景，不接流式通道。
const PathTTS = "/v1/tools/tts"

// ttsHTTPClient 合成专用客户端：音频边合成边流式返回，耗时随音频时长增长（单次
// 最多返回 30 分钟音频），共用 doJSON 的 60s 短超时会掐断长任务。
var ttsHTTPClient = &http.Client{Timeout: 15 * time.Minute}

// ttsRequest 合成请求 wire 结构（官方 guides/tts，2026-11-19 校准）：text 必填
// （≤10,000 计费字符，请求体 ≤64KiB）；voice_id 缺省 stella；format 缺省 wav；
// filter_markdown 缺省 true（朗读前去掉 Markdown 格式，符合朗读场景，不透传）。
// 官方不支持语速调节与中断续传；语种无参数，按文本自动识别。
type ttsRequest struct {
	Text    string `json:"text"`
	VoiceID string `json:"voice_id,omitempty"`
	Format  string `json:"format,omitempty"`
}

// Synthesize 合成并返回完整 WAV 字节：响应体为 audio/wav 流（边合成边返回）。
// voice 传空回落官方默认 stella。官方产物为流式 WAV（RIFF/data 长度写 0xFFFFFFFF
// 占位），读取成功后统一修正为实际长度再交付。
func (c *Client) Synthesize(ctx context.Context, text, voice string) ([]byte, error) {
	payload, err := json.Marshal(ttsRequest{Text: text, VoiceID: voice, Format: "wav"})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+PathTTS, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ttsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 DeepSeek 合成服务失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, decodeError(body, resp.StatusCode)
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		// 官方口径：音频开始返回后合成失败时响应中途断开，提前结束视为失败
		return nil, fmt.Errorf("音频响应读取中断（合成未完成）: %w", err)
	}
	if err := checkWAVComplete(audio); err != nil {
		return nil, err
	}
	return fixStreamingWAV(audio), nil
}

// wavLenPlaceholder 流式 WAV 的长度占位：边合成边返回时头部无法预知总长，
// 官方把 RIFF size 与 data size 都写 0xFFFFFFFF（真机核实，2026-11-19）。
const wavLenPlaceholder = 0xFFFFFFFF

// checkWAVComplete 按头部声明长度校验响应完整性：流式通道合成失败时 HTTP 仍是 200
// 但响应被截断，靠 RIFF 声明尺寸与实际长度比对兜底识别；声明为流式占位时长度未知，
// 以读取无错为准。
func checkWAVComplete(b []byte) error {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return fmt.Errorf("DeepSeek TTS 响应不是有效 WAV（可能合成失败），请重试")
	}
	declared := binary.LittleEndian.Uint32(b[4:8])
	if declared == wavLenPlaceholder {
		return nil
	}
	if len(b) < int(declared)+8 {
		return fmt.Errorf("DeepSeek TTS 音频响应不完整（声明 %d 字节，实际 %d）：合成中途失败，请重试", declared+8, len(b))
	}
	return nil
}

// fixStreamingWAV 把流式占位长度（0xFFFFFFFF）重写为实际值：data 块起点由解析结果
// 回算（总长 - data 载荷），RIFF size = 总长 - 8。无法解析或布局异常时原样返回
// （主流播放器对占位长度也有容忍，修正只为产出规范 WAV）。
func fixStreamingWAV(b []byte) []byte {
	w, err := provider.ParseWAV(b)
	if err != nil || w.FormatTag != 1 {
		return b
	}
	dataOff := len(b) - w.DataLen()
	if dataOff < 44 || w.DataLen() == 0 {
		return b
	}
	out := append([]byte(nil), b...)
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(b)-8))
	binary.LittleEndian.PutUint32(out[dataOff-4:dataOff], uint32(w.DataLen()))
	return out
}
