package minimax

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// TTSLongClient MiniMax 异步长文本 TTS 客户端。
type TTSLongClient struct{ apiKey, baseURL string }

func NewTTSLongClient(apiKey, baseURL string) *TTSLongClient {
	return &TTSLongClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// TTSLongCreateReq 异步长文本合成创建参数。
type TTSLongCreateReq struct {
	Text          string  // ≤50000 字符
	Voice         string  // 音色 ID
	Model         string  // speech-2.8-hd | speech-2.8-turbo，空回落 hd
	Speed         float64 // [0.5,2]，0 = 上游默认
	Volume        float64 // (0,10]，0 = 上游默认
	Pitch         int     // [-12,12]，0 = 上游默认
	Emotion       string  // 空 = 模型自动匹配
	LanguageBoost string  // 空 = 不传
	Format        string  // mp3|wav|flac，空 = mp3
	SampleRate    int     // 0 = 上游默认 32000
}

// t2aAsyncCreateResp 创建响应：task_id 供轮询，file_id 供完成后下载。
type t2aAsyncCreateResp struct {
	TaskID          string   `json:"task_id"`
	FileID          int64    `json:"file_id"`
	UsageCharacters int64    `json:"usage_characters"`
	BaseResp        baseResp `json:"base_resp"`
}

// t2aAsyncQueryResp 查询响应：status 大小写官方示例与枚举不一致，按小写归一处理。
type t2aAsyncQueryResp struct {
	TaskID   int64    `json:"task_id"`
	Status   string   `json:"status"`
	FileID   int64    `json:"file_id"`
	BaseResp baseResp `json:"base_resp"`
}

// Create 创建异步合成任务，返回 task_id。
func (c *TTSLongClient) Create(ctx context.Context, req TTSLongCreateReq) (string, error) {
	if strings.TrimSpace(req.Voice) == "" {
		return "", fmt.Errorf("MiniMax 长文本合成缺少必填参数: voice")
	}
	model := req.Model
	if model == "" {
		model = ModelTTSHD
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}
	voiceSetting := map[string]any{"voice_id": req.Voice}
	if req.Speed > 0 {
		voiceSetting["speed"] = req.Speed
	}
	if req.Volume > 0 {
		voiceSetting["vol"] = req.Volume
	}
	if req.Pitch != 0 {
		voiceSetting["pitch"] = req.Pitch
	}
	if req.Emotion != "" {
		voiceSetting["emotion"] = req.Emotion
	}
	// 异步接口的采样率键为 audio_sample_rate（同步接口是 sample_rate），显式传默认值
	sampleRate := req.SampleRate
	if sampleRate <= 0 {
		sampleRate = 32000
	}
	audioSetting := map[string]any{"format": format, "channel": 1, "audio_sample_rate": sampleRate}
	body := map[string]any{
		"model":         model,
		"text":          req.Text,
		"voice_setting": voiceSetting,
		"audio_setting": audioSetting,
	}
	if req.LanguageBoost != "" {
		body["language_boost"] = req.LanguageBoost
	}
	var resp t2aAsyncCreateResp
	if err := doJSON(ctx, pollClient, "POST", c.baseURL+pathT2AAsync, c.apiKey, body, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.TaskID) == "" {
		return "", fmt.Errorf("MiniMax 长文本合成响应中未返回任务 ID")
	}
	return resp.TaskID, nil
}

// Query 查询任务状态：processing / success / failed / expired（大小写归一为小写）。
func (c *TTSLongClient) Query(ctx context.Context, taskID string) (t2aAsyncQueryResp, error) {
	q := url.Values{"task_id": []string{taskID}}
	var resp t2aAsyncQueryResp
	if err := doJSON(ctx, pollClient, "GET", c.baseURL+pathT2AQuery+"?"+q.Encode(), c.apiKey, nil, &resp); err != nil {
		return resp, err
	}
	resp.Status = strings.ToLower(strings.TrimSpace(resp.Status))
	return resp, nil
}

// Download 完成任务的结果文件（retrieve_content 直出二进制）。官方文档提示单文本输入
// 亦可能产出音频+字幕+额外信息的多文件包（zip），此处嗅探 PK 魔数后抽取其中的音频条目。
func (c *TTSLongClient) Download(ctx context.Context, fileID int64) ([]byte, error) {
	if fileID <= 0 {
		return nil, fmt.Errorf("MiniMax 任务结果文件 ID 缺失，无法下载")
	}
	q := url.Values{"file_id": []string{fmt.Sprintf("%d", fileID)}}
	data, err := doDownload(ctx, c.baseURL+pathFileRetrieve+"?"+q.Encode(), c.apiKey)
	if err != nil {
		return nil, err
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")) {
		return extractZipAudio(data)
	}
	return data, nil
}

// zipAudioExts 结果包内按扩展名识别音频条目。
var zipAudioExts = map[string]bool{".mp3": true, ".wav": true, ".flac": true}

// extractZipAudio 从结果 zip 中抽取音频条目：多个音频条目时取体积最大者（主产物）。
func extractZipAudio(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("MiniMax 结果包解析失败: %w", err)
	}
	var best []byte
	for _, f := range zr.File {
		ext := strings.ToLower(f.Name)
		if i := strings.LastIndexByte(ext, '.'); i >= 0 {
			ext = ext[i:]
		} else {
			continue
		}
		if !zipAudioExts[ext] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		// 结果包理论上限 1GB，限量读取防御异常条目撑爆内存
		buf, err := io.ReadAll(io.LimitReader(rc, 1<<30))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(buf)) > int64(len(best)) {
			best = buf
		}
	}
	if best == nil {
		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		return nil, fmt.Errorf("MiniMax 结果包内未找到音频文件（包含: %s）", strings.Join(names, ", "))
	}
	return best, nil
}
