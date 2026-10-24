package qingmusic

// FLAC 标签写入：重写 VORBIS_COMMENT(4) 与 PICTURE(6) 两个元数据块。
//
// FLAC 的元数据块不能就地扩容，所以只能整文件重写：
//
//	"fLaC" + [STREAMINFO] + [VORBIS_COMMENT] + [PICTURE] + [其余原块] + 音频帧
//
// STREAMINFO 必须排第一（规范要求），因此原样保留；原有的 4/6 块整块替换，
// PADDING 之类原样留着，避免把文件结构改得和原文件差异过大。

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

const (
	flacBlockStreamInfo = 0
	flacBlockVorbis     = 4
	flacBlockPicture    = 6

	flacVendor = "voxbox"
)

type flacBlock struct {
	typ  byte
	data []byte
}

func writeFLACTags(path string, meta TagMeta) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) < 8 || string(raw[:4]) != "fLaC" {
		return fmt.Errorf("不是合法的 FLAC 文件（缺少 fLaC 标识）")
	}

	blocks, audio, err := parseFLACBlocks(raw[4:])
	if err != nil {
		return err
	}

	// 新的注释块与图片块，插在 STREAMINFO 之后。
	fresh := make([]flacBlock, 0, 2)
	if c := buildVorbisComment(meta); len(c) > 0 {
		fresh = append(fresh, flacBlock{flacBlockVorbis, c})
	}
	if len(meta.CoverData) > 0 {
		fresh = append(fresh, flacBlock{flacBlockPicture, buildFLACPicture(meta)})
	}

	kept := make([]flacBlock, 0, len(blocks)+len(fresh))
	for i, b := range blocks {
		if b.typ == flacBlockVorbis || b.typ == flacBlockPicture {
			continue // 旧的整块丢掉
		}
		kept = append(kept, b)
		if i == 0 { // STREAMINFO 固定在首位，紧跟着插新块
			kept = append(kept, fresh...)
		}
	}
	if len(blocks) == 0 {
		kept = append(kept, fresh...)
	}

	out := make([]byte, 0, len(raw)+256)
	out = append(out, 'f', 'L', 'a', 'C')
	for i, b := range kept {
		var hdr [4]byte
		hdr[0] = b.typ & 0x7F
		if i == len(kept)-1 {
			hdr[0] |= 0x80 // 最后一块置位，其后即音频帧
		}
		putBE24(hdr[1:], len(b.data))
		out = append(out, hdr[:]...)
		out = append(out, b.data...)
	}
	out = append(out, audio...)

	return atomicRewrite(path, out)
}

// parseFLACBlocks 拆出元数据块与音频起始位置。输入是去掉 "fLaC" 之后的字节。
func parseFLACBlocks(b []byte) ([]flacBlock, []byte, error) {
	var (
		blocks []flacBlock
		off    int
	)
	for {
		if off+4 > len(b) {
			return nil, nil, fmt.Errorf("FLAC 元数据块头被截断（偏移 %d）", off)
		}
		h := b[off]
		last := h&0x80 != 0
		typ := h & 0x7F
		n := int(b[off+1])<<16 | int(b[off+2])<<8 | int(b[off+3])
		off += 4
		if off+n > len(b) {
			return nil, nil, fmt.Errorf("FLAC 元数据块长度越界（type=%d len=%d）", typ, n)
		}
		blocks = append(blocks, flacBlock{typ: typ, data: b[off : off+n]})
		off += n
		if last {
			break
		}
	}
	return blocks, b[off:], nil
}

// buildVorbisComment 组 VORBIS_COMMENT 块数据。
//
// 布局（全小端）：vendorLen + vendor + count + [len + "KEY=value"]...
func buildVorbisComment(meta TagMeta) []byte {
	entries := make([]string, 0, 4)
	add := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			entries = append(entries, k+"="+v)
		}
	}
	add("TITLE", meta.Title)
	add("ARTIST", meta.Artist)
	add("ALBUM", meta.Album)
	// 歌词按 Vorbis 约定可放 LYRICS。
	add("LYRICS", meta.Lyrics)

	if len(entries) == 0 {
		return nil
	}

	var out []byte
	out = binary.LittleEndian.AppendUint32(out, uint32(len(flacVendor)))
	out = append(out, flacVendor...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(entries)))
	for _, e := range entries {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(e)))
		out = append(out, e...)
	}
	return out
}

// buildFLACPicture 组 PICTURE 块数据（全大端）。
//
//	类型(3=front cover) + MIME + 描述 + 宽高/色深/色数 + 图片数据
//
// 宽高从图片本身解析；解析不出来就写 0，播放器普遍能容忍。
func buildFLACPicture(meta TagMeta) []byte {
	mime := meta.CoverMIME
	if mime == "" {
		mime = "image/jpeg"
	}
	desc := ""
	w, h, depth, colors := imageDimensions(meta.CoverData, mime)

	var out []byte
	out = binary.BigEndian.AppendUint32(out, 3)
	out = binary.BigEndian.AppendUint32(out, uint32(len(mime)))
	out = append(out, mime...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(desc)))
	out = append(out, desc...)
	out = binary.BigEndian.AppendUint32(out, uint32(w))
	out = binary.BigEndian.AppendUint32(out, uint32(h))
	out = binary.BigEndian.AppendUint32(out, uint32(depth))
	out = binary.BigEndian.AppendUint32(out, uint32(colors))
	out = binary.BigEndian.AppendUint32(out, uint32(len(meta.CoverData)))
	return append(out, meta.CoverData...)
}

func putBE24(b []byte, n int) {
	b[0] = byte(n >> 16)
	b[1] = byte(n >> 8)
	b[2] = byte(n)
}

// imageDimensions 从 PNG/JPEG 头部取宽高。返回 0,0,0,0 表示没解析出来。
func imageDimensions(data []byte, mime string) (w, h, depth, colors int) {
	if len(data) >= 24 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		w = int(binary.BigEndian.Uint32(data[16:20]))
		h = int(binary.BigEndian.Uint32(data[20:24]))
		return w, h, 0, 0
	}
	if strings.HasPrefix(mime, "image/jpeg") || (len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8) {
		w, h = jpegDimensions(data)
		return w, h, 0, 0
	}
	return 0, 0, 0, 0
}

// jpegDimensions 扫 JPEG 段找 SOFn 帧头。
func jpegDimensions(d []byte) (w, h int) {
	i := 2 // 跳过 SOI
	for i+3 < len(d) {
		if d[i] != 0xFF {
			i++
			continue
		}
		marker := d[i+1]
		// 填充字节与无长度字段的标记
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		if i+3 >= len(d) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(d[i+2 : i+4]))
		if segLen < 2 {
			break
		}
		// SOF0..SOF15，排除 DHT(C4) / JPG(C8) / DAC(CC)
		if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
			if i+9 > len(d) {
				break
			}
			h = int(binary.BigEndian.Uint16(d[i+5 : i+7]))
			w = int(binary.BigEndian.Uint16(d[i+7 : i+9]))
			return w, h
		}
		i += 2 + segLen
	}
	return 0, 0
}
