package qingmusic

// 音频标签写入：ID3v2.3（mp3）与 Vorbis Comment（flac），纯标准库实现，无第三方依赖。
//
// 为什么手写而不是引库：App 只做四件事——写 title/singer/albumName、写封面、
// 写歌词、其余格式跳过。这套需求用两种最简格式各 ~150 行就能覆盖，且省掉
// 三个外部模块（bogem/id3v2、go-flac/flacvorbis、go-flac/flacpicture）。
//
// 所有写操作都走「写临时文件 → 原子改名」，保证标签写坏时不会毁掉刚下好的音频。

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// TagMeta 要写进文件的元数据。
type TagMeta struct {
	Title  string
	Artist string
	Album  string
	Lyrics string

	CoverData []byte
	CoverMIME string
}

// WriteTags 按**真实容器**分发写标签。
//
// 为什么要嗅探而不能只看扩展名：App 的扩展名推断只匹配 URL 字面量，
// 实测酷我 lossless 给的是 `.../trackmedia/xxx.m4a?bitrate$48&format$aac`，
// 三个条件都不命中 → 存成 .mp3。此时若按扩展名走 ID3，会往 m4a 前面塞一个
// ID3 头，播放器按 ftyp 找盒子就找不到，文件直接废掉。
// App 侧不会出这个问题是因为原生库自己嗅探容器、嗅到 m4a 就抛错被 catch 掉。
//
// 返回的 skipped 非空表示**按 App 行为跳过**（不是错误），调用方应据此打一条
// 「XX 文件跳过元数据写入」并保留音频文件。
func WriteTags(path string, meta TagMeta) (skipped string, err error) {
	c := SniffContainer(path)
	if c == ContainerUnknown {
		// 嗅不出来（极短/异常文件）才退回扩展名。
		c = Container(strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")))
	}
	switch c {
	case ContainerMP3:
		return "", writeID3v2(path, meta)
	case ContainerFLAC:
		return "", writeFLACTags(path, meta)
	case ContainerM4A, ContainerAAC:
		// 字符串表 12326 的 FAQ 原文：目前仅支持 mp3 和 flac 格式的音频嵌入
		// 歌词和封面。App 对这两种容器直接跳过。
		return "AAC/M4A 文件跳过元数据写入", nil
	default:
		return fmt.Sprintf("%s 文件不支持标签写入，已跳过", filepath.Ext(path)), nil
	}
}

// Container 真实音频容器。
type Container string

const (
	ContainerMP3     Container = "mp3"
	ContainerFLAC    Container = "flac"
	ContainerM4A     Container = "m4a"
	ContainerAAC     Container = "aac"
	ContainerUnknown Container = ""
)

// SniffContainer 读文件头判断真实容器。判断不出返回 ContainerUnknown。
func SniffContainer(path string) Container {
	f, err := os.Open(path)
	if err != nil {
		return ContainerUnknown
	}
	defer f.Close()

	var b [64]byte
	n, _ := io.ReadFull(f, b[:])
	if n < 8 {
		return ContainerUnknown
	}
	head := b[:n]

	switch {
	case string(head[:4]) == "fLaC":
		return ContainerFLAC
	case string(head[:3]) == "ID3":
		return ContainerMP3
	case indexOf(head[:32], []byte("ftyp")) >= 0:
		// m4a/mp4：正常是 b[4:8]=="ftyp"，带 free/wide 前置盒时靠搜索兜底。
		return ContainerM4A
	case head[0] == 0xFF && (head[1] == 0xF1 || head[1] == 0xF9):
		return ContainerAAC // ADTS
	case head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return ContainerMP3 // MPEG 帧同步
	}
	return ContainerUnknown
}

func indexOf(haystack, needle []byte) int {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------- mp3 / ID3v2.3

// writeID3v2 在文件头部写入 ID3v2.3 标签。
//
// 用 2.3 而不是 2.4：2.3 + UTF-16 的兼容面最广（iTunes/Windows/Android 都吃），
// 2.4 的 UTF-8 支持反而是一些老播放器的坑。
//
// 旧标签里我们没覆盖的帧会被保留（App 是 readMetadata → Object.assign →
// writeMetadata，同样是合并语义）——实测咪咕的 mp3 自带 TLEN / encoder 帧，
// 直接整段丢掉会平白少信息。
func writeID3v2(path string, meta TagMeta) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// 我们要写的帧（决定旧标签里哪些要丢）。
	drop := map[string]bool{
		"TIT2": strings.TrimSpace(meta.Title) != "",
		"TPE1": strings.TrimSpace(meta.Artist) != "",
		"TALB": strings.TrimSpace(meta.Album) != "",
		"APIC": len(meta.CoverData) > 0,
		"USLT": strings.TrimSpace(meta.Lyrics) != "",
	}
	audio, kept := splitID3(data, drop)

	var frames []byte
	addText := func(id, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		frames = append(frames, id3Frame(id, id3TextPayload(value))...)
	}
	addText("TIT2", meta.Title)
	addText("TPE1", meta.Artist)
	addText("TALB", meta.Album)

	if len(meta.CoverData) > 0 {
		frames = append(frames, id3Frame("APIC", id3APICPayload(meta))...)
	}
	if strings.TrimSpace(meta.Lyrics) != "" {
		frames = append(frames, id3Frame("USLT", id3USLTPayload(meta.Lyrics))...)
	}
	frames = append(frames, kept...)
	if len(frames) == 0 {
		return nil
	}

	tag := make([]byte, 0, 10+len(frames))
	tag = append(tag, 'I', 'D', '3', 0x03, 0x00, 0x00)
	tag = append(tag, syncsafeEncode(len(frames))...)
	tag = append(tag, frames...)

	return atomicRewrite(path, append(tag, audio...))
}

// splitID3 剥掉文件开头的 ID3v2 标签，返回音频部分与「可保留的旧帧」。
//
// 只在旧标签确实是 ID3v2.3 且未置 unsynchronisation 位时才解析帧；
// 其它情况（v2.2/v2.4/带 unsync）一律整段丢弃 —— 版本混写的标签比丢几帧更糟。
func splitID3(data []byte, drop map[string]bool) (audio, keep []byte) {
	if len(data) < 10 || string(data[:3]) != "ID3" {
		return data, nil
	}
	body := data[10:]
	if size := int(syncsafeDecode(data[6:10])); size <= len(body) {
		body = body[:size]
	} else {
		return data, nil
	}
	audio = data[10+len(body):]

	major, flags := data[3], data[5]
	if major != 3 || flags&0x80 != 0 {
		return audio, nil
	}

	for i := 0; i+10 <= len(body); {
		id := string(body[i : i+4])
		if !isFrameID(id) {
			break // 填充或已损坏，后面不再解析
		}
		n := int(binary.BigEndian.Uint32(body[i+4 : i+8]))
		if n < 0 || i+10+n > len(body) {
			break
		}
		raw := body[i : i+10+n]
		if !drop[id] {
			keep = append(keep, raw...)
		}
		i += 10 + n
	}
	return audio, keep
}

// isFrameID 4 位大写字母或数字。
func isFrameID(id string) bool {
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// id3Frame 组一个帧：id(4) + size(4, 大端) + flags(2) + data。
func id3Frame(id string, payload []byte) []byte {
	out := make([]byte, 0, 10+len(payload))
	out = append(out, id...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, 0x00, 0x00)
	return append(out, payload...)
}

// id3TextPayload 编码字节 0x01（UTF-16 + BOM）后接文本，末尾补终止符。
func id3TextPayload(s string) []byte {
	out := []byte{0x01}
	out = append(out, 0xFF, 0xFE) // little-endian BOM
	out = append(out, utf16LE(s)...)
	return append(out, 0x00, 0x00)
}

// id3APICPayload 封面帧：编码 + MIME + 0x00 + 图片类型 + 描述 + 0x00 + 图片数据。
// 图片类型 0x03 = front cover。
func id3APICPayload(meta TagMeta) []byte {
	mime := meta.CoverMIME
	if mime == "" {
		mime = "image/jpeg"
	}
	out := []byte{0x00} // ISO-8859-1，MIME/描述都是 ASCII，省一半字节
	out = append(out, mime...)
	out = append(out, 0x00)
	out = append(out, 0x03)
	out = append(out, 0x00) // 空描述
	return append(out, meta.CoverData...)
}

// id3USLTPayload 歌词帧：编码 + 3 字节语言 + 描述 + 终止符 + 正文。
func id3USLTPayload(lyrics string) []byte {
	out := []byte{0x01}
	out = append(out, 'e', 'n', 'g')
	out = append(out, 0xFF, 0xFE, 0x00, 0x00) // 空描述（UTF-16 BOM + 终止符）
	out = append(out, 0xFF, 0xFE)             // 正文自己的 BOM
	return append(out, utf16LE(lyrics)...)
}

func syncsafeEncode(n int) []byte {
	return []byte{
		byte((n >> 21) & 0x7F),
		byte((n >> 14) & 0x7F),
		byte((n >> 7) & 0x7F),
		byte(n & 0x7F),
	}
}

func syncsafeDecode(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return uint32(b[0]&0x7F)<<21 | uint32(b[1]&0x7F)<<14 | uint32(b[2]&0x7F)<<7 | uint32(b[3]&0x7F)
}

// utf16LE 转 UTF-16 小端字节（含代理对处理，emoji 不会写坏）。
func utf16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(u)*2)
	for _, c := range u {
		out = binary.LittleEndian.AppendUint16(out, c)
	}
	return out
}

// atomicRewrite 先写 <path>.tagtmp 再改名，避免中途失败留下半个文件。
func atomicRewrite(path string, data []byte) error {
	tmp := path + ".tagtmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
