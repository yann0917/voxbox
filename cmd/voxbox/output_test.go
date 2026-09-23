package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
)

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{fmt.Errorf("缺少必填参数: text"), 2},
		{fmt.Errorf("缺少输入：请提供公网可访问的音视频 URL"), 2},
		{fmt.Errorf("speakers 需要恰好 2 个音色 ID（逗号分隔，voxbox voices list 查询）"), 2},
		{fmt.Errorf("对话稿格式错误：第 1 轮缺少 speaker 或 text"), 2},
		{fmt.Errorf("播客输入只能提供其一（文本/网页/对话稿）"), 2},
		// 长度/载荷超限：契约规定为参数类（2），且上游未推理、不消耗配额
		{fmt.Errorf("翻译载荷超限(45000130): text_list[0] overflow tokens(2361>1024)（单条文本不超过 1024 Tokens、列表不超过 16 条，请分段提交）"), 2},
		{fmt.Errorf("文本超出长度限制：当前 100001 字符，上限 100000（请分段提交）"), 2},
		{fmt.Errorf("仅支持官方 32 种语言代码，target_language=\"xx\" 不在清单内"), 2},
		{fmt.Errorf("%w: xxx", volcengine.ErrNoCred), 4},
		{fmt.Errorf("%w: bad token", volcengine.ErrAuth), 4},
		{fmt.Errorf("%w: 机器翻译资源 volc.speech.mt 未开通", volcengine.ErrNotGranted), 4},
		// 千问凭证缺失走同款哨兵映射（配置类问题，重试无意义）
		{fmt.Errorf("%w：请在设置页「云端服务」配置千问平台凭证，或 voxbox config set qianwen.api_key", qianwen.ErrNoCred), 4},
		{errors.New("火山 TTS 错误 内容审核(50000)"), 3},
	}
	for _, c := range cases {
		if got := exitCodeFor(c.err); got != c.want {
			t.Errorf("exitCodeFor(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestAbsArtifactPath(t *testing.T) {
	dataDir := filepath.Join("some", "data")
	if got := absArtifactPath(dataDir, filepath.Join("tts", "u.mp3")); got != filepath.Join("some", "data", "tts", "u.mp3") {
		t.Errorf("relative path not joined with data dir: %q", got)
	}
	if got := absArtifactPath(dataDir, "/abs/out.mp3"); got != "/abs/out.mp3" {
		t.Errorf("absolute path should be kept: %q", got)
	}
	if got := absArtifactPath(dataDir, ""); got != "" {
		t.Errorf("empty path should stay empty: %q", got)
	}
}
