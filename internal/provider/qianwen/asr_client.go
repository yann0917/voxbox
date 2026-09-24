package qianwen

import (
	"context"
	"fmt"
	"net/http"
)

// ASRParams filetrans 提交参数（nil/零值不发送，走上游默认）。
type ASRParams struct {
	LanguageHints      []string
	DiarizationEnabled bool
	EnableITN          *bool
	EnableWords        *bool
}

// 任务状态（DashScope 异步任务口径）。
const (
	StatusPending   = "PENDING"
	StatusRunning   = "RUNNING"
	StatusSucceeded = "SUCCEEDED"
	StatusFailed    = "FAILED"
)

type asrSubmitRequest struct {
	Model      string         `json:"model"`
	Input      asrSubmitInput `json:"input"`
	Parameters *ASRParamsWire `json:"parameters,omitempty"`
}

// qwen3 系模型 input.file_url 单串；qwen-audio / fun-asr 系 input.file_urls 数组。
type asrSubmitInput struct {
	FileURL  string   `json:"file_url,omitempty"`
	FileURLs []string `json:"file_urls,omitempty"`
}

type ASRParamsWire struct {
	LanguageHints      []string `json:"language_hints,omitempty"`
	DiarizationEnabled bool     `json:"diarization_enabled,omitempty"`
	EnableITN          *bool    `json:"enable_itn,omitempty"`
	EnableWords        *bool    `json:"enable_words,omitempty"`
}

type asrSubmitResponse struct {
	Output struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
	} `json:"output"`
	apiError
}

// TranscriptionTask 异步任务查询结果；FAILED 时 Message 为上游原因。
type TranscriptionTask struct {
	TaskID            string
	Status            string
	TranscriptionURLs []string
	Message           string
}

type taskQueryResponse struct {
	Output struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		Message    string `json:"message"`
		Results    []struct {
			TranscriptionURL string `json:"transcription_url"`
		} `json:"results"`
	} `json:"output"`
	apiError
}

// Transcription transcription_url 指向的转写结果 JSON（transcripts[].sentences[]）。
type Transcription struct {
	Transcripts []struct {
		Sentences []TranscriptionSentence `json:"sentences"`
	} `json:"transcripts"`
}

type TranscriptionSentence struct {
	BeginTime int64  `json:"begin_time"`
	EndTime   int64  `json:"end_time"`
	Text      string `json:"text"`
	SpeakerID string `json:"speaker_id,omitempty"`
	Words     []struct {
		BeginTime int64  `json:"begin_time"`
		EndTime   int64  `json:"end_time"`
		Text      string `json:"text"`
	} `json:"words"`
}

// ASRClient 千问 filetrans 文件转写客户端。
type ASRClient struct{ apiKey, baseURL string }

func NewASRClient(apiKey, baseURL string) *ASRClient {
	return &ASRClient{apiKey: apiKey, baseURL: trimSlash(baseURL)}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// SubmitTranscription 提交异步转写任务。model 决定 input 形态：qwen3 系单 file_url，
// 其余（qwen-audio / fun-asr 系）file_urls 数组。
func (c *ASRClient) SubmitTranscription(ctx context.Context, model, fileURL string, p ASRParams) (string, error) {
	in := asrSubmitInput{}
	if isQwen3Model(model) {
		in.FileURL = fileURL
	} else {
		in.FileURLs = []string{fileURL}
	}
	var wire *ASRParamsWire
	if len(p.LanguageHints) > 0 || p.DiarizationEnabled || p.EnableITN != nil || p.EnableWords != nil {
		wire = &ASRParamsWire{
			LanguageHints:      p.LanguageHints,
			DiarizationEnabled: p.DiarizationEnabled,
			EnableITN:          p.EnableITN,
			EnableWords:        p.EnableWords,
		}
	}
	body := asrSubmitRequest{Model: model, Input: in, Parameters: wire}
	var resp asrSubmitResponse
	if err := doJSON(ctx, http.MethodPost, c.baseURL+pathASRSub, c.apiKey,
		map[string]string{"X-DashScope-Async": "enable"}, body, &resp); err != nil {
		return "", err
	}
	if resp.Output.TaskID == "" {
		return "", fmt.Errorf("千问转写提交未返回 task_id")
	}
	return resp.Output.TaskID, nil
}

func isQwen3Model(model string) bool {
	return len(model) >= 6 && model[:6] == "qwen3-"
}

// QueryTask 查询异步任务状态；SUCCEEDED 时带出转写结果地址。
func (c *ASRClient) QueryTask(ctx context.Context, taskID string) (TranscriptionTask, error) {
	var resp taskQueryResponse
	if err := doJSON(ctx, http.MethodGet, fmt.Sprintf(c.baseURL+pathTaskFmt, taskID), c.apiKey, nil, nil, &resp); err != nil {
		return TranscriptionTask{}, err
	}
	t := TranscriptionTask{
		TaskID:  resp.Output.TaskID,
		Status:  resp.Output.TaskStatus,
		Message: resp.Output.Message,
	}
	for _, r := range resp.Output.Results {
		if r.TranscriptionURL != "" {
			t.TranscriptionURLs = append(t.TranscriptionURLs, r.TranscriptionURL)
		}
	}
	return t, nil
}

// FetchTranscription 拉取 transcription_url 的转写 JSON。该 URL 为跨域预签名地址，
// 传空 apiKey 使 doJSON 不附带 Authorization 头（避免凭证外泄与签名参数冲突）。
func (c *ASRClient) FetchTranscription(ctx context.Context, url string) (Transcription, error) {
	var tr Transcription
	if err := doJSON(ctx, http.MethodGet, url, "", nil, nil, &tr); err != nil {
		return Transcription{}, err
	}
	return tr, nil
}
