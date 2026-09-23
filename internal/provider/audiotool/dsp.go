// 音乐调性（key）与 BPM 检测的纯 Go DSP 实现，不依赖 cgo 或外部库。
//
// 管线（22050Hz 单声道 PCM 输入；重采样抗混叠由 ffmpeg swr 的带限 sinc 承担）：
//
//	调性支路（长窗重频率分辨）：Hann 2048 / 跳 512（Δf=10.8Hz）→ 幅度谱
//	  → 各 bin 按「半音带」硬量化到音级（bin 中心频率落在哪个半音带就归谁；
//	  181Hz 以下半音间距小于一个 bin 属分辨率极限，由谐波折叠兜底——低音的
//	  2/3 次谐波落在可分辨区）+ 谐波折叠（平抑相对大小调歧义）→ 逐帧 L2
//	  归一化（响度帧不主导全曲统计）→ chroma 累积 × KK 轮廓 24 组 Pearson。
//	节奏支路（短窗重瞬态分辨）：Hann 1024 / 跳 256（窗 46ms、包络 86fps）
//	  → log(1+γ·|X|) 压缩 → 半波整流谱通量 = onset 包络 → 去均值自相关，
//	  在 40-240 BPM 全区间取峰，倍速/半速歧义由对数高斯先验（120 BPM 为心）
//	  统一裁决——区间放宽后所有倍速/半速峰都在候选集内，无需手工查 lag/2lag。
package audiotool

import (
	"fmt"
	"math"
	"math/cmplx"
)

const (
	// 调性支路：长窗利于频率分辨
	chromaFrameSize = 2048
	chromaHopSize   = 512
	// 节奏支路：短窗短跳利于瞬态分辨与包络时间精度
	fluxFrameSize = 1024
	fluxHopSize   = 256
	// flux 幅度的 log 压缩系数：log(1+γ·m)，抑制低频能量独大
	fluxGamma = 100
	// 调性映射的频率范围（Hz）
	chromaMinHz = 55.0
	chromaMaxHz = 5000.0
	// BPM 候选区间（倍速/半速峰一并入集，由先验裁决）
	minBPM = 40.0
	maxBPM = 240.0
	// 节奏先验：log2 域以 120 BPM 为心、σ=0.9 的高斯（流行乐中位 tempo 附近）
	priorCenterBPM = 120.0
	priorSigma     = 0.9
	// 置信门槛：最优峰自相关须达到包络方差的 15%（正常节奏曲目通常 ≥0.4，
	// 纯音/噪声 <0.1），否则视为无明确节奏而不是硬给伪值
	bpmConfidenceFloor = 0.15
)

// AnalyzeResult 是调与 BPM 分析结果。
type AnalyzeResult struct {
	BPM       float64   `json:"bpm"`                // 每分钟拍数；无法确定时为 0
	BPMAlts   []float64 `json:"bpm_alts,omitempty"` // 备选节奏（按置信度排序，含倍速/半速可能）
	Scale     string    `json:"scale"`              // major / minor
	Key       string    `json:"key"`                // 音名，如 "F#"
	KeyClass  int       `json:"key_class"`          // 音级 0-11（0=C）
	Camelot   string    `json:"camelot"`            // DJ 和谐混音编码，如 "11A"
	KeyScore  float64   `json:"key_score"`          // 最优相关系数 -1..1
	KeyMargin float64   `json:"key_margin"`         // 最优与次优相关系数之差，越大越可信

	Chroma []float64 `json:"chroma,omitempty"` // 归一化 12 维色度向量（0=C 顺序），调试/展示用
}

var keyNames = []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// Camelot 编码表（下标为音级 0=C）：major 8B/9B/10B/11B/12B/1B/2B…，minor 8A/9A/10A/11A/12A/1A/2A…
var (
	camelotMajor = []int{8, 3, 10, 5, 12, 7, 2, 9, 4, 11, 6, 1}
	camelotMinor = []int{5, 12, 7, 2, 9, 4, 11, 6, 1, 8, 3, 10}
)

// krumhanslKessler 心理声学轮廓（Krumhansl & Kessler, 1982），下标为距主音的度数。
var (
	kkMajor = []float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	kkMinor = []float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
)

// analyzeSamples 对单声道 [-1,1] 采样做调与 BPM 分析。
func analyzeSamples(samples []float64, sampleRate int) *AnalyzeResult {
	res := &AnalyzeResult{Scale: "major", KeyClass: 0, Key: "C", Camelot: "8B"}

	frames := chromaFrames(samples, sampleRate)
	if len(frames) < 8 {
		return res // 素材过短（<0.2s）：给默认值，不猜
	}

	res.Chroma = chromaFromFrames(frames, sampleRate)
	keyClass, scale, score, margin := detectKey(res.Chroma)
	res.KeyClass, res.Scale, res.KeyScore, res.KeyMargin = keyClass, scale, score, margin
	res.Key = keyNames[keyClass]
	if scale == "minor" {
		res.Camelot = fmt.Sprintf("%dA", camelotMinor[keyClass])
	} else {
		res.Camelot = fmt.Sprintf("%dB", camelotMajor[keyClass])
	}

	if env, fps := fluxEnvelope(samples, sampleRate); len(env) >= 16 {
		res.BPM, res.BPMAlts = detectBPM(env, fps)
	}
	return res
}

// hann 返回 n 点 Hann 窗。
func hann(n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// chromaFrames 分帧加 Hann 窗做 FFT，返回各帧幅度谱（chromaFrameSize/2+1 bins）。
func chromaFrames(samples []float64, sampleRate int) [][]float64 {
	total := len(samples)
	if total < chromaFrameSize {
		return nil
	}
	window := hann(chromaFrameSize)
	buf := make([]complex128, chromaFrameSize)
	bins := chromaFrameSize/2 + 1
	var frames [][]float64
	for start := 0; start+chromaFrameSize <= total; start += chromaHopSize {
		for i := 0; i < chromaFrameSize; i++ {
			buf[i] = complex(samples[start+i]*window[i], 0)
		}
		fft(buf)
		mag := make([]float64, bins)
		for i := 0; i < bins; i++ {
			mag[i] = cmplx.Abs(buf[i])
		}
		frames = append(frames, mag)
	}
	return frames
}

// fft 原地迭代 radix-2 Cooley-Tukey，长度须为 2 的幂。
func fft(seq []complex128) {
	n := len(seq)
	if n < 2 {
		return
	}
	// 位反转重排
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			seq[i], seq[j] = seq[j], seq[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		angle := -2 * math.Pi / float64(length)
		wStep := complex(math.Cos(angle), math.Sin(angle)) // exp(i·angle)
		for start := 0; start < n; start += length {
			w := complex(1, 0)
			half := length / 2
			for k := start; k < start+half; k++ {
				u := seq[k]
				v := seq[k+half] * w
				seq[k] = u + v
				seq[k+half] = u - v
				w *= wStep
			}
		}
	}
}

// harmWeights 谐波折叠权重（1×/2×/3×/4× 次谐波）：八度=同音级，3 次谐波=上方五度。
// 纯音/频谱稀疏素材只靠基频三四个音级，与大/小调轮廓做相关时相对大小调
// （共享两个音级）极易误判；折叠后五度音级获得额外能量，可显著拉开区分度。
var harmWeights = []float64{1, 0.5, 0.333, 0.25}

// chromaFromFrames 把各帧幅度谱映射成 12 维音级能量后累积：
//
//   - 每个 bin 按半音带硬量化（round(p)）。不用三角插值：STFT 峰谱的能量以
//     真实分音频率为中心对称泄漏，插值按 bin 中心频率劈权重会把本属同一
//     音级的整瓣能量系统性漏给相邻音级（实测纯音三和弦因此判成中音小调）；
//   - 折叠 2/3/4 次谐波（低音区半音间距小于 bin 宽的分辨率极限由此兜底）；
//   - 每帧 L2 归一化后再累加，否则大音量帧主导全曲统计；
//   - 最终峰值归一（展示/调试用）。
func chromaFromFrames(frames [][]float64, sampleRate int) []float64 {
	chroma := make([]float64, 12)
	binHz := float64(sampleRate) / float64(chromaFrameSize)
	minBin := int(chromaMinHz / binHz)
	if minBin < 1 {
		minBin = 1
	}
	maxBin := int(chromaMaxHz / binHz)
	if maxBin > chromaFrameSize/2 {
		maxBin = chromaFrameSize / 2
	}
	for _, mag := range frames {
		var frame [12]float64
		for bin := minBin; bin <= maxBin; bin++ {
			m := math.Sqrt(mag[bin])
			if m == 0 {
				continue
			}
			f0 := float64(bin) * binHz
			for h, w := range harmWeights {
				freq := f0 * float64(h+1)
				if freq > chromaMaxHz {
					break
				}
				p := 69 + 12*math.Log2(freq/440)
				frame[((int(math.Round(p))%12)+12)%12] += w * m
			}
		}
		// 逐帧 L2 归一化
		var norm float64
		for _, v := range frame {
			norm += v * v
		}
		if norm == 0 {
			continue
		}
		inv := 1 / math.Sqrt(norm)
		for i := range frame {
			chroma[i] += frame[i] * inv
		}
	}
	maxV := 0.0
	for _, v := range chroma {
		if v > maxV {
			maxV = v
		}
	}
	if maxV > 0 {
		for i := range chroma {
			chroma[i] /= maxV
		}
	}
	return chroma
}

// detectKey 返回（音级, 音阶, 最优相关系数, 最优与次优之差）。
func detectKey(chroma []float64) (keyClass int, scale string, score, margin float64) {
	best, second := -2.0, -2.0
	bestClass, bestScale := 0, "major"
	for tonic := 0; tonic < 12; tonic++ {
		for mode, profile := range map[string][]float64{"major": kkMajor, "minor": kkMinor} {
			rotated := make([]float64, 12)
			for degree := 0; degree < 12; degree++ {
				rotated[(tonic+degree)%12] = profile[degree]
			}
			r := pearson(chroma, rotated)
			if r > best {
				second = best
				best, bestClass, bestScale = r, tonic, mode
			} else if r > second {
				second = r
			}
		}
	}
	return bestClass, bestScale, best, best - second
}

// pearson 两等长向量的 Pearson 相关系数；任一方差为 0 返回 0。
func pearson(a, b []float64) float64 {
	n := len(a)
	if n == 0 || len(b) != n {
		return 0
	}
	var ma, mb float64
	for i := 0; i < n; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)
	var ab, aa, bb float64
	for i := 0; i < n; i++ {
		da, db := a[i]-ma, b[i]-mb
		ab += da * db
		aa += da * da
		bb += db * db
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return ab / math.Sqrt(aa*bb)
}

// fluxEnvelope 构建 onset 包络：短窗（46ms）短跳（11.6ms）STFT，幅度经
// log(1+γ·m) 压缩后取半波整流谱通量。返回（包络, 包络帧率 fps）。
func fluxEnvelope(samples []float64, sampleRate int) ([]float64, float64) {
	total := len(samples)
	if total < fluxFrameSize {
		return nil, 0
	}
	fps := float64(sampleRate) / float64(fluxHopSize)
	window := hann(fluxFrameSize)
	buf := make([]complex128, fluxFrameSize)
	bins := fluxFrameSize/2 + 1
	prev := make([]float64, bins)
	cur := make([]float64, bins)
	var env []float64
	for start := 0; start+fluxFrameSize <= total; start += fluxHopSize {
		for i := 0; i < fluxFrameSize; i++ {
			buf[i] = complex(samples[start+i]*window[i], 0)
		}
		fft(buf)
		for i := 0; i < bins; i++ {
			cur[i] = math.Log1p(fluxGamma * cmplx.Abs(buf[i]))
		}
		sum := 0.0
		if len(env) > 0 || start > 0 {
			for i := 0; i < bins; i++ {
				if d := cur[i] - prev[i]; d > 0 {
					sum += d
				}
			}
		} else {
			sum = 0 // 首帧无前帧可比
		}
		env = append(env, sum)
		prev, cur = cur, prev
	}
	return env, fps
}

// detectBPM 从 onset 包络自相关中估计节奏，返回（BPM, 备选列表）。
//
// 候选区间 40-240 BPM：脉冲串的 ACF 只在周期整数倍处有峰，因此一首歌的
// 倍速/半速峰都会出现在候选集内，由 log 高斯先验加权排序统一裁决，
// 无需单独的 lag/2lag 检查。
func detectBPM(env []float64, fps float64) (float64, []float64) {
	// 去均值，消除慢变趋势
	var mean float64
	for _, v := range env {
		mean += v
	}
	mean /= float64(len(env))
	var energy float64
	for i := range env {
		env[i] -= mean
		energy += env[i] * env[i]
	}
	if energy == 0 {
		return 0, nil // 恒定信号（静音/纯音）没有节拍概念
	}

	minLag := int(math.Floor(fps * 60.0 / maxBPM))
	maxLag := int(math.Ceil(fps * 60.0 / minBPM))
	if maxLag >= len(env) {
		maxLag = len(env) - 2
	}
	if minLag < 2 || maxLag <= minLag {
		return 0, nil
	}
	acv := autocorr(env, minLag, maxLag)

	// 找局部极大值点（按对数高斯先验加权后的分数排序）
	type peak struct {
		lag  int
		bpm  float64
		acv  float64 // 原始自相关值
		rank float64
	}
	var peaks []peak
	for lag := minLag + 1; lag < maxLag; lag++ {
		if acv(lag) >= acv(lag-1) && acv(lag) > acv(lag+1) {
			bpm := 60.0 * fps / float64(lag)
			rank := acv(lag) * tempoPrior(bpm)
			peaks = append(peaks, peak{lag: lag, bpm: bpm, acv: acv(lag), rank: rank})
		}
	}
	if len(peaks) == 0 {
		return 0, nil
	}
	for i := 1; i < len(peaks); i++ {
		for j := i; j > 0 && peaks[j].rank > peaks[j-1].rank; j-- {
			peaks[j], peaks[j-1] = peaks[j-1], peaks[j]
		}
	}
	best := peaks[0]
	// 置信门槛：最优峰的自相关须达到包络方差的一定比例，否则视为无明确节奏
	if best.acv/(energy/float64(len(env))) < bpmConfidenceFloor {
		return 0, nil
	}
	// 抛物线插值把峰位细化到帧内（整点 lag 在 120 BPM 处量化误差约 ±2.8 BPM）
	if lag := parabolic(acv, best.lag); lag > 0 {
		best.bpm = 60.0 * fps / lag
	}
	bpm := math.Round(best.bpm*10) / 10

	alts := make([]float64, 0, 3)
	seen := map[float64]bool{bpm: true}
	for _, p := range peaks {
		v := math.Round(p.bpm*10) / 10
		if len(alts) >= 2 {
			break
		}
		dup := false
		for s := range seen {
			if math.Abs(s-v) < 1.5 {
				dup = true
				break
			}
		}
		if !dup {
			seen[v] = true
			alts = append(alts, v)
		}
	}
	return bpm, alts
}

// tempoPrior 节奏先验：log2 域高斯。人耳节拍感知是对数式的，同乘除 2 的
// 两个候选感知距离相同，故在对数域施压而非线性域。
func tempoPrior(bpm float64) float64 {
	return math.Exp(-0.5 * math.Pow(math.Log2(bpm/priorCenterBPM)/priorSigma, 2))
}

// autocorr 计算指定滞后区间的自相关，返回取值闭包（插值复用）。
func autocorr(env []float64, minLag, maxLag int) func(lag int) float64 {
	n := len(env)
	values := make([]float64, maxLag+1)
	for lag := minLag; lag <= maxLag; lag++ {
		var sum float64
		for i := 0; i+lag < n; i++ {
			sum += env[i] * env[i+lag]
		}
		values[lag] = sum / float64(n-lag) // 按重叠长度归一，避免长滞后系统性偏小
	}
	return func(lag int) float64 {
		if lag < 0 || lag > maxLag {
			return 0
		}
		return values[lag]
	}
}

// parabolic 对离散峰做三点抛物线插值，返回更精确的峰位（小数滞后）。
func parabolic(f func(int) float64, lag int) float64 {
	y0, y1, y2 := f(lag-1), f(lag), f(lag+1)
	den := y0 - 2*y1 + y2
	if den == 0 {
		return 0
	}
	return float64(lag) + 0.5*(y0-y2)/den
}
