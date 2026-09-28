package xiaomi

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yann0917/voxbox/internal/provider"
)

// asrFixture 同步转写响应夹具：content 为纯文本，seconds 为音频时长。
func asrFixture(content string, seconds int) string {
	return `{"choices":[{"message":{"content":"` + content + `"}}],
		"usage":{"prompt_tokens_details":{"audio_tokens":10,"seconds":` + itoa(seconds) + `}}}`
}

func itoa(n int) string { return strconv.Itoa(n) }

// mp3Bytes 伪造带 ID3 魔数的 mp3 内容（sniff 通过即可，不发真实上游）。
var mp3Bytes = append([]byte("ID3\x04\x00\x00\x00"), []byte("fake-audio-payload")...)

// asr 工具：本地文件直读（Files["audio"]，无需对象存储）、_out 重定向、summary 汇总。
func TestASRToolRunLocalFile(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		body = string(raw)
		_, _ = w.Write([]byte(asrFixture("你好，世界", 4)))
	}))
	defer srv.Close()

	out := t.TempDir()
	src := filepath.Join(t.TempDir(), "clip.mp3")
	if err := os.WriteFile(src, mp3Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewASRTool("sk-test", out)
	tool.client = NewASRClient("sk-test", srv.URL)

	outPath := filepath.Join(out, "transcript.txt")
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"_out": outPath, "language": "zh"},
		Files:  map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Kind != "transcript" || res.Artifacts[0].Format != "txt" {
		t.Fatalf("产物不符: %+v", res.Artifacts)
	}
	if res.Artifacts[0].Path != "transcript.txt" {
		t.Errorf("产物路径 = %q（_out 重定向后应为相对 out）", res.Artifacts[0].Path)
	}
	if res.Artifacts[0].DurationMS != 4000 {
		t.Errorf("DurationMS = %d（usage.seconds×1000）", res.Artifacts[0].DurationMS)
	}
	if raw, err := os.ReadFile(outPath); err != nil || string(raw) != "你好，世界" {
		t.Errorf("转写文本落盘不符: %q err=%v", raw, err)
	}
	for _, want := range []string{`"type":"input_audio"`, `"data":"data:audio/mpeg;base64,`, `"language":"zh"`} {
		if !strings.Contains(body, want) {
			t.Errorf("请求体缺少 %s:\n%s", want, body)
		}
	}
	if res.Summary["source"] != "file" || res.Summary["model"] != asrModel || res.Summary["duration_ms"] != int64(4000) {
		t.Errorf("Summary 不符: %+v", res.Summary)
	}
}

// URL 输入：下载字节后转写，不带 Authorization 头（用户公网地址不外泄凭证）。
func TestASRToolRunURL(t *testing.T) {
	var audioAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clip.wav" {
			audioAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("RIFF\x24\x00\x00\x00WAVE" + "data"))
			return
		}
		_, _ = w.Write([]byte(asrFixture("wav text", 2)))
	}))
	defer srv.Close()
	tool := NewASRTool("sk-test", t.TempDir())
	tool.client = NewASRClient("sk-test", srv.URL)

	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"url": srv.URL + "/clip.wav"},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if audioAuth != "" {
		t.Errorf("下载用户 URL 不应附带 Authorization 头, got %q", audioAuth)
	}
	if res.Summary["source"] != "url" {
		t.Errorf("Summary.source = %v", res.Summary["source"])
	}
	if res.Artifacts[0].Format != "txt" || res.Artifacts[0].DurationMS != 2000 {
		t.Errorf("产物字段不符: %+v", res.Artifacts[0])
	}
}

// 格式白名单（非 mp3/wav 魔数）与载荷超限在请求前拦截；缺输入/缺凭证按契约报错。
func TestASRToolValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发起上游请求")
	}))
	defer srv.Close()
	tool := NewASRTool("sk-test", t.TempDir())
	tool.client = NewASRClient("sk-test", srv.URL)
	run := func(params map[string]any, files map[string]string) error {
		t.Helper()
		_, err := tool.Run(context.Background(), provider.TaskInput{Params: params, Files: files},
			func(int, string, map[string]any) {})
		return err
	}

	// 缺输入（含「缺少必填参数」→ CLI/Web 均映射参数错误）
	err := run(map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "缺少必填参数") {
		t.Errorf("缺输入应报缺少必填参数, got %v", err)
	}
	// URL scheme 校验
	err = run(map[string]any{"url": "ftp://example.com/a.mp3"}, nil)
	if err == nil || !strings.Contains(err.Error(), "http(s)") {
		t.Errorf("非 http(s) URL 应报参数错误, got %v", err)
	}
	// 非 mp3/wav 魔数（flac 头）在请求前拦截
	bad := filepath.Join(t.TempDir(), "song.flac")
	if err := os.WriteFile(bad, []byte("fLaC\x00\x00\x00\x22fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run(map[string]any{}, map[string]string{"audio": bad})
	if err == nil || !strings.Contains(err.Error(), "仅支持 mp3 / wav") {
		t.Errorf("flac 应报仅支持 mp3/wav, got %v", err)
	}
	// 载荷超限（>7.5MB）不再硬拦截，转自动分段；不可探测的垃圾内容报可定位错误
	big := filepath.Join(t.TempDir(), "big.mp3")
	if err := os.WriteFile(big, append(mp3Bytes, make([]byte, asrMaxRaw)...), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run(map[string]any{}, map[string]string{"audio": big})
	if err == nil || !strings.Contains(err.Error(), "自动分段") {
		t.Errorf("超限应转自动分段并报可定位错误, got %v", err)
	}
	// 整文件上限（200MB）：稀疏文件只截断不落盘
	huge := filepath.Join(t.TempDir(), "huge.wav")
	if f, ferr := os.Create(huge); ferr != nil {
		t.Fatal(ferr)
	} else if _, ferr := f.WriteString("RIFF"); ferr != nil {
		t.Fatal(ferr)
	} else if ferr := f.Truncate(asrInputCap + 1); ferr != nil {
		t.Fatal(ferr)
	} else {
		f.Close()
	}
	err = run(map[string]any{}, map[string]string{"audio": huge})
	if err == nil || !strings.Contains(err.Error(), "火山引擎") {
		t.Errorf("超 200MB 应拦截并指引火山引擎, got %v", err)
	}

	// 缺凭证（输入就绪后才检查，包装 ErrNoCred 哨兵）
	tool.apiKey = ""
	fine := filepath.Join(t.TempDir(), "ok.mp3")
	if err := os.WriteFile(fine, mp3Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	err = run(map[string]any{}, map[string]string{"audio": fine})
	if err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Errorf("缺凭证应报配置指引, got %v", err)
	}
	if !errors.Is(err, ErrNoCred) {
		t.Errorf("缺凭证错误应包装 ErrNoCred 哨兵, got %v", err)
	}
}

// 超大 wav 自动分段：缩小段预算后，4KB wav 应切成多段逐段转写拼接，
// summary 透出 segment_num 与整体 duration_ms。
func TestASRToolSegmentedWAV(t *testing.T) {
	oldBudget := asrSegBytes
	asrSegBytes = 1024
	defer func() { asrSegBytes = oldBudget }()

	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		_, _ = w.Write([]byte(asrFixture("段", 1)))
	}))
	defer srv.Close()

	out := t.TempDir()
	src := filepath.Join(t.TempDir(), "long.wav")
	if err := os.WriteFile(src, testWAV(8000, 8000), 0o644); err != nil { // 16KB data → 16 段
		t.Fatal(err)
	}
	tool := NewASRTool("sk-test", out)
	tool.client = NewASRClient("sk-test", srv.URL)
	res, err := tool.Run(context.Background(), provider.TaskInput{
		Params: map[string]any{"_out": filepath.Join(out, "t.txt")},
		Files:  map[string]string{"audio": src},
	}, func(int, string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	if count < 2 {
		t.Fatalf("上游请求数 = %d, want >= 2（分段转写）", count)
	}
	if res.Summary["segment_num"] != count {
		t.Errorf("segment_num = %v, want %d", res.Summary["segment_num"], count)
	}
	if res.Summary["duration_ms"] != int64(1000) {
		t.Errorf("duration_ms = %v, want 1000（整段 1s）", res.Summary["duration_ms"])
	}
	raw, err := os.ReadFile(filepath.Join(out, "t.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat("段", count); string(raw) != want {
		t.Errorf("拼接文本 = %q, want %q", raw, want)
	}
}

// testWAV 构造 16bit 单声道 PCM WAV（sampleRate 采样率、frames 帧数）。
func testWAV(sampleRate, frames int) []byte {
	data := make([]byte, 2*frames)
	out := make([]byte, 44+len(data))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(data)))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], 1)
	binary.LittleEndian.PutUint32(out[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(data)))
	copy(out[44:], data)
	return out
}

// ParamSpecs 约束：无 model 枚举（上游单模型）、language 三档、Meta 归属 xiaomi.asr。
func TestASRToolSpecs(t *testing.T) {
	tool := NewASRTool("", t.TempDir())
	if m := tool.Meta(); m.Provider != "xiaomi" || m.Name != "asr" {
		t.Errorf("Meta = %+v", tool.Meta())
	}
	hasLang, hasModel := false, false
	for _, s := range tool.ParamSpecs() {
		if s.Key == "language" {
			hasLang = len(s.Options) == 3
		}
		if s.Key == "model" {
			hasModel = true
		}
	}
	if !hasLang {
		t.Error("language 枚举应为自动/中文/英语三档")
	}
	if hasModel {
		t.Error("上游仅 mimo-v2.5-asr 一款模型，ParamSpecs 不应暴露 model 枚举")
	}
}
