// Package localruntime 本地推理运行时:audiocpp 常驻 server(TTS)与 sherpa 一次性子进程(ASR)。
// 仅子进程/HTTP 调用,不引入 cgo。
package localruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
)

// SynthRequest 一次合成请求(参数与 audiocpp_server /v1/tasks/run 对齐)。
type SynthRequest struct {
	ModelID  string // 已安装的 TTS 条目 id(qwen3 GGUF / IndexTTS2.5 GGUF / chatterbox GGUF)
	Family   string // 模型族:决定 server 配置 task(chatterbox→clon)与 options 取舍
	Text     string
	RefWav   string // 克隆:参考 wav 绝对路径(24k mono pcm16);空=preset 模式
	RefText  string // 克隆:参考音频转写;空则走 x_vector_only_mode
	Speaker  string // preset:预置音色名
	Instruct string // 可选风格指令
	Language string

	// —— IndexTTS2.5 情感控制(qwen3 服务端会忽略,透传无害)——
	EmotionText  string  // 情感描述文本;非空 → emotion_text + use_emotion_text:true
	EmotionAlpha float64 // 情感强度 0-1;∈(0,1) 才下发(1.0 为默认全强度,不发送)

	// —— kokoro 家族(sherpa 子进程)——
	SpeakerSID int // 预置音色在 voices.bin 中的 speaker id(qwen3/index 不消费)
}

// TTSRuntime audiocpp_server 生命周期管理:懒启动、健康轮询、崩溃自愈、退出回收。
type TTSRuntime struct {
	dataDir string
	models  *localmodel.Manager

	mu       sync.Mutex
	cmd      *exec.Cmd
	baseURL  string
	exited   atomic.Bool // server 进程退出标志(startLocked 拉起后的 wait goroutine 置位)
	procAttr procStarter

	// 测试 seam:BaseURL 非空时跳过子进程直连;backendOverride 强制后端。
	BaseURL         string
	backendOverride string
	healthInterval  time.Duration
}

type procStarter func(cmd *exec.Cmd) error

// healthClient 健康探针专用短超时 client:不用 DefaultClient(无超时),
// server 起不来时不至于占死连接;单请求 5s 足够本机回环。
var healthClient = &http.Client{Timeout: 5 * time.Second}

func NewTTSRuntime(dataDir string, models *localmodel.Manager) *TTSRuntime {
	return &TTSRuntime{
		dataDir:        dataDir,
		models:         models,
		healthInterval: 250 * time.Millisecond,
		procAttr:       func(cmd *exec.Cmd) error { return cmd.Start() },
	}
}

// Close 回收 server 子进程(服务关闭时调用;幂等)。
func (t *TTSRuntime) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	t.cmd = nil
	t.baseURL = ""
	return nil
}

// ensureHealth 轮询 /health 至就绪(上限 120s);server 未起或已崩溃退出则(重新)拉起。
// 崩溃自愈(spec §4:server 意外退出由下次任务拉起时发现):轮询中发现「进程已退出 &&
// 本任务尚未重建过」则调 startLocked 重建一次;每任务至多重启一次,超出仍走 120s 超时。
func (t *TTSRuntime) ensureHealth(ctx context.Context) (string, error) {
	t.mu.Lock()
	base := t.BaseURL
	ownProcess := base == "" // BaseURL seam 下不管理子进程,不参与崩溃自愈
	if ownProcess {
		if t.cmd == nil || t.exited.Load() {
			url, err := t.startLocked()
			if err != nil {
				t.mu.Unlock()
				return "", err
			}
			base = url
		} else {
			base = t.baseURL
		}
	}
	t.mu.Unlock()
	deadline := time.Now().Add(120 * time.Second)
	restarted := false // 本任务是否已重建过(至多一次)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if ownProcess && !restarted && t.exited.Load() {
			restarted = true
			t.mu.Lock()
			// 双检:并发任务可能已重建(退出标志已复位),直接复用其地址
			if !t.exited.Load() {
				base = t.baseURL
				t.mu.Unlock()
				continue
			}
			url, err := t.startLocked() // 内部重置退出标志并由新 goroutine 监视新进程
			if err != nil {
				t.mu.Unlock()
				return "", err
			}
			base = url
			t.mu.Unlock()
			continue
		}
		// 探针带 ctx + 短超时:ctx 取消立即中断轮询,server 挂起时单请求也有上限
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if err != nil {
			return "", err
		}
		resp, err := healthClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return base, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(t.healthInterval):
		}
	}
	return "", fmt.Errorf("本地合成引擎健康检查超时(120s)")
}

// startLocked 拉起 audiocpp_server(调用方持锁)。返回监听地址。
func (t *TTSRuntime) startLocked() (string, error) {
	if t.models == nil {
		return "", fmt.Errorf("本地模型管理器未初始化")
	}
	bin, err := t.models.EngineBinary("audiocpp")
	if err != nil {
		return "", err
	}
	port, err := freePort()
	if err != nil {
		return "", err
	}
	backend := t.backendOverride
	if backend == "" {
		if runtime.GOOS == "darwin" {
			backend = "metal" // server 默认 cuda,macOS 必须显式 metal(调研实测)
		} else {
			backend = "cpu"
		}
	}
	// models[]:已安装的 tts 条目逐个注册(family 取目录条目:index_tts2/qwen3_tts)
	type serverModel struct {
		ID     string `json:"id"`
		Family string `json:"family"`
		Path   string `json:"path"`
		Task   string `json:"task"`
		Mode   string `json:"mode"`
	}
	var serverModels []serverModel
	for _, e := range t.models.List() {
		// ModelView 同时内嵌 Entry 与 ModelState(二者都有 ID),选择器需显式 Entry
		if e.Entry.Kind != "tts" || !t.models.Installed(e.Entry.ID) {
			continue
		}
		p, err := t.models.InstalledModelFile(e.Entry.ID)
		if err != nil {
			continue // 已安装但文件异常:跳过,Run 时会再校验
		}
		family := e.Entry.Family
		if family == "" {
			family = "qwen3_tts" // 防御:目录已强制 tts 必填 family,空值回落 qwen3
		}
		// task 按家族路由:chatterbox 家族 server 只收 clon/vc,其余走 tts
		task := "tts"
		if family == "chatterbox" {
			task = "clon"
		}
		serverModels = append(serverModels, serverModel{ID: e.Entry.ID, Family: family, Path: p, Task: task, Mode: "offline"})
	}
	if len(serverModels) == 0 {
		return "", fmt.Errorf("没有已安装的本地 TTS 模型:请到设置页下载(Qwen3-TTS / IndexTTS)")
	}
	cfg := map[string]any{
		"host": "127.0.0.1", "port": port, "backend": backend, "device": 0,
		"threads": 4, "lazy_load": true, "max_loaded_models": 1,
		"idle_unload_ms": 300000, "max_request_body_bytes": 1048576,
		"models": serverModels,
	}
	cfgPath := filepath.Join(t.dataDir, "engines", "audiocpp-server.json")
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command(bin, "--config", cfgPath, "--no-ui")
	if err := t.procAttr(cmd); err != nil {
		return "", fmt.Errorf("拉起本地合成引擎失败: %w", err)
	}
	t.cmd = cmd
	t.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	t.exited.Store(false) // 重置退出标志(崩溃自愈裁定):新进程由新 wait goroutine 监视
	go func() {
		_ = cmd.Wait() // 进程退出(含被 Kill)即置位;由下次任务的 ensureHealth 发现并重建
		t.exited.Store(true)
	}()
	return t.baseURL, nil
}

// Synthesize 合成一段语音:健康就绪 → RPC → base64 WAV 落盘(.part→rename)。
func (t *TTSRuntime) Synthesize(ctx context.Context, req SynthRequest, report func(p int, note string)) (string, error) {
	report(5, "检查本地合成引擎…")
	base, err := t.ensureHealth(ctx)
	if err != nil {
		return "", err
	}
	if t.models != nil {
		if !t.models.Installed(req.ModelID) {
			return "", fmt.Errorf("本地模型未安装: %s,请到设置页下载", req.ModelID)
		}
	}
	inner := map[string]any{"text": req.Text}
	opts := map[string]any{}
	if req.RefWav != "" {
		inner["voice_ref"] = req.RefWav
		if req.Family == "chatterbox" {
			// chatterbox 纯零样本克隆:server 只收 voice_ref,reference_text/
			// x_vector_only_mode 是 qwen3 语义,不透传
		} else if req.RefText != "" {
			opts["reference_text"] = req.RefText
			opts["x_vector_only_mode"] = false
		} else {
			opts["x_vector_only_mode"] = true
		}
	}
	if req.Speaker != "" {
		opts["speaker"] = req.Speaker
	}
	if req.Instruct != "" {
		opts["instruct"] = req.Instruct
	}
	// IndexTTS2.5 情感控制:情感文本非空才启用;强度默认 1.0(全强度)不下发,
	// 仅 (0,1) 开区间透传(qwen3 服务端不识别这些键,忽略无害)
	if req.EmotionText != "" {
		opts["emotion_text"] = req.EmotionText
		opts["use_emotion_text"] = true
	}
	if req.EmotionAlpha > 0 && req.EmotionAlpha < 1 {
		opts["emotion_alpha"] = req.EmotionAlpha
	}
	if len(opts) > 0 {
		inner["options"] = opts
	}
	if req.Language != "" {
		inner["language"] = req.Language
	}
	payload, _ := json.Marshal(map[string]any{"model": req.ModelID, "request": inner})

	report(15, "提交合成任务…")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/tasks/run", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("本地合成请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("本地合成引擎响应异常: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Audio string `json:"audio"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Audio == "" {
		return "", fmt.Errorf("本地合成引擎响应无法解析")
	}
	report(80, "合成完成,写入产物…")
	raw, err := base64.StdEncoding.DecodeString(out.Audio)
	if err != nil {
		return "", fmt.Errorf("音频数据解码失败: %w", err)
	}
	outDir := filepath.Join(t.dataDir, "tts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("local_%d.wav", time.Now().UnixNano())
	final := filepath.Join(outDir, name)
	part := final + ".part"
	if err := os.WriteFile(part, raw, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	report(100, "本地合成完成")
	return final, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
