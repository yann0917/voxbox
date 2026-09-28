package provider

import (
	"encoding/binary"
	"fmt"
	"math"
)

// 纯 Go WAV（RIFF/WAVE）工具：供云端 TTS 分段结果拼接、ASR 超限音频切分使用，
// 不引入 ffmpeg 依赖——WAV 是定长帧的 PCM 容器，解析/重组/按帧切分都可精确完成。
// 非线性格式（mp3 等）的切分不在本文件（走 audiotool 的 ffmpeg 通道）。

// WAV 是解析后的 WAV 文件：参数信息 + 原样 fmt/fact 块（重组时保真）+ data 引用
// （data 直接引用输入缓冲，不拷贝）。
type WAV struct {
	FormatTag     int // 1=PCM，3=IEEE float，0xFFFE=extensible
	Channels      int
	SampleRate    int
	BitsPerSample int

	fmtChunk  []byte // 含 8 字节 chunk 头的完整 fmt 块原样字节
	factChunk []byte // 非 PCM 格式的 fact 块（可空）
	data      []byte // data 块载荷

	frameSize int // Channels * BitsPerSample / 8
}

// ParseWAV 解析 RIFF/WAVE 字节。容忍 fmt 之外的块（LIST/bext 等）；
// data 块声明长度超出实际缓冲时按实际截断（容忍头部虚标的非常规文件）。
func ParseWAV(b []byte) (*WAV, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("不是有效的 WAV 文件（缺少 RIFF/WAVE 头）")
	}
	w := &WAV{}
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		if body+size > len(b) {
			size = len(b) - body // 头部虚标：按实际可用截断
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("WAV fmt 块过短（%d 字节）", size)
			}
			w.fmtChunk = b[pos : body+size]
			w.FormatTag = int(binary.LittleEndian.Uint16(b[body : body+2]))
			w.Channels = int(binary.LittleEndian.Uint16(b[body+2 : body+4]))
			w.SampleRate = int(binary.LittleEndian.Uint32(b[body+4 : body+8]))
			w.BitsPerSample = int(binary.LittleEndian.Uint16(b[body+14 : body+16]))
		case "fact":
			w.factChunk = b[pos : body+size]
		case "data":
			if w.fmtChunk == nil {
				return nil, fmt.Errorf("WAV data 块出现在 fmt 之前")
			}
			w.data = b[body : body+size]
			w.frameSize = w.Channels * w.BitsPerSample / 8
			return w, nil // data 之后一般是尾部填充/元数据，无需再走
		}
		pos = body + size
		if size%2 == 1 {
			pos++ // RIFF 块按 2 字节对齐
		}
	}
	return nil, fmt.Errorf("WAV 缺少 data 块")
}

// Duration 返回音频时长（秒）；参数异常返回 0。
func (w *WAV) Duration() float64 {
	if w.SampleRate <= 0 || w.frameSize <= 0 {
		return 0
	}
	return float64(len(w.data)) / float64(w.SampleRate*w.frameSize)
}

// DataLen 返回 data 块字节数。
func (w *WAV) DataLen() int { return len(w.data) }

// ConcatWAV 拼接多段 WAV 字节为单个 WAV：各段采样参数须一致（同一合成通道
// 的分段天然一致），data 顺序合并、头部只写一次。单段输入原样返回（零拷贝）。
func ConcatWAV(chunks ...[]byte) ([]byte, error) {
	var head *WAV
	datas := make([][]byte, 0, len(chunks))
	for i, chunk := range chunks {
		w, err := ParseWAV(chunk)
		if err != nil {
			return nil, fmt.Errorf("第 %d 段音频解析失败: %w", i+1, err)
		}
		if head == nil {
			head = w
		} else if w.FormatTag != head.FormatTag || w.Channels != head.Channels ||
			w.SampleRate != head.SampleRate || w.BitsPerSample != head.BitsPerSample {
			return nil, fmt.Errorf("第 %d 段音频参数不一致（%dHz/%dch/%dbit/tag%d，首段 %dHz/%dch/%dbit/tag%d）",
				i+1, w.SampleRate, w.Channels, w.BitsPerSample, w.FormatTag,
				head.SampleRate, head.Channels, head.BitsPerSample, head.FormatTag)
		}
		datas = append(datas, w.data)
	}
	if head == nil {
		return nil, fmt.Errorf("没有可拼接的音频段")
	}
	if len(datas) == 1 {
		return chunks[0], nil
	}
	total := 0
	for _, d := range datas {
		total += len(d)
	}
	out := make([]byte, 0, total+len(head.fmtChunk)+len(head.factChunk)+16)
	out = appendWAVHeader(out, head, total)
	for _, d := range datas {
		out = append(out, d...)
	}
	return out, nil
}

// Split 把 WAV 按预算切成若干段完整 WAV 字节：每段时长 ≤maxSec 且 data ≤maxBytes
// （帧对齐双兜底；maxSec/maxBytes ≤0 表示不设该项预算）。quiet 为真时把目标边界
// 吸附到附近 ±1.5s 内最安静的 20ms 区块（减少拦腰切断字词）；长度不超预算时
// 返回重组单段。
func (w *WAV) Split(maxSec float64, maxBytes int, quiet bool) [][]byte {
	if w.frameSize <= 0 || len(w.data) == 0 {
		return [][]byte{nil} // 调用方保证过 ParseWAV；防御性返回不可切段
	}
	maxFrames := int64(len(w.data) / w.frameSize)
	secFrames := int64(0)
	if maxSec > 0 {
		secFrames = int64(maxSec * float64(w.SampleRate))
	}
	byteFrames := int64(0)
	if maxBytes > 0 {
		byteFrames = int64(maxBytes / w.frameSize)
	}
	budget := maxFrames
	if secFrames > 0 && secFrames < budget {
		budget = secFrames
	}
	if byteFrames > 0 && byteFrames < budget {
		budget = byteFrames
	}
	if budget <= 0 || budget >= maxFrames {
		return [][]byte{joinChunks(w.fmtChunk, w.factChunk, w.data)}
	}

	var segs [][]byte
	for start := int64(0); start < maxFrames; {
		end := start + budget
		if end >= maxFrames {
			end = maxFrames
		} else if quiet {
			end = w.quietBoundary(start, end)
		}
		segs = append(segs, joinChunks(w.fmtChunk, w.factChunk, w.data[start*int64(w.frameSize):end*int64(w.frameSize)]))
		start = end
	}
	return segs
}

// joinChunks 以 head 的 fmt/fact 头 + data 组装单个完整 WAV 字节。
func joinChunks(fmtChunk, factChunk, data []byte) []byte {
	out := make([]byte, 0, len(fmtChunk)+len(factChunk)+len(data)+16)
	return append(appendWAVHeader(out, headerOf(fmtChunk, factChunk), len(data)), data...)
}

// headerOf 从原样块字节还原参数（仅用于 Split 重组路径；ConcatWAV 走解析对象）。
func headerOf(fmtChunk, factChunk []byte) *WAV {
	w := &WAV{fmtChunk: fmtChunk, factChunk: factChunk}
	if len(fmtChunk) >= 24 {
		w.FormatTag = int(binary.LittleEndian.Uint16(fmtChunk[8:10]))
		w.Channels = int(binary.LittleEndian.Uint16(fmtChunk[10:12]))
		w.SampleRate = int(binary.LittleEndian.Uint32(fmtChunk[12:16]))
		w.BitsPerSample = int(binary.LittleEndian.Uint16(fmtChunk[22:24]))
	}
	return w
}

// appendWAVHeader 追加 RIFF/WAVE 头（fmt 原样 + fact 如有 + data 声明）到 out。
func appendWAVHeader(out []byte, w *WAV, dataLen int) []byte {
	riffSize := 4 + len(w.fmtChunk) + len(w.factChunk) + 8 + dataLen
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(riffSize))
	out = append(out, "WAVE"...)
	out = append(out, w.fmtChunk...)
	out = append(out, w.factChunk...)
	out = append(out, "data"...)
	return binary.LittleEndian.AppendUint32(out, uint32(dataLen))
}

// quietBlockMs 静音吸附的 RMS 区块粒度（20ms：字词间隙的典型量级）。
const quietBlockMs = 20

// quietSearchSec 边界吸附搜索半径（±1.5s：正常语句间隙密度下的折中）。
const quietSearchSec = 1.5

// quietBoundary 在 [start,end) 的边界候选 end 附近 ±quietSearchSec 内找最安静的
// 20ms 区块，返回其帧起点（夹在 (start,end) 开区间内，保证段非空且不超预算）。
func (w *WAV) quietBoundary(start, end int64) int64 {
	win := int64(quietSearchSec * float64(w.SampleRate))
	lo, hi := end-win, end+win
	if lo < start+1 {
		lo = start + 1
	}
	if hi > end-1 {
		hi = end - 1
	}
	if hi <= lo {
		return end // 搜索空间塌缩：放弃吸附
	}
	block := int64(quietBlockMs) * int64(w.SampleRate) / 1000
	if block < 1 {
		block = 1
	}
	bestAt, bestRMS := int64(-1), math.MaxFloat64
	for at := lo - lo%block + block; at < hi; at += block {
		rms := w.blockRMS(at-block, at)
		if rms < bestRMS {
			bestRMS, bestAt = rms, at
		}
	}
	if bestAt < 0 {
		return end
	}
	return bestAt
}

// blockRMS 计算 [from,to) 帧区间的 RMS（0..1）。异常区间返回 0（视为静音）。
func (w *WAV) blockRMS(from, to int64) float64 {
	if from < 0 {
		from = 0
	}
	if to > int64(len(w.data)/w.frameSize) {
		to = int64(len(w.data) / w.frameSize)
	}
	if to <= from {
		return 0
	}
	var sum float64
	count := 0
	for i := from; i < to; i++ {
		off := i * int64(w.frameSize)
		for ch := 0; ch < w.Channels; ch++ {
			v := sampleAmp(w.data[off:], w.BitsPerSample, w.FormatTag)
			sum += v * v
			count++
			off += int64(w.BitsPerSample / 8)
		}
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

// sampleAmp 解码 offset 处的单个采样为 [-1,1] 幅度（8bit 无符号 / 16-24bit 有符号
// LE / 32bit 按 tag 取整型或 float）。
func sampleAmp(b []byte, bits, tag int) float64 {
	switch bits {
	case 8:
		return (float64(b[0]) - 128) / 128
	case 16:
		return float64(int16(binary.LittleEndian.Uint16(b))) / 32768
	case 24:
		v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if v&0x800000 != 0 {
			v -= 1 << 24
		}
		return float64(v) / 8388608
	case 32:
		if tag == 3 {
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
		}
		return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
	return 0
}
