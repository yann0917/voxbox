package provider

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// buildWAV 构造标准 44 字节头单声道 WAV，samples 为 16bit 有符号采样值。
func buildWAV(t *testing.T, sampleRate int, samples []int16) []byte {
	t.Helper()
	data := make([]byte, 2*len(samples))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(v))
	}
	out := make([]byte, 44+len(data))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(data)))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:], 1) // mono
	binary.LittleEndian.PutUint32(out[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(data)))
	copy(out[44:], data)
	return out
}

func TestParseWAV(t *testing.T) {
	raw := buildWAV(t, 16000, make([]int16, 16000)) // 1s
	w, err := ParseWAV(raw)
	if err != nil {
		t.Fatal(err)
	}
	if w.SampleRate != 16000 || w.Channels != 1 || w.BitsPerSample != 16 || w.FormatTag != 1 {
		t.Errorf("参数不符: %+v", w)
	}
	if d := w.Duration(); math.Abs(d-1) > 1e-9 {
		t.Errorf("Duration = %g, want 1", d)
	}
	if _, err := ParseWAV([]byte("not a wav")); err == nil {
		t.Error("非 WAV 应报错")
	}
}

// 带 LIST 附加块的 WAV 也能解析；重组后 fmt 原样保留。
func TestParseWAVWithExtraChunks(t *testing.T) {
	raw := buildWAV(t, 8000, []int16{100, -100, 200})
	// 在 data 块前插入 LIST 块（8 头 + 4 体）
	withList := append([]byte{}, raw[:36]...)
	withList = append(withList, "LIST"...)
	withList = binary.LittleEndian.AppendUint32(withList, 4)
	withList = append(withList, []byte("INFO")...)
	withList = append(withList, raw[36:]...)
	w, err := ParseWAV(withList)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.data) != 6 {
		t.Errorf("data 长度 = %d, want 6", len(w.data))
	}
}

func TestConcatWAV(t *testing.T) {
	a := buildWAV(t, 16000, []int16{1, 2})
	b := buildWAV(t, 16000, []int16{3, 4, 5})
	got, err := ConcatWAV(a, b)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ParseWAV(got)
	if err != nil {
		t.Fatalf("拼接结果不可解析: %v", err)
	}
	want := []byte{1, 0, 2, 0, 3, 0, 4, 0, 5, 0}
	if !bytes.Equal(w.data, want) {
		t.Errorf("拼接 data = %v, want %v", w.data, want)
	}
	if d := w.Duration(); math.Abs(d-5.0/16000) > 1e-9 {
		t.Errorf("Duration = %g", d)
	}
	// 单段原样返回
	same, err := ConcatWAV(a)
	if err != nil || !bytes.Equal(same, a) {
		t.Errorf("单段拼接应原样返回: %v %v", err, bytes.Equal(same, a))
	}
	// 参数不一致报错
	c := buildWAV(t, 8000, []int16{1})
	if _, err := ConcatWAV(a, c); err == nil {
		t.Error("采样率不一致应报错")
	}
}

func TestWAVSplitByDuration(t *testing.T) {
	raw := buildWAV(t, 8000, make([]int16, 8000*10)) // 10s
	w, _ := ParseWAV(raw)
	segs := w.Split(4, 0, false)
	if len(segs) != 3 { // 4+4+2
		t.Fatalf("段数 = %d, want 3", len(segs))
	}
	var total float64
	for i, s := range segs {
		sw, err := ParseWAV(s)
		if err != nil {
			t.Fatalf("段 %d 不可解析: %v", i, err)
		}
		if sw.SampleRate != 8000 {
			t.Errorf("段 %d 头参数丢失", i)
		}
		total += sw.Duration()
	}
	if math.Abs(total-10) > 1e-6 {
		t.Errorf("总时长 = %g, want 10", total)
	}
}

// 字节预算按帧对齐：段数据均不超预算、无帧被劈开。
func TestWAVSplitByBytes(t *testing.T) {
	raw := buildWAV(t, 8000, make([]int16, 8000)) // 16KB data
	w, _ := ParseWAV(raw)
	segs := w.Split(0, 5000, false) // 5000/2=2500 帧/段
	if len(segs) != 4 {             // 2500*3 + 500
		t.Fatalf("段数 = %d, want 4", len(segs))
	}
	for i, s := range segs {
		sw, _ := ParseWAV(s)
		if len(sw.data) > 5000 {
			t.Errorf("段 %d data = %d 超预算", i, len(sw.data))
		}
		if len(sw.data)%2 != 0 {
			t.Errorf("段 %d 未按帧对齐", i)
		}
	}
}

// 静音吸附：边界应落在两侧静音区而非正弦波中间。
func TestWAVSplitQuietBoundary(t *testing.T) {
	const sr = 8000
	samples := make([]int16, 0, sr*10)
	// 5s 正弦 + 0.6s 静音 + 4.4s 正弦；预算 6s → 原始边界 48000 帧落在静音区内
	for i := 0; i < sr*5; i++ {
		samples = append(samples, int16(20000*math.Sin(2*math.Pi*440*float64(i)/sr)))
	}
	samples = append(samples, make([]int16, sr*3/5)...)
	for i := 0; i < sr*22/5; i++ {
		samples = append(samples, int16(20000*math.Sin(2*math.Pi*440*float64(i)/sr)))
	}
	w, _ := ParseWAV(buildWAV(t, sr, samples))
	segs := w.Split(6, 0, true)
	if len(segs) != 2 {
		t.Fatalf("段数 = %d, want 2", len(segs))
	}
	s1, _ := ParseWAV(segs[0])
	boundary := int64(len(s1.data) / 2)
	// 边界应吸附到静音区 [40000, 44800] 内（预算边界 48000 往回搜 ±1.5s）
	if boundary < 40000 || boundary > 44800 {
		t.Errorf("边界帧 = %d, want 落在静音区 [40000,44800]", boundary)
	}
}

// 24bit 格式同样可解析切分（参数保真）。
func TestWAV24Bit(t *testing.T) {
	frame := []byte{1, 2, 3}
	data := append(append([]byte{}, frame...), frame...)
	out := make([]byte, 0, 60)
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(36+len(data)))
	out = append(out, "WAVEfmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint32(out, 48000)
	out = binary.LittleEndian.AppendUint32(out, 48000*3)
	out = binary.LittleEndian.AppendUint16(out, 3)
	out = binary.LittleEndian.AppendUint16(out, 24)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, data...)

	w, err := ParseWAV(out)
	if err != nil {
		t.Fatal(err)
	}
	if w.BitsPerSample != 24 || w.SampleRate != 48000 {
		t.Errorf("参数不符: %+v", w)
	}
	segs := w.Split(0, 3, false)
	if len(segs) != 2 {
		t.Errorf("段数 = %d, want 2", len(segs))
	}
	if s, err := ParseWAV(segs[0]); err != nil || s.BitsPerSample != 24 {
		t.Errorf("段头丢失 24bit 参数: %v", err)
	}
}
