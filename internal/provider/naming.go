// 产物/输入文件命名辅助：各引擎共享「源文件基名」的解析规则——
// Web 上传通道落盘为 <uuid>-<净化原名>.<ext>，此处剥掉 uuid 前缀还原可读名，
// 让分离/转换产物命名带歌名或原文件名，而不是 uuid。
package provider

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var uploadIDPrefix = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-`)

// musicLevelTags 音乐档位后缀词表（service 旧 levelTag 归一形态：小写字母数字）。
// 音乐下载文件名曾带档位（<歌名 - 歌手>.<档位>.<ext>，2026-09-19 起已去掉），数据目录
// 里的历史文件仍在：分离/转换产物不继承这个内部标记 —— SourceBase 在末段整段恰好是
// 档位词时剥掉。词表 = 音源配置 music.json 的 levels 全集（2026-09-19 快照），上游
// 新增档位时在此追加。
var musicLevelTags = map[string]bool{
	"standard": true, "exhigh": true, "lossless": true, "hires": true, "master": true,
	"jymaster": true, "jyeffect": true, "atmos": true, "atmosplus": true,
	"bili192": true, "clear": true, "sky": true,
}

// trimMusicLevelTag 剥掉音乐下载命名追加的档位后缀（<基名>.<档位> → <基名>）。
func trimMusicLevelTag(base string) string {
	i := strings.LastIndexByte(base, '.')
	if i <= 0 {
		return base
	}
	if musicLevelTags[base[i+1:]] {
		return base[:i]
	}
	return base
}

// SourceBase 从输入文件路径解析可读的源基名（不含扩展名）：
// 剥掉 Web 上传的 uuid 前缀与音乐档位后缀，再净化为安全落盘名（保留中文等 Unicode 字母）。
func SourceBase(path string) string {
	base := filepath.Base(path)
	base = uploadIDPrefix.ReplaceAllString(base, "")
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = trimMusicLevelTag(base)
	var b strings.Builder
	for _, r := range base {
		keep := r == '.' || r == '-' || r == '_' || r == ' ' || r == '(' || r == ')' ||
			unicode.IsLetter(r) || unicode.IsNumber(r)
		if !keep {
			r = '_'
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), " ._")
	if out == "" {
		out = "audio"
	}
	return out
}
