package provider

import "testing"

// TestSourceBase 源基名解析：uuid 前缀剥离、音乐档位后缀剥离、非法字符净化。
func TestSourceBase(t *testing.T) {
	cases := []struct{ path, want string }{
		// Web 上传通道：<uuid>-<原名>.<ext> → 原名
		{"/data/uploads/2a3b4c5d-1111-2222-3333-444455556666-我的歌.mp3", "我的歌"},
		// 音乐下载命名：<歌名 - 歌手>.<档位>.<ext> → 档位后缀剥掉（产物不带 .standard）
		{"/data/music/稻香 - 周杰伦.standard.mp3", "稻香 - 周杰伦"},
		{"/data/music/稻香 - 周杰伦.exhigh.flac", "稻香 - 周杰伦"},
		{"/data/music/稻香 - 周杰伦.atmosplus.flac", "稻香 - 周杰伦"}, // levelTag 归一：atmos_plus → .atmosplus
		// 非档位点段不剥：年份数字、普通词
		{"/data/music/ demo 2024.mp3", "demo 2024"},
		{"/tmp/demo.recording.wav", "demo.recording"},
		// 整段就是档位词的裸名不剥（剥完为空会回落 audio，不如保留原词）
		{"/tmp/standard.mp3", "standard"},
		// 普通路径原样净化
		{"/tmp/input.mp3", "input"},
	}
	for _, c := range cases {
		if got := SourceBase(c.path); got != c.want {
			t.Errorf("SourceBase(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
