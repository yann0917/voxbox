// Package freetranslate 免费翻译源(零凭证):Google 免费接口 + DeepLX 自建端点,
// 供字幕翻译免费链使用。接口均为社区通用形态(Google client=gtx 一次只译一段,
// 调用方逐条喂;DeepLX 为 /v1/translate|/translate POST JSON)。
package freetranslate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 包级测试缝:非空时覆盖真实端点主机部分(生产恒为空);同包测试直接赋值,
// 不经环境变量。/translate_a/single 与 /translate 路径由实现拼接。
var (
	testGoogleBase string
	testDeepLXBase string
)

const (
	googleEndpoint   = "https://translate.googleapis.com"
	googleSinglePath = "/translate_a/single"
)

var freeClient = &http.Client{Timeout: 15 * time.Second}

// googleLang 语言码映射:zh→zh-CN、zh-Hant→zh-TW,其余透传;空码报错。
func googleLang(code string) (string, error) {
	switch code {
	case "":
		return "", fmt.Errorf("目标语言不能为空")
	case "zh":
		return "zh-CN", nil
	case "zh-Hant":
		return "zh-TW", nil
	}
	return code, nil
}

func googleURL() string {
	if testGoogleBase != "" {
		return testGoogleBase + googleSinglePath
	}
	return googleEndpoint + googleSinglePath
}

// GoogleFree 单条翻译(client=gtx)。响应 data[0] 为 [译段,原段,...] 数组的数组,
// 译段顺序拼接即全文;一次只译一段,调用方逐条喂。
func GoogleFree(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	sl, err := googleLang(sourceLang)
	if err != nil {
		sl = "auto" // 源为空走自动检测
	}
	tl, err := googleLang(targetLang)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("client", "gtx")
	q.Set("sl", sl)
	q.Set("tl", tl)
	q.Set("dt", "t")
	q.Set("q", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleURL()+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	resp, err := freeClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google 免费翻译请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("google 免费翻译 HTTP %d", resp.StatusCode)
	}
	var shape []any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&shape); err != nil {
		return "", fmt.Errorf("google 免费翻译响应解析失败: %w", err)
	}
	if len(shape) == 0 {
		return "", fmt.Errorf("google 免费翻译响应为空")
	}
	rows, ok := shape[0].([]any)
	if !ok {
		return "", fmt.Errorf("google 免费翻译响应形状异常")
	}
	var b strings.Builder
	for _, row := range rows {
		cells, ok := row.([]any)
		if !ok || len(cells) == 0 {
			continue
		}
		if s, ok := cells[0].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String(), nil
}

// DeepLX DeepLX 端点翻译:POST {endpoint}/translate {"text","source_lang","target_lang"}
// → {"code":200,"data":"译文"}。语言码转大写基础码(zh-Hant→ZH,DeepLX 免费端点不保证繁体)。
func DeepLX(ctx context.Context, endpoint, text, sourceLang, targetLang string) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", fmt.Errorf("未配置 DeepLX 端点(voxbox config set translate.deeplx_url)")
	}
	base := strings.TrimRight(endpoint, "/")
	if testDeepLXBase != "" {
		base = testDeepLXBase
	}
	upper := func(c string) string {
		if i := strings.IndexByte(c, '-'); i > 0 {
			c = c[:i]
		}
		return strings.ToUpper(c)
	}
	body, _ := json.Marshal(map[string]string{
		"text": text, "source_lang": upper(sourceLang), "target_lang": upper(targetLang),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/translate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := freeClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("DeepLX 请求失败: %w", err)
	}
	defer resp.Body.Close()
	var shape struct {
		Code    any    `json:"code"`
		Data    string `json:"data"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&shape); err != nil {
		return "", fmt.Errorf("DeepLX 响应解析失败: %w", err)
	}
	if shape.Data == "" {
		if shape.Message != "" {
			return "", fmt.Errorf("DeepLX 错误: %s", shape.Message)
		}
		return "", fmt.Errorf("DeepLX 未返回译文")
	}
	return shape.Data, nil
}

// Chain 免费源回退链:Google(失败重试 1 次)→ DeepLX(端点已配置时)。
// 全败聚合报错;zh-Hant 目标直接报错(免费链不支持,提示改用 AI/火山)。
func Chain(deeplxEndpoint string) func(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	return func(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
		if strings.EqualFold(strings.TrimSpace(targetLang), "zh-Hant") {
			return "", fmt.Errorf("免费翻译不支持繁体中文目标,请改用 AI 或火山翻译")
		}
		var errs []string
		for attempt := 0; attempt < 2; attempt++ {
			tr, err := GoogleFree(ctx, text, sourceLang, targetLang)
			if err == nil {
				return tr, nil
			}
			errs = append(errs, "google: "+err.Error())
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
		}
		if deeplxEndpoint != "" {
			tr, err := DeepLX(ctx, deeplxEndpoint, text, sourceLang, targetLang)
			if err == nil {
				return tr, nil
			}
			errs = append(errs, "deeplx: "+err.Error())
		}
		return "", fmt.Errorf("免费翻译源全部失败(%s)", strings.Join(errs, "; "))
	}
}
