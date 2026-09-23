package volcengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

const (
	ttsBaseURL = "https://openspeech.bytedance.com"
	ttsCluster = "volcano_tts"
	ttsCodeOK  = 3000
)

type TTSSynthesizeReq struct {
	Text        string
	VoiceType   string
	Format      string
	SpeedRatio  float64
	VolumeRatio float64
}

type TTSSynthesizeResp struct {
	Audio      []byte
	DurationMS int64
}

type TTSClient struct {
	resty *resty.Client
	cred  SpeechCred
}

func NewTTSClient(cred SpeechCred) *TTSClient {
	return NewTTSClientWithBaseURL(cred, ttsBaseURL)
}

// NewTTSClientWithBaseURL 供测试注入 mock 地址。
func NewTTSClientWithBaseURL(cred SpeechCred, baseURL string) *TTSClient {
	r := resty.New().SetBaseURL(baseURL).
		SetHeader("Content-Type", "application/json").
		SetTimeout(60 * time.Second)
	return &TTSClient{resty: r, cred: cred}
}

type ttsAPIRequest struct {
	App struct {
		AppID   string `json:"appid"`
		Token   string `json:"token"`
		Cluster string `json:"cluster"`
	} `json:"app"`
	User struct {
		UID string `json:"uid"`
	} `json:"user"`
	Audio struct {
		VoiceType   string  `json:"voice_type"`
		Encoding    string  `json:"encoding"`
		SpeedRatio  float64 `json:"speed_ratio"`
		VolumeRatio float64 `json:"volume_ratio"`
	} `json:"audio"`
	Request struct {
		ReqID     string `json:"reqid"`
		Text      string `json:"text"`
		Operation string `json:"operation"`
	} `json:"request"`
}

type ttsAPIResponse struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	Data     string `json:"data"`
	Addition struct {
		Duration string `json:"duration"`
	} `json:"addition"`
}

func (c *TTSClient) Synthesize(ctx context.Context, req TTSSynthesizeReq) (TTSSynthesizeResp, error) {
	if req.SpeedRatio == 0 {
		req.SpeedRatio = 1.0
	}
	if req.VolumeRatio == 0 {
		req.VolumeRatio = 1.0
	}
	if req.VoiceType == "" {
		req.VoiceType = "zh_female_cancan_mars_bigtts"
	}

	var apiReq ttsAPIRequest
	apiReq.App.AppID = c.cred.AppID
	apiReq.App.Token = c.cred.AccessToken
	apiReq.App.Cluster = ttsCluster
	apiReq.User.UID = "voxbox"
	apiReq.Audio.VoiceType = req.VoiceType
	apiReq.Audio.Encoding = req.Format
	apiReq.Audio.SpeedRatio = req.SpeedRatio
	apiReq.Audio.VolumeRatio = req.VolumeRatio
	apiReq.Request.ReqID = newRequestID()
	apiReq.Request.Text = req.Text
	apiReq.Request.Operation = "query"

	httpResp, err := c.resty.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer;"+c.cred.AccessToken).
		SetBody(apiReq).
		Post("/api/v1/tts")
	if err != nil {
		return TTSSynthesizeResp{}, fmt.Errorf("请求火山 TTS 失败: %w", err)
	}

	var apiResp ttsAPIResponse
	if err := json.Unmarshal(httpResp.Body(), &apiResp); err != nil {
		return TTSSynthesizeResp{}, fmt.Errorf("解析火山 TTS 响应失败: %w", err)
	}
	if apiResp.Code != ttsCodeOK {
		if apiResp.Code == 3001 || apiResp.Code == 3005 {
			return TTSSynthesizeResp{}, fmt.Errorf("%w: %s(%d)", ErrAuth, apiResp.Message, apiResp.Code)
		}
		return TTSSynthesizeResp{}, fmt.Errorf("火山 TTS 错误 %s(%d)", apiResp.Message, apiResp.Code)
	}
	audio, err := base64.StdEncoding.DecodeString(apiResp.Data)
	if err != nil {
		return TTSSynthesizeResp{}, fmt.Errorf("音频数据解码失败: %w", err)
	}
	var durationMS int64
	if d, err := strconv.ParseInt(apiResp.Addition.Duration, 10, 64); err == nil {
		durationMS = d
	}
	return TTSSynthesizeResp{Audio: audio, DurationMS: durationMS}, nil
}

func newRequestID() string { return uuid.NewString() }
