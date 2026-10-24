package qingmusic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Gateway 是音乐解析网关客户端（对应内置的「服务线路」）。
//
//	POST {baseUrl}/music/resolve-url   → 拿播放直链
//	GET  {baseUrl}/music/health        → music-gateway 健康检查
//	GET  {baseUrl}/health              → auth 后端健康检查（同一主机）
type Gateway struct {
	BaseURL string
	HTTP    *http.Client
}

// NewGateway 默认用主线路。
func NewGateway() *Gateway {
	return &Gateway{BaseURL: ServiceLines[0].BaseURL, HTTP: NewHTTPClient(15 * time.Second)}
}

// NewGatewayWith 指定线路（baseURL 传 ServiceLines[i].BaseURL）。
func NewGatewayWith(baseURL string) *Gateway {
	return &Gateway{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: NewHTTPClient(15 * time.Second)}
}

// Health 探测线路：GET {base}/music/health
func (g *Gateway) Health(ctx context.Context) (map[string]any, error) {
	return g.getJSON(ctx, MusicHealthPath)
}

// AuthHealth 探测账号后端：GET {base}/health
func (g *Gateway) AuthHealth(ctx context.Context) (map[string]any, error) {
	return g.getJSON(ctx, AuthHealthPath)
}

func (g *Gateway) getJSON(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := g.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &GatewayError{Code: "GATEWAY_TIMEOUT", Message: "播放地址解析超时"}
		}
		return nil, &GatewayError{Code: "NETWORK_UNREACHABLE", Message: "无法连接播放地址解析服务", HTTPStatus: 0}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, &GatewayError{Code: "INVALID_GATEWAY_RESPONSE", Message: "解析服务返回了无效数据", HTTPStatus: res.StatusCode}
	}
	return out, nil
}

// ResolveURL 换真实播放直链。
//
// 线上请求（与 JS 侧 fetch 完全一致）：
//
//	POST {baseUrl}/music/resolve-url
//	Content-Type: application/json
//	Accept: application/json
//	X-Request-Id: {rid}
//	{"source":"kg","rid":"...","level":"lossless"}
//
// 注意：该接口【不需要鉴权】，不带 Authorization。
func (g *Gateway) ResolveURL(ctx context.Context, source, rid, level string) (*Resolved, error) {
	if level == "" {
		level = LevelStandard
	}
	payload, err := json.Marshal(resolveRequest{Source: source, Rid: rid, Level: level})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.BaseURL+ResolvePath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-Id", rid) // App 用 rid 当请求 ID

	res, err := g.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &GatewayError{Code: "GATEWAY_TIMEOUT", Message: "播放地址解析超时"}
		}
		return nil, &GatewayError{Code: "NETWORK_UNREACHABLE", Message: "无法连接播放地址解析服务"}
	}
	defer res.Body.Close()

	traceID := res.Header.Get("x-trace-id")
	if traceID == "" {
		traceID = rid
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, &GatewayError{Code: "GATEWAY_REQUEST_FAILED", Message: "播放地址解析响应读取失败", TraceID: traceID, HTTPStatus: res.StatusCode}
	}

	var out resolveResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &GatewayError{Code: "INVALID_GATEWAY_RESPONSE", Message: "解析服务返回了无效数据", TraceID: traceID, HTTPStatus: res.StatusCode}
	}
	if out.TraceID != "" {
		traceID = out.TraceID
	}

	codeOK := false
	if out.Code != nil {
		s := strings.Trim(string(*out.Code), `"`)
		codeOK = s == "0" || s == ""
	} else {
		codeOK = true
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || !codeOK {
		msg := "播放地址解析失败"
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return nil, &GatewayError{Code: "GATEWAY_REQUEST_FAILED", Message: msg, TraceID: traceID, HTTPStatus: res.StatusCode}
	}

	if out.Data == nil || out.Data.URL == "" {
		return nil, &GatewayError{Code: "INVALID_GATEWAY_RESPONSE", Message: "解析服务未返回播放地址", TraceID: traceID, HTTPStatus: res.StatusCode}
	}

	r := &Resolved{
		URL:             out.Data.URL,
		Headers:         out.Data.Headers,
		PlaybackHeaders: out.Data.PlaybackHeaders,
		TraceID:         traceID,
	}
	if n, err := strconv.ParseInt(out.Data.ExpiresAt.String(), 10, 64); err == nil {
		r.ExpiresAt = n
	}
	return r, nil
}

// PickFastestLine 并发探测所有线路，返回第一个健康的（失败则返回主线路）。
func PickFastestLine(ctx context.Context) (ServiceLine, error) {
	type result struct {
		line ServiceLine
		err  error
	}
	ch := make(chan result, len(ServiceLines))
	for _, l := range ServiceLines {
		go func(l ServiceLine) {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err := NewGatewayWith(l.BaseURL).Health(c)
			ch <- result{l, err}
		}(l)
	}
	var lastErr error
	for i := 0; i < len(ServiceLines); i++ {
		r := <-ch
		if r.err == nil {
			return r.line, nil
		}
		lastErr = r.err
	}
	return ServiceLines[0], lastErr
}
