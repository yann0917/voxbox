package audiotool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// SegmentAudioFile 集成：ffmpeg 生成 5s 正弦 mp3 → 按 2s 流拷贝切分 →
// 3 段、各段可被 ffprobe 探测且时长 ≤2.5s。无 ffmpeg 跳过（与剪辑工具同款可选依赖）。
func TestSegmentAudioFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 未安装，跳过分段集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	src := filepath.Join(t.TempDir(), "in.mp3")
	if err := runFFmpeg(ctx, []string{
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5",
		"-c:a", "libmp3lame", "-b:a", "64k", src,
	}, 5, nil); err != nil {
		t.Fatalf("生成测试 mp3 失败: %v", err)
	}

	dir, parts, err := SegmentAudioFile(ctx, src, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if len(parts) < 3 || len(parts) > 4 { // 帧对齐落刀允许 3-4 段
		t.Errorf("分段数 = %d, want 3-4", len(parts))
	}
	var total float64
	for _, p := range parts {
		info, err := probeAudio(ctx, p)
		if err != nil {
			t.Fatalf("分段 %s 不可探测: %v", p, err)
		}
		if info.Duration > 2.5 {
			t.Errorf("分段 %s 时长 %gs 超预算", p, info.Duration)
		}
		total += info.Duration
	}
	if total < 4.5 || total > 6 {
		t.Errorf("分段总时长 %gs 偏离原音频 5s", total)
	}
}
