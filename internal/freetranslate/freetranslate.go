// Package freetranslate 免费翻译源(零凭证):DeepLX 自建端点,供字幕翻译免费链
// 使用。POST {endpoint}/translate JSON,社区通用形态。Google 免费接口已移除:
// 国内网络不可达(反滥用拦截),自建 DeepLX 是免费链的稳定通道。
package freetranslate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 包级测试缝:非空时覆盖真实端点(生产恒为空);同包测试直接赋值。
var testDeepLXBase string

var freeClient = &http.Client{Timeout: 15 * time.Second}

// DeepLX DeepLX 端点翻译:POST {endpoint}/translate {"text","source_lang","target_lang"}
// → {"code":200,"data":"译文"}。语言码转大写基础码(zh-Hant→ZH,DeepLX 免费端点不保证繁体,
// 繁体目标由 Chain 前置拦截引导走 AI/火山)。
func DeepLX(ctx context.Context, endpoint, text, sourceLang, targetLang string) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", fmt.Errorf("免费翻译需自建 DeepLX 端点(voxbox config set translate.deeplx_url),或改用 AI / 火山翻译")
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

// Chain 免费源入口:繁体目标前置拦截(DeepLX 免费端点不保证繁体),其余直通
// DeepLX。保留函数形态供引擎与测试消费,回退语义已随 Google 源移除而终结。
func Chain(deeplxEndpoint string) func(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	return func(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
		if strings.EqualFold(strings.TrimSpace(targetLang), "zh-Hant") {
			return "", fmt.Errorf("免费翻译不支持繁体中文目标,请改用 AI 或火山翻译")
		}
		return DeepLX(ctx, deeplxEndpoint, text, sourceLang, targetLang)
	}
}
