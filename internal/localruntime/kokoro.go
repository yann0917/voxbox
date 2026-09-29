package localruntime

// Kokoro TTS:sherpa-onnx-offline-tts 一次性子进程(kokoro 家族专属,与 audiocpp
// 常驻 server 互不影响)。v1.13.8 真机实测参数面:--kokoro-model/voices/tokens/
// data-dir/lexicon + --tts-rule-fsts;dict 目录该版本不消费(中文走 lexicon),不解包。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yann0917/voxbox/internal/localmodel"
)

// kokoroTTSBinary kokoro 家族消费的引擎 CLI 名(sherpa 引擎发布包自带,非 Binaries[0])。
const kokoroTTSBinary = "sherpa-onnx-offline-tts"

// SynthesizeKokoro 一次性子进程合成:sid 选内置音色,输出固定 24kHz 单声道 pcm16
// (与音色库管线一致)。启动前校验模型文件在位,失败取 stderr 末行,产物未落地视为失败。
func SynthesizeKokoro(ctx context.Context, binPath, modelDir, outPath, text string, sid int) error {
	model := filepath.Join(modelDir, "model.onnx")
	voices := filepath.Join(modelDir, "voices.bin")
	tokens := filepath.Join(modelDir, "tokens.txt")
	espeakData := filepath.Join(modelDir, "espeak-ng-data")
	for _, p := range []string{model, voices, tokens, espeakData} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("输入缺失: %s", p)
		}
	}
	lexicon := filepath.Join(modelDir, "lexicon-us-en.txt") + "," + filepath.Join(modelDir, "lexicon-zh.txt")
	ruleFsts := strings.Join([]string{
		filepath.Join(modelDir, "date-zh.fst"),
		filepath.Join(modelDir, "phone-zh.fst"),
		filepath.Join(modelDir, "number-zh.fst"),
	}, ",")
	args := []string{
		"--kokoro-model=" + model,
		"--kokoro-voices=" + voices,
		"--kokoro-tokens=" + tokens,
		"--kokoro-data-dir=" + espeakData,
		"--kokoro-lexicon=" + lexicon,
		"--tts-rule-fsts=" + ruleFsts,
		"--sid=" + strconv.Itoa(sid),
		"--num-threads=2",
		"--output-filename=" + outPath,
		text,
	}
	cmd := exec.CommandContext(ctx, binPath, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("任务已取消")
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("本地合成失败: %s", msg)
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("合成产物缺失: %s", outPath)
	}
	return nil
}

// KokoroTTS kokoro 家族的合成运行时:复用已安装的 sherpa-onnx 引擎,一次性子进程,
// 无常驻进程;产物落 <dataDir>/tts/local_*.wav(与 audiocpp 产物同目录同命名)。
type KokoroTTS struct {
	dataDir string
	models  *localmodel.Manager
}

func NewKokoroTTS(dataDir string, models *localmodel.Manager) *KokoroTTS {
	return &KokoroTTS{dataDir: dataDir, models: models}
}

// Synthesize 预置音色合成:引擎按条目 requires_engine 解析,CLI 在引擎目录内按名定位
// (旧安装未重装同样命中),模型按目录整体消费。
func (k *KokoroTTS) Synthesize(ctx context.Context, req SynthRequest, report func(p int, note string)) (string, error) {
	if k.models == nil {
		return "", fmt.Errorf("本地模型管理器未初始化")
	}
	e, ok := k.models.GetEntry(req.ModelID)
	if !ok {
		return "", fmt.Errorf("%w: %s", localmodel.ErrUnknownModel, req.ModelID)
	}
	report(5, "检查本地合成引擎…")
	bin, err := k.models.EngineBinaryNamed(e.RequiresEngine, kokoroTTSBinary)
	if err != nil {
		return "", err
	}
	dir, err := k.models.InstalledModelDir(req.ModelID)
	if err != nil {
		return "", err
	}
	outDir := filepath.Join(k.dataDir, "tts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, fmt.Sprintf("local_%d.wav", time.Now().UnixNano()))
	report(15, "本地合成中…")
	if err := SynthesizeKokoro(ctx, bin, dir, out, req.Text, req.SpeakerSID); err != nil {
		return "", err
	}
	report(100, "本地合成完成")
	return out, nil
}
