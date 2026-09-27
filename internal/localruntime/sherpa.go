package localruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SherpaResult sherpa-onnx-offline stdout 的解析结果(字段为其实测 JSON 输出)。
type SherpaResult struct {
	Text       string    `json:"text"`
	Lang       string    `json:"lang"`
	Emotion    string    `json:"emotion"`
	Timestamps []float64 `json:"timestamps"`
}

// Transcribe 一次性子进程识别:启动前校验模型文件在位,stdout 解析一行 JSON,
// 失败取 stderr 末行(audiotool runner 同款报错风格)。ctx 取消即 kill 子进程。
func Transcribe(ctx context.Context, binPath, modelDir, wav string, language string, itn bool) (SherpaResult, error) {
	var res SherpaResult
	onnx := filepath.Join(modelDir, "model.int8.onnx")
	tokens := filepath.Join(modelDir, "tokens.txt")
	for _, p := range []string{onnx, tokens, wav} {
		if _, err := os.Stat(p); err != nil {
			return res, fmt.Errorf("输入缺失: %s", p)
		}
	}
	args := []string{
		"--sense-voice-model=" + onnx,
		"--tokens=" + tokens,
		"--sense-voice-use-itn=" + strconv.FormatBool(itn),
	}
	if language != "" && language != "auto" {
		args = append(args, "--sense-voice-language="+language)
	}
	args = append(args, wav)
	cmd := exec.CommandContext(ctx, binPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("任务已取消")
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return res, fmt.Errorf("本地识别失败: %s", msg)
	}
	line := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i] // 实测 stdout 为一行 JSON;多行时取首行容错
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return res, fmt.Errorf("识别输出无法解析: %w", err)
	}
	return res, nil
}
