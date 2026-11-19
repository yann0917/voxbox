package deepseek

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// mkWAV 构造最小合法 WAV（16bit PCM 单声道），data 载荷 n 字节、填充非零均匀值
// （模拟真实音频振幅：全零会让 Split 的静音边界吸附在「段预算小于搜索半径」的
// 测试构造下塌缩成微段）。
func mkWAV(sampleRate, n int) []byte {
	buf := make([]byte, 44+n)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+n))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)                    // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1)                    // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))   // sample rate
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2)) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                    // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                   // bits
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(n))
	for i := 44; i < len(buf); i++ {
		buf[i] = 0x11
	}
	return buf
}

func TestTTSSynthesizeAndRun(t *testing.T) {
	var gotVoice, gotFormat, gotPath, gotAuth string
	var gotText string
	wav := mkWAV(24000, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Text    string `json:"text"`
			VoiceID string `json:"voice_id"`
			Format  string `json:"format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotText, gotVoice, gotFormat = body.Text, body.VoiceID, body.Format
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer ts.Close()

	if _, err := NewClient("sk-test", ts.URL).Synthesize(context.Background(), "你好 DeepSeek", "tide"); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if gotPath != "/v1/tools/tts" || gotAuth != "Bearer sk-test" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if gotText != "你好 DeepSeek" || gotVoice != "tide" || gotFormat != "wav" {
		t.Fatalf("body text=%q voice=%q format=%q", gotText, gotVoice, gotFormat)
	}

	// Run 全链路（同包替换 Client 指向 mock 上游），验证产物落盘与 summary
	outDir := t.TempDir()
	tool := NewTTSTool("sk-test", outDir)
	tool.client = NewClient("sk-test", ts.URL)
	out, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{
		"text": "你好", "voice": "",
	}}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Format != "wav" || out.Artifacts[0].Size != int64(len(wav)) {
		t.Fatalf("artifacts = %+v", out.Artifacts)
	}
	if out.Summary["voice"] != DefaultVoice { // 空音色按服务端默认回填
		t.Fatalf("summary voice = %v", out.Summary["voice"])
	}
	abs, _ := resolveOut(outDir, out.Artifacts[0].Path)
	if fi, err := os.Stat(abs); err != nil || fi.Size() != int64(len(wav)) {
		t.Fatalf("产物写入失败: %v", err)
	}
}

func TestTTSBillingLimitAndNoCred(t *testing.T) {
	// 超限本地拦截不出网：mock 上游收到请求即失败
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("超限请求不应出网: %s", r.URL.Path)
	}))
	defer ts.Close()

	tool := NewTTSTool("k", t.TempDir())
	tool.client = NewClient("k", ts.URL)
	long := strings.Repeat("汉", ttsMaxBillingChars/2+1) // 汉字按 2 计，恰好超限
	_, err := tool.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": long}}, func(int, string, map[string]any) {})
	if err == nil || !strings.Contains(err.Error(), "计费字符") {
		t.Fatalf("err = %v, want 计费字符超限", err)
	}

	nc := NewTTSTool("", t.TempDir())
	_, err = nc.Run(context.Background(), provider.TaskInput{Params: map[string]any{"text": "你好"}}, func(int, string, map[string]any) {})
	if !errors.Is(err, ErrNoCred) {
		t.Fatalf("err = %v, want ErrNoCred", err)
	}
}

func TestCheckWAVComplete(t *testing.T) {
	if err := checkWAVComplete(mkWAV(24000, 16)); err != nil {
		t.Fatalf("完整 WAV 报错: %v", err)
	}
	truncated := mkWAV(24000, 16)[:30]
	if err := checkWAVComplete(truncated); err == nil || !strings.Contains(err.Error(), "不完整") {
		t.Fatalf("err = %v, want 截断报错", err)
	}
	if err := checkWAVComplete([]byte("not a wav")); err == nil {
		t.Fatal("非 WAV 应报错")
	}
	// 官方流式产物：RIFF 长度写 0xFFFFFFFF 占位——不算截断
	streaming := mkWAV(24000, 16)
	binary.LittleEndian.PutUint32(streaming[4:8], 0xFFFFFFFF)
	if err := checkWAVComplete(streaming); err != nil {
		t.Fatalf("流式占位 WAV 报错: %v", err)
	}
}

func TestFixStreamingWAV(t *testing.T) {
	src := mkWAV(24000, 16)
	binary.LittleEndian.PutUint32(src[4:8], 0xFFFFFFFF)   // RIFF size 占位
	binary.LittleEndian.PutUint32(src[40:44], 0xFFFFFFFF) // data size 占位
	fixed := fixStreamingWAV(src)
	if got := binary.LittleEndian.Uint32(fixed[4:8]); got != uint32(len(fixed)-8) {
		t.Fatalf("RIFF size = %d, want %d", got, len(fixed)-8)
	}
	if got := binary.LittleEndian.Uint32(fixed[40:44]); got != 16 {
		t.Fatalf("data size = %d, want 16", got)
	}
	// 非 WAV 原样返回
	if got := fixStreamingWAV([]byte("junk")); string(got) != "junk" {
		t.Fatalf("junk = %q", got)
	}
}

func TestBillingChars(t *testing.T) {
	if got := billingChars("你好ab 1"); got != 8 { // 2 汉字×2 + a/b/空格/1 各×1
		t.Fatalf("billingChars = %d, want 8", got)
	}
	if got := billingChars(""); got != 0 {
		t.Fatalf(`billingChars("") = %d`, got)
	}
}
