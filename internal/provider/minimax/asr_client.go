package minimax

import (
	"context"
	"fmt"
	"strings"
)

// ASRModel 识别模型（官方当前唯一枚举）。
const ASRModel = "asr-1.0"

// ASRResp 语音识别响应（response_format=verbose_json）：text 为全文（各段按时间序拼接），
// duration 为音频时长（秒，计费口径），segments 携带句级时间戳与说话人标识；
// response_format=json 时仅 text + duration。
type ASRResp struct {
	Text      string  `json:"text"`
	Duration  float64 `json:"duration"`
	NSpeakers int     `json:"n_speakers"`
	Segments  []struct {
		ID      int     `json:"id"`
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Speaker string  `json:"speaker"`
		Text    string  `json:"text"`
	} `json:"segments"`
	TraceID string `json:"trace_id"`
}

// Transcribe 转写本地音频文件：multipart 上传（file 字段），language 为 BCP-47 语种提示
// （官方走请求头而非表单字段，空 = 混合语言识别）。固定 verbose_json + 句级时间戳——
// 说话人分离与时间戳是 MiniMax 识别相对纯文本转写的核心增益，SRT 产物依赖于此。
// 官方单次限制 ≤500 秒/50MB（且不支持裸 PCM），超限由工具层自动分段转写后归一。
func (c *ASRClient) Transcribe(ctx context.Context, audioPath, language string) (ASRResp, error) {
	fields := map[string]string{
		"model":           ASRModel,
		"response_format": "verbose_json",
		"timestamp_level": "sentence",
	}
	headers := map[string]string{}
	if strings.TrimSpace(language) != "" {
		headers["language"] = strings.TrimSpace(language)
	}
	var resp ASRResp
	if err := doMultipart(ctx, c.baseURL+pathASR, c.apiKey, fields, headers, "file", audioPath, &resp); err != nil {
		return ASRResp{}, err
	}
	if strings.TrimSpace(resp.Text) == "" && len(resp.Segments) == 0 {
		return ASRResp{}, fmt.Errorf("MiniMax ASR 响应中未找到转写文本")
	}
	return resp, nil
}

// ASRClient MiniMax 语音识别客户端。
type ASRClient struct{ apiKey, baseURL string }

func NewASRClient(apiKey, baseURL string) *ASRClient {
	return &ASRClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}
