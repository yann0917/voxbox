package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/config"
)

func TestScanStreamDeltas(t *testing.T) {
	// qwen3.8 真机形态：先 reasoning_content 思考通道（不回调），再 content 正文，最后 usage-only 分片
	body := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning_content\":\"\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"\",\"reasoning_content\":\"思考\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n" +
		"\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"total_tokens\":9}}\n\n" +
		"data: [DONE]\n\n"
	var got []string
	if err := scanStream(context.Background(), ProviderQianwen, strings.NewReader(body), func(d string) {
		got = append(got, d)
	}); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if strings.Join(got, "") != "你好" {
		t.Fatalf("deltas = %q, want 你好", got)
	}
}

func TestScanStreamInlineError(t *testing.T) {
	// 个别平台在流内以 data 行下发错误体
	body := "data: {\"error\":{\"code\":\"1113\",\"message\":\"余额不足\"}}\n\n"
	err := scanStream(context.Background(), ProviderZhipu, strings.NewReader(body), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "智谱") || !strings.Contains(err.Error(), "余额不足") {
		t.Fatalf("err = %v, want 智谱 API 错误: 余额不足", err)
	}
}

func TestScanStreamRawJSONError(t *testing.T) {
	// 流式请求直接回 JSON 错误体、无任何 data 行（智谱余额不足的真机形态）
	body := "{\"error\":{\"code\":\"1113\",\"message\":\"余额不足或无可用资源包,请充值。\"}}"
	err := scanStream(context.Background(), ProviderZhipu, strings.NewReader(body), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "余额不足") {
		t.Fatalf("err = %v, want 余额不足", err)
	}
}

func TestScanStreamDoneWithoutTerminator(t *testing.T) {
	// 上游未发 [DONE] 直接断流：正常收尾不算错误
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n"
	var got []string
	if err := scanStream(context.Background(), ProviderXiaomi, strings.NewReader(body), func(d string) {
		got = append(got, d)
	}); err != nil {
		t.Fatalf("scanStream: %v", err)
	}
	if strings.Join(got, "") != "好" {
		t.Fatalf("deltas = %q, want 好", got)
	}
}

func TestBuildChatRequest(t *testing.T) {
	req := buildChatRequest(ProviderQianwen, "qwen3.8-flash", []Message{{Role: "user", Content: "你好"}})
	if !req.Stream || len(req.Messages) != 2 {
		t.Fatalf("req = %+v", req)
	}
	if req.Messages[0].Role != "system" || req.Messages[1].Role != "user" || req.Messages[1].Content != "你好" {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if req.EnableThinking == nil || *req.EnableThinking {
		t.Fatalf("千问应携带 enable_thinking=false, got %+v", req.EnableThinking)
	}
	other := buildChatRequest(ProviderZhipu, "glm-5.3-flash", nil)
	if other.EnableThinking != nil {
		t.Fatalf("智谱不应发送 enable_thinking")
	}
}

func TestModelAllowed(t *testing.T) {
	if !ModelAllowed(ProviderZhipu, "glm-5.3-flash") || !ModelAllowed(ProviderXiaomi, "mimo-v2.6-pro") {
		t.Fatal("目录内模型应放行")
	}
	if ModelAllowed(ProviderZhipu, "gpt-4o") || ModelAllowed(Provider("foo"), "glm-5.3") {
		t.Fatal("白名单外模型应拒绝")
	}
}

func TestCatalogWith(t *testing.T) {
	cfg := &config.Config{}
	cat := CatalogWith(cfg)
	if len(cat) != 3 {
		t.Fatalf("平台数 = %d, want 3", len(cat))
	}
	for _, p := range cat {
		if p.Enabled {
			t.Fatalf("空配置下 %s 不应可用", p.Provider)
		}
		if len(p.Models) != 2 {
			t.Fatalf("%s 模型数 = %d, want 2", p.Provider, len(p.Models))
		}
	}
	cfg.Zhipu.APIKey = "k"
	if cat := CatalogWith(cfg); cat[0].Enabled != true || cat[1].Enabled || cat[2].Enabled {
		t.Fatalf("仅配置智谱时 Enabled 判定错误: %+v", cat)
	}
}

func TestKnownProviderAndLabel(t *testing.T) {
	if !KnownProvider(ProviderQianwen) || KnownProvider("volc") || KnownProvider("") {
		t.Fatal("KnownProvider 判定错误")
	}
	if Label(ProviderZhipu) != "智谱" || Label("foo") != "foo" {
		t.Fatal("Label 判定错误")
	}
}

func TestCheckCredentialWrapsSentinel(t *testing.T) {
	err := CheckCredential(&config.Config{}, ProviderXiaomi)
	if err == nil || !strings.Contains(err.Error(), "小米 API Key 未配置") {
		t.Fatalf("err = %v, want 小米 API Key 未配置", err)
	}
}
