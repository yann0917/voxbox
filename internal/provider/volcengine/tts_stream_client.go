package volcengine

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/yann0917/voxbox/internal/provider/volcengine/sauc"
)

const (
	// ttsStreamPath 单向流式合成路径（官方文档 6561/2528925）。
	ttsStreamPath = "/api/v3/tts/unidirectional"
	// ttsStreamChunkOK 中间音频分片的响应码（data 携带 base64 音频块）。
	ttsStreamChunkOK = 0
	// ttsStreamCodeOK 终止行响应码：流正常结束（与 chunk 码 0 语义不同，勿混用）。
	ttsStreamCodeOK = 20000000
)

// TTSStreamSubmitReq 流式合成请求参数（Tool 层完成校验与默认值）。
type TTSStreamSubmitReq struct {
	Text             string
	Speaker          string
	Resource         string // seed-tts-2.0 | seed-icl-2.0
	Model            string // 复刻音色时透传；指定后不支持 context_texts
	Format           string // mp3|pcm|ogg_opus|wav
	SampleRate       int
	BitRate          int // 0 不传；wav/pcm 不支持（Tool 层拦截）
	SpeechRate       int
	LoudnessRate     int
	EnableSubtitle   bool // 字级时间戳（仅中英语种返回）
	ExplicitLanguage string
	ExplicitDialect  string
	Pitch            int
	SilenceDuration  int // 文本末尾静音 ms，0 不传
	ContextText      string
	ToneFidelity     bool
	AIGCWatermark    bool
}

// TTSStreamWord 字级时间戳（秒 → 毫秒归一）。
type TTSStreamWord struct {
	Word    string
	StartMS int64
	EndMS   int64
}

// TTSStreamResult 流式合成结果：拼接后的音频 + 字级时间戳 + 用量。
type TTSStreamResult struct {
	Audio       []byte
	Words       []TTSStreamWord
	Sentence    string
	BilledWords int // usage.text_words：计费字符数（含标点）
	Chunks      int
}

// TTSStreamClient 火山引擎单向流式语音合成（HTTP Chunked）客户端。
// 响应为按行分隔的 JSON 流：中间行 code=0（data 为 base64 音频块）、
// 终止行 code=20000000、其余 code>0 为错误。鉴权同 tts_long（SpeechCred 双轨）。
type TTSStreamClient struct {
	resty *resty.Client
	cred  SpeechCred
}

func NewTTSStreamClient(cred SpeechCred) *TTSStreamClient {
	return NewTTSStreamClientWithBaseURL(cred, ttsBaseURL)
}

// NewTTSStreamClientWithBaseURL 供测试注入 mock 地址。
// 超时 5 分钟：流式合成整体时长随文本量增长，需大于官方 60s 默认的量级。
func NewTTSStreamClientWithBaseURL(cred SpeechCred, baseURL string) *TTSStreamClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(5 * time.Minute)
	return &TTSStreamClient{resty: r, cred: cred}
}

// ttsStreamLine 流式响应行：中间分片 code=0 携带 data；终态/错误行可能携带
// sentence（字级时间戳）与 usage（计费统计）。
type ttsStreamLine struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	Data     string `json:"data"`
	Sentence struct {
		Text  string          `json:"text"`
		Words []ttsStreamWord `json:"words"`
	} `json:"sentence"`
	Usage struct {
		TextWords int `json:"text_words"`
	} `json:"usage"`
}

// ttsStreamWord 协议字级时间戳：秒（float64）。
type ttsStreamWord struct {
	Word      string  `json:"word"`
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
}

// SynthesizeStream 发起流式合成：onChunk 在每个音频分片到达时回调（累计分片数与字节数，
// 可为 nil）。任一行报错、行解析失败或连接中断均返回错误；未收到终止行即断流视为异常。
func (c *TTSStreamClient) SynthesizeStream(ctx context.Context, req TTSStreamSubmitReq, onChunk func(chunks, bytes int)) (TTSStreamResult, error) {
	headers := sauc.NewAuthHeaderFrom(c.cred.APIKey, c.cred.AppID, c.cred.AccessToken, req.Resource)

	body := map[string]any{"req_params": ttsStreamReqParams(req)}
	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(aucHeaderMap(headers)).
		SetHeader("Connection", "keep-alive").
		SetHeader("X-Control-Require-Usage-Tokens-Return", "*").
		SetDoNotParseResponse(true).
		SetBody(body).
		Post(ttsStreamPath)
	if err != nil {
		return TTSStreamResult{}, fmt.Errorf("请求火山流式合成失败: %w", err)
	}
	defer httpResp.RawBody().Close()
	if httpResp.StatusCode() != http.StatusOK {
		return TTSStreamResult{}, fmt.Errorf("请求火山流式合成失败(HTTP %d): %s%s",
			httpResp.StatusCode(), streamErrorExcerpt(httpResp), ttsLogidSuffix(httpResp))
	}

	var out TTSStreamResult
	scanner := bufio.NewScanner(httpResp.RawBody())
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 官方 demo 同款上限
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return TTSStreamResult{}, fmt.Errorf("流式合成已取消: %w", err)
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg ttsStreamLine
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return TTSStreamResult{}, fmt.Errorf("解析流式响应行失败: %w", err)
		}
		switch {
		case msg.Code == ttsStreamChunkOK:
			// 中间分片：base64 音频块；字级时间戳可能随分片增量下发
			if msg.Data != "" {
				chunk, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					return TTSStreamResult{}, fmt.Errorf("音频分片解码失败: %w", err)
				}
				out.Audio = append(out.Audio, chunk...)
				out.Chunks++
				if onChunk != nil {
					onChunk(out.Chunks, len(out.Audio))
				}
			}
			out.Sentence = nonEmpty(msg.Sentence.Text, out.Sentence)
			out.Words = append(out.Words, streamWordsToMS(msg.Sentence.Words)...)
			if msg.Usage.TextWords > 0 {
				out.BilledWords = msg.Usage.TextWords
			}
		case msg.Code == ttsStreamCodeOK:
			// 终止行：流正常结束
			out.Sentence = nonEmpty(msg.Sentence.Text, out.Sentence)
			out.Words = append(out.Words, streamWordsToMS(msg.Sentence.Words)...)
			if msg.Usage.TextWords > 0 {
				out.BilledWords = msg.Usage.TextWords
			}
			return out, nil
		default:
			return TTSStreamResult{}, fmt.Errorf("火山流式合成失败(%d): %s%s",
				msg.Code, msg.Message, ttsLogidSuffix(httpResp))
		}
	}
	if err := scanner.Err(); err != nil {
		return TTSStreamResult{}, fmt.Errorf("读取流式响应失败: %w", err)
	}
	// 流中断且未收到终止行：已拼出的音频不完整，不能当成功产物
	return TTSStreamResult{}, fmt.Errorf("流式连接中断，未收到终止帧（已收 %d 分片）", out.Chunks)
}

// ttsStreamReqParams 组装 req_params（omitempty 保证可选字段缺省不下发）。
func ttsStreamReqParams(req TTSStreamSubmitReq) map[string]any {
	params := map[string]any{
		"text":    req.Text,
		"speaker": req.Speaker,
		"audio_params": map[string]any{
			"format":          req.Format,
			"sample_rate":     req.SampleRate,
			"bit_rate":        req.BitRate,
			"speech_rate":     req.SpeechRate,
			"loudness_rate":   req.LoudnessRate,
			"enable_subtitle": req.EnableSubtitle,
		},
	}
	if req.Model != "" {
		params["model"] = req.Model
	}
	if req.ExplicitLanguage != "" {
		params["explicit_language"] = req.ExplicitLanguage
	}
	if req.ExplicitDialect != "" {
		params["explicit_dialect"] = req.ExplicitDialect
	}
	if req.SilenceDuration > 0 {
		params["silence_duration"] = req.SilenceDuration
	}
	if req.ContextText != "" {
		params["context_texts"] = []string{req.ContextText}
	}
	if req.Pitch != 0 {
		params["post_process"] = map[string]any{"pitch": req.Pitch}
	}
	if req.ToneFidelity {
		params["tone_fidelity"] = true
	}
	if req.AIGCWatermark {
		params["aigc_watermark"] = true
	}
	// 零值可选键不转发（bit_rate/speech_rate 等与服务端默认一致，省略更干净）
	trimZeroKeys(params["audio_params"].(map[string]any))
	return params
}

// trimZeroKeys 删除 map 中值为 0/false 的键（enable_subtitle=false 与默认一致）。
func trimZeroKeys(m map[string]any) {
	for k, v := range m {
		switch x := v.(type) {
		case int:
			if x == 0 {
				delete(m, k)
			}
		case bool:
			if !x {
				delete(m, k)
			}
		}
	}
}

// streamWordsToMS 字级时间戳秒 → 毫秒归一。
func streamWordsToMS(words []ttsStreamWord) []TTSStreamWord {
	if len(words) == 0 {
		return nil
	}
	out := make([]TTSStreamWord, 0, len(words))
	for _, w := range words {
		out = append(out, TTSStreamWord{
			Word:    w.Word,
			StartMS: secToMS(w.StartTime),
			EndMS:   secToMS(w.EndTime),
		})
	}
	return out
}

// nonEmpty 返回第一个非空字符串（新值优先）。
func nonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// streamErrorExcerpt 截取错误响应体前 200 字节（防超长刷屏）。
func streamErrorExcerpt(resp *resty.Response) string {
	body := resp.Body()
	if len(body) == 0 {
		// DoNotParseResponse 下 Body() 为空，从 RawBody 读一小段
		raw := resp.RawBody()
		if raw != nil {
			buf := make([]byte, 200)
			n, _ := raw.Read(buf)
			body = buf[:n]
		}
	}
	return aucBodyExcerpt(body)
}
