package zhipu

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// ASRClient 智谱 ASR 客户端：multipart 直传音频文件（本地路径），同步返回转写文本。
type ASRClient struct{ apiKey, baseURL string }

func NewASRClient(apiKey, baseURL string) *ASRClient {
	return &ASRClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

// asrResponse 语音转文本响应（stream=false）。
type asrResponse struct {
	Text string `json:"text"`
	apiError
}

// Transcribe 转写本地音频文件：multipart 上传（file 字段），prompt 为长文本上下文、
// hotwords 为热词表（≤100 个），均可空。限制：wav/mp3 ≤25MB ≤30 秒（官方口径，工具层校验）。
func (c *ASRClient) Transcribe(ctx context.Context, audioPath, prompt string, hotwords []string) (string, error) {
	fields := map[string]string{"model": "glm-asr-2512", "stream": "false"}
	if strings.TrimSpace(prompt) != "" {
		fields["prompt"] = strings.TrimSpace(prompt)
	}
	var resp asrResponse
	if err := doMultipart(ctx, c.baseURL+pathASR, c.apiKey, fields, "file", audioPath, &resp,
		hotwordFields(hotwords)...); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Text) == "" {
		return "", fmt.Errorf("智谱 ASR 响应中未找到转写文本（text 为空）")
	}
	return resp.Text, nil
}

// hotwordFields 热词数组按 multipart form 语义展开为多个同名字段（hotwords=a&hotwords=b 的
// form 版本；官方 schema 声明 hotwords 为 array of string）。
func hotwordFields(hotwords []string) [][2]string {
	out := make([][2]string, 0, len(hotwords))
	for _, w := range hotwords {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, [2]string{"hotwords", w})
		}
	}
	return out
}

// VoiceClone 基于示例音频复刻音色：fileID 为经文件接口上传的示例音频（purpose=voice-clone-input），
// name 为唯一音色名，input 为试听文本。返回复刻音色 ID（voice_clone_*，可直接用于合成）。
func (c *VoiceClient) VoiceClone(ctx context.Context, name, fileID, input, sampleText string) (string, error) {
	body := map[string]any{
		"model":      "glm-tts-clone",
		"voice_name": name,
		"file_id":    fileID,
		"input":      input,
	}
	if strings.TrimSpace(sampleText) != "" {
		body["text"] = strings.TrimSpace(sampleText)
	}
	var resp struct {
		Voice string `json:"voice"`
		apiError
	}
	if err := doJSON(ctx, http.MethodPost, c.baseURL+pathVoiceClone, c.apiKey, body, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Voice) == "" {
		return "", fmt.Errorf("智谱音色复刻响应中未返回音色 ID（voice 为空）")
	}
	return resp.Voice, nil
}

// VoiceDelete 删除指定音色（含复刻音色）。
func (c *VoiceClient) VoiceDelete(ctx context.Context, voice string) error {
	var resp struct {
		Voice string `json:"voice"`
		apiError
	}
	return doJSON(ctx, http.MethodPost, c.baseURL+pathVoiceDel, c.apiKey, map[string]any{"voice": voice}, &resp)
}

// UploadVoiceSample 上传复刻示例音频（POST /paas/v4/files，purpose=voice-clone-input），
// 返回 file_id。限制：mp3/wav ≤10MB，建议时长 3-30 秒。
func (c *VoiceClient) UploadVoiceSample(ctx context.Context, audioPath string) (string, error) {
	var resp struct {
		ID string `json:"id"`
		apiError
	}
	err := doMultipart(ctx, c.baseURL+pathFiles, c.apiKey,
		map[string]string{"purpose": "voice-clone-input"}, "file", audioPath, &resp)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", fmt.Errorf("智谱文件上传响应中未返回文件 ID（id 为空）")
	}
	return resp.ID, nil
}
