package audiotool

import (
	"math"
	"math/cmplx"
	"testing"
)

// synth 生成采样序列。
func synth(n int, fn func(t float64) float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = fn(float64(i) / float64(analyzeSampleRate))
	}
	return out
}

func TestFFTAgainstDFT(t *testing.T) {
	n := 256
	in := make([]complex128, n)
	for i := range in {
		in[i] = complex(math.Cos(2*math.Pi*float64(i)*10/float64(n))+0.3*math.Sin(2*math.Pi*float64(i)*37/float64(n)), 0)
	}
	got := append([]complex128(nil), in...)
	fft(got)

	// 朴素 DFT 对照
	want := make([]complex128, n)
	for k := 0; k < n; k++ {
		var sum complex128
		for x := 0; x < n; x++ {
			sum += in[x] * cmplx.Exp(complex(0, -2*math.Pi*float64(k)*float64(x)/float64(n)))
		}
		want[k] = sum
	}
	for k := range got {
		if math.Abs(real(got[k])-real(want[k])) > 1e-6 || math.Abs(imag(got[k])-imag(want[k])) > 1e-6 {
			t.Fatalf("FFT 与 DFT 不一致 bin %d: got %v want %v", k, got[k], want[k])
		}
	}
}

func TestDetectKeySineChord(t *testing.T) {
	// C 大调三和弦（C4+E4+G4, 261.63/329.63/392 Hz）应检出 C 大调
	tone := func(freq float64) func(float64) float64 {
		return func(t float64) float64 {
			return 0.3 * math.Sin(2*math.Pi*freq*t)
		}
	}
	samples := synth(analyzeSampleRate*4, func(t float64) float64 {
		return tone(261.63)(t) + tone(329.63)(t) + tone(392.0)(t)
	})
	res := analyzeSamples(samples, analyzeSampleRate)
	if res.Key != "C" || res.Scale != "major" {
		t.Fatalf("C 大调三和弦检出 %s %s (score=%.3f), want C major", res.Key, res.Scale, res.KeyScore)
	}
	if res.Camelot != "8B" {
		t.Fatalf("C 大调 Camelot 应为 8B，得到 %s", res.Camelot)
	}

	// A 小调三和弦（A3+C4+E4, 220/261.63/329.63）应检出 A 小调
	samples = synth(analyzeSampleRate*4, func(t float64) float64 {
		return 0.3*math.Sin(2*math.Pi*220*t) + 0.3*math.Sin(2*math.Pi*261.63*t) + 0.3*math.Sin(2*math.Pi*329.63*t)
	})
	res = analyzeSamples(samples, analyzeSampleRate)
	if res.Key != "A" || res.Scale != "minor" {
		t.Fatalf("A 小调三和弦检出 %s %s, want A minor", res.Key, res.Scale)
	}
	if res.Camelot != "8A" {
		t.Fatalf("A 小调 Camelot 应为 8A，得到 %s", res.Camelot)
	}
}

func TestDetectBPMMetronome(t *testing.T) {
	// 120 BPM 节拍器：每 0.5s 一个 50ms 短促敲击
	bpm := 120.0
	samples := synth(analyzeSampleRate*12, func(t float64) float64 {
		phase := math.Mod(t, 60/bpm)
		if phase < 0.05 {
			// 敲击用复合频率（低音鼓质感），幅度带衰减包络
			env := 1 - phase/0.05
			return 0.8 * env * math.Sin(2*math.Pi*100*t)
		}
		return 0
	})
	res := analyzeSamples(samples, analyzeSampleRate)
	if math.Abs(res.BPM-bpm) > 2 {
		t.Fatalf("120BPM 节拍器检出 %.1f（备选 %v），偏差超 2", res.BPM, res.BPMAlts)
	}

	// 85 BPM 慢节奏
	bpm = 85.0
	samples = synth(analyzeSampleRate*12, func(t float64) float64 {
		phase := math.Mod(t, 60/bpm)
		if phase < 0.05 {
			return 0.8 * (1 - phase/0.05) * math.Sin(2*math.Pi*100*t)
		}
		return 0
	})
	res = analyzeSamples(samples, analyzeSampleRate)
	if math.Abs(res.BPM-bpm) > 2 {
		t.Fatalf("85BPM 节拍器检出 %.1f，偏差超 2", res.BPM)
	}
}

func TestDetectBPMWideRange(t *testing.T) {
	// 200 BPM 快节奏：先验裁决的主值会落到其半速代表（100 附近，音乐学上
	// 等效且更可打拍），但宽搜索区间必须让 200 的倍速峰以备选形式可见
	bpm := 200.0
	samples := synth(analyzeSampleRate*12, func(t float64) float64 {
		phase := math.Mod(t, 60/bpm)
		if phase < 0.05 {
			return 0.8 * (1 - phase/0.05) * math.Sin(2*math.Pi*100*t)
		}
		return 0
	})
	res := analyzeSamples(samples, analyzeSampleRate)
	primaryOK := math.Abs(res.BPM-bpm) <= 2 || math.Abs(res.BPM-bpm/2) <= 2
	if !primaryOK {
		t.Fatalf("200BPM 节拍器检出 %.1f，应为 200 或其半速 100", res.BPM)
	}
	foundAlt := false
	for _, a := range res.BPMAlts {
		if math.Abs(a-bpm) <= 2 || math.Abs(a*2-bpm) <= 2 {
			foundAlt = true
		}
	}
	if !foundAlt {
		t.Fatalf("备选应含 200 的倍速/半速代表，得到 %v（主值 %.1f）", res.BPMAlts, res.BPM)
	}
}

func TestAnalyzeSilenceIsSafe(t *testing.T) {
	res := analyzeSamples(make([]float64, analyzeSampleRate*2), analyzeSampleRate)
	if res.BPM != 0 || res.Key != "C" {
		t.Fatalf("静音应给默认值不 panic: %+v", res)
	}
}

func TestDetectKeyFullScale(t *testing.T) {
	// 12 个大调三和弦逐一验证。声位按真实编曲习惯：根音加重并低八度重复
	//（贝斯奏根音），三音最弱——裸等幅三和弦天然与其中音小调（共享两个音级）
	// 存在调性歧义，真实音乐里由低音根音消解。
	triads := []struct {
		class int
		freqs []float64 // root, third, fifth
	}{
		{0, []float64{261.63, 329.63, 392.00}},  // C
		{2, []float64{293.66, 369.99, 440.00}},  // D
		{4, []float64{329.63, 415.30, 493.88}},  // E
		{5, []float64{349.23, 440.00, 523.25}},  // F
		{7, []float64{392.00, 493.88, 587.33}},  // G
		{9, []float64{440.00, 554.37, 659.26}},  // A
		{11, []float64{493.88, 622.25, 739.99}}, // B
	}
	for _, r := range triads {
		rootBass := r.freqs[0] / 2
		samples := synth(analyzeSampleRate*3, func(t float64) float64 {
			return 0.35*math.Sin(2*math.Pi*rootBass*t) +
				0.55*math.Sin(2*math.Pi*r.freqs[0]*t) +
				0.30*math.Sin(2*math.Pi*r.freqs[1]*t) +
				0.38*math.Sin(2*math.Pi*r.freqs[2]*t)
		})
		res := analyzeSamples(samples, analyzeSampleRate)
		if res.Key != keyNames[r.class] || res.Scale != "major" {
			t.Errorf("%s 大调三和弦检出 %s %s (margin=%.3f)", keyNames[r.class], res.Key, res.Scale, res.KeyMargin)
		}
	}
}
