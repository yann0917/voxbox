package audiotool

import (
	"strings"
	"testing"
)

func TestBuildTrimArgs(t *testing.T) {
	ext, codec, bitrate := resolveOutCodec("auto", "a.mp3", "192k")
	spec := struct{}{}
	_ = spec

	// 保留选区：输出侧定位 + 重编码
	args, err := buildTrimArgs(trimParams{Start: 12.5, End: 30}, 60, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-ss 12.5") || !strings.Contains(got, "-t 17.5") {
		t.Fatalf("trim args 缺定位参数: %s", got)
	}
	if !strings.Contains(got, "-c:a libmp3lame") || !strings.Contains(got, "-b:a 192k") {
		t.Fatalf("trim args 缺编码参数: %s", got)
	}

	// 挖除选区：filter_complex 双 atrim + concat，且 asetpts 必须在（时间戳归零）
	args, err = buildTrimArgs(trimParams{Start: 10, End: 20, Cutout: true}, 60, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("cutout: %v", err)
	}
	got = strings.Join(args, " ")
	for _, want := range []string{"atrim=start=0:end=10", "atrim=start=20", "asetpts=N/SR/TB", "concat=n=2:v=0:a=1", "-map [out]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cutout args 缺 %q: %s", want, got)
		}
	}

	// 参数非法
	if _, err := buildTrimArgs(trimParams{Start: -1}, 60, ext, codec, bitrate); err == nil {
		t.Fatal("负起点应报错")
	}
	if _, err := buildTrimArgs(trimParams{Start: 70}, 60, ext, codec, bitrate); err == nil {
		t.Fatal("起点超时长应报错")
	}
	if _, err := buildTrimArgs(trimParams{Start: 20, End: 10}, 60, ext, codec, bitrate); err == nil {
		t.Fatal("终点小于起点应报错")
	}
	if _, err := buildTrimArgs(trimParams{Start: 10, Cutout: true}, 60, ext, codec, bitrate); err == nil {
		t.Fatal("挖除缺终点应报错")
	}

	// 到结尾：不给 -t
	args, _ = buildTrimArgs(trimParams{Start: 5}, 60, ext, codec, bitrate)
	if strings.Contains(strings.Join(args, " "), "-t ") {
		t.Fatalf("到结尾不应有 -t: %s", strings.Join(args, " "))
	}
}

func TestBuildMergeArgs(t *testing.T) {
	ext, codec, bitrate := resolveOutCodec("", "a.mp3", "")

	args, err := buildMergeArgs(3, mergeParams{Crossfade: 0}, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "concat=n=3:v=0:a=1") {
		t.Fatalf("concat args 错: %s", got)
	}
	// 异源统一：三路都应有 aformat
	if strings.Count(got, "aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo") != 3 {
		t.Fatalf("concat 未统一三路输入: %s", got)
	}

	// 交叉淡化：acrossfade 链
	args, err = buildMergeArgs(3, mergeParams{Crossfade: 2}, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("crossfade: %v", err)
	}
	got = strings.Join(args, " ")
	wantFC := "[0:a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo[a0];" +
		"[1:a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo[a1];" +
		"[2:a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo[a2];" +
		"[a0][a1]acrossfade=d=2:c1=tri:c2=tri[x1];[x1][a2]acrossfade=d=2:c1=tri:c2=tri[x2]"
	if !strings.Contains(got, wantFC) {
		t.Fatalf("crossfade 链错误（注意各段间的分号）:\n got: %s\nwant: %s", got, wantFC)
	}

	if _, err := buildMergeArgs(1, mergeParams{}, ext, codec, bitrate); err == nil {
		t.Fatal("单文件合并应报错")
	}
	if _, err := buildMergeArgs(2, mergeParams{Crossfade: -1}, ext, codec, bitrate); err == nil {
		t.Fatal("负淡化时长应报错")
	}
}

func TestBuildPitchArgs(t *testing.T) {
	ext, codec, bitrate := resolveOutCodec("mp3", "a.wav", "192k")

	// 只变调 +1 半音（44100Hz → asetrate 46717 反算补偿，时长不变）
	args, err := buildPitchArgs(pitchParams{Semitones: 1, Tempo: 1}, 44100, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("semitones=1: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "asetrate=46722,aresample=44100") {
		t.Fatalf("asetrate 链错误: %s", got)
	}
	if !strings.Contains(got, "atempo=") {
		t.Fatalf("缺速度补偿: %s", got)
	}

	// 变调 + 变速：补偿与目标倍率折叠进同一条 atempo 链
	args, err = buildPitchArgs(pitchParams{Semitones: 3, Tempo: 1.25}, 44100, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("both: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "atempo=") {
		t.Fatal("变调变速应共用 atempo 链")
	}

	// 只变速 0.5x
	args, err = buildPitchArgs(pitchParams{Semitones: 0, Tempo: 0.5}, 44100, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("tempo only: %v", err)
	}
	got = strings.Join(args, " ")
	if !strings.Contains(got, "atempo=0.5") || strings.Contains(got, "asetrate") {
		t.Fatalf("纯变速不应有 asetrate: %s", got)
	}

	// 边界
	if _, err := buildPitchArgs(pitchParams{Semitones: 13}, 44100, ext, codec, bitrate); err == nil {
		t.Fatal("超 12 半音应报错")
	}
	if _, err := buildPitchArgs(pitchParams{Tempo: 5}, 44100, ext, codec, bitrate); err == nil {
		t.Fatal("超 4 倍速应报错")
	}
	if _, err := buildPitchArgs(pitchParams{}, 44100, ext, codec, bitrate); err == nil {
		t.Fatal("全默认应报错（无需处理）")
	}
	if _, err := buildPitchArgs(pitchParams{Semitones: 1}, 0, ext, codec, bitrate); err == nil {
		t.Fatal("采样率缺失应报错")
	}
}

func TestAtempoChain(t *testing.T) {
	cases := []struct {
		ratio float64
		want  []string // 每级都在 [0.5, 2.0] 且乘积≈ratio
	}{
		{1.0, []string{"atempo=1"}},
		{0.5, []string{"atempo=0.5"}},
		{2.0, []string{"atempo=2"}},
		{4.0, []string{"atempo=2", "atempo=2"}},
		{0.25, []string{"atempo=0.5", "atempo=0.5"}},
		{3.0, []string{"atempo=2", "atempo=1.5"}},
	}
	for _, c := range cases {
		got, err := atempoChain(c.ratio)
		if err != nil {
			t.Fatalf("atempoChain(%g): %v", c.ratio, err)
		}
		if got != strings.Join(c.want, ",") {
			t.Errorf("atempoChain(%g) = %q, want %v", c.ratio, got, c.want)
		}
	}
	if _, err := atempoChain(0); err == nil {
		t.Fatal("零倍率应报错")
	}
}

func TestResolveEqGainsAndChain(t *testing.T) {
	// 预设
	gains, err := resolveEqGains(eqParams{Preset: "vocal"})
	if err != nil {
		t.Fatalf("preset: %v", err)
	}
	if len(gains) != 10 || gains[6] != 4 {
		t.Fatalf("vocal 预设错误: %v", gains)
	}

	// 自定义 CSV 覆盖预设
	gains, err = resolveEqGains(eqParams{Preset: "bass", Gains: "1,2,3,4,5,6,7,8,9,10"})
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if gains[9] != 10 {
		t.Fatalf("CSV 应覆盖预设: %v", gains)
	}

	// 非法
	if _, err := resolveEqGains(eqParams{Gains: "1,2,3"}); err == nil {
		t.Fatal("段数错误应报错")
	}
	if _, err := resolveEqGains(eqParams{Gains: "1,2,3,4,5,6,7,8,9,x"}); err == nil {
		t.Fatal("非数字应报错")
	}
	if _, err := resolveEqGains(eqParams{Gains: "13,0,0,0,0,0,0,0,0,0"}); err == nil {
		t.Fatal("超 ±12dB 应报错")
	}
	if _, err := resolveEqGains(eqParams{Preset: "nope"}); err == nil {
		t.Fatal("未知预设应报错")
	}

	// 全 0 → anull；部分 0 → 跳过零段
	if got := buildEqGains(make([]float64, 10)); got != "anull" {
		t.Fatalf("全 0 应退化 anull，得到 %q", got)
	}
	got := buildEqGains([]float64{3, 0, 0, 0, -2, 0, 0, 0, 0, 0})
	if !strings.Contains(got, "equalizer=f=31:t=q:w=1:g=3") ||
		!strings.Contains(got, "equalizer=f=500:t=q:w=1:g=-2") || strings.Count(got, "equalizer") != 2 {
		t.Fatalf("eq 链错误: %s", got)
	}
}

func TestBuildVolumeArgs(t *testing.T) {
	ext, codec, bitrate := resolveOutCodec("mp3", "a.wav", "192k")

	// 归一化：loudnorm + 钉回源采样率（loudnorm 单遍动态模式会抬采样率）
	args, err := buildVolumeArgs(volumeParams{Normalize: true, LUFS: -16, GainDB: 3}, 44100, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "loudnorm=I=-16:TP=-1.5:LRA=11") || !strings.Contains(got, "-ar 44100") {
		t.Fatalf("normalize args 错误: %s", got)
	}

	// 正增益：alimiter 防削顶
	args, _ = buildVolumeArgs(volumeParams{GainDB: 6}, 44100, ext, codec, bitrate)
	if !strings.Contains(strings.Join(args, " "), "volume=6dB,alimiter=limit=0.98:level=disabled") {
		t.Fatalf("正增益缺 limiter: %s", strings.Join(args, " "))
	}

	// 负增益：无 limiter
	args, _ = buildVolumeArgs(volumeParams{GainDB: -5}, 44100, ext, codec, bitrate)
	got = strings.Join(args, " ")
	if strings.Contains(got, "alimiter") || !strings.Contains(got, "volume=-5dB") {
		t.Fatalf("负增益不应有 limiter: %s", got)
	}

	if _, err := buildVolumeArgs(volumeParams{}, 44100, ext, codec, bitrate); err == nil {
		t.Fatal("全默认应报错")
	}
	if _, err := buildVolumeArgs(volumeParams{GainDB: 50}, 44100, ext, codec, bitrate); err == nil {
		t.Fatal("超范围增益应报错")
	}
}

func TestBuildFadeArgs(t *testing.T) {
	ext, codec, bitrate := resolveOutCodec("mp3", "a.wav", "192k")

	args, err := buildFadeArgs(fadeParams{FadeIn: 2, FadeOut: 3, Curve: "log"}, 60, ext, codec, bitrate)
	if err != nil {
		t.Fatalf("fade: %v", err)
	}
	got := strings.Join(args, " ")
	if !strings.Contains(got, "afade=t=in:st=0:d=2:curve=log") ||
		!strings.Contains(got, "afade=t=out:st=57:d=3:curve=log") {
		t.Fatalf("fade args 错误: %s", got)
	}

	// linear → tri（afade 的线性曲线名）
	args, _ = buildFadeArgs(fadeParams{FadeOut: 5}, 100, ext, codec, bitrate)
	got = strings.Join(args, " ")
	if !strings.Contains(got, "curve=tri") || !strings.Contains(got, "st=95") {
		t.Fatalf("linear 映射错误: %s", got)
	}

	if _, err := buildFadeArgs(fadeParams{}, 60, ext, codec, bitrate); err == nil {
		t.Fatal("全 0 应报错")
	}
	if _, err := buildFadeArgs(fadeParams{FadeIn: 2}, 0, ext, codec, bitrate); err == nil {
		t.Fatal("时长探测失败应报错")
	}
}

func TestOutputExtAndCodec(t *testing.T) {
	cases := []struct {
		format, src, wantExt, wantCodec string
	}{
		{"auto", "a.mp3", "mp3", "libmp3lame"},
		{"", "a.flac", "flac", "flac"},
		{"wav", "a.mp3", "wav", "pcm_s16le"},
		{"m4a", "a.wav", "m4a", "aac"},
		{"auto", "a.aac", "m4a", "aac"}, // 裸 AAC 落 m4a 容器
		{"auto", "a.xyz", "mp3", "libmp3lame"},
		{"auto", "a.ogg", "ogg", "libvorbis"},
	}
	for _, c := range cases {
		ext := resolveOutputExt(c.format, c.src)
		if ext != c.wantExt {
			t.Errorf("resolveOutputExt(%q,%q) = %q, want %q", c.format, c.src, ext, c.wantExt)
		}
		codec, _ := audioCodecFor(ext)
		if codec != c.wantCodec {
			t.Errorf("audioCodecFor(%q) = %q, want %q", ext, codec, c.wantCodec)
		}
	}
	// 无损不给码率
	tail := encodeTail("flac", "flac", "320k")
	if strings.Contains(strings.Join(tail, " "), "-b:a") {
		t.Fatalf("flac 不应带码率: %v", tail)
	}
}

func TestOrderedInputs(t *testing.T) {
	got := orderedInputs(map[string]string{
		"audio3": "c.mp3", "audio": "a.mp3", "audio2": "b.mp3", "other": "x", "audio0": "z",
	})
	if len(got) != 3 || got[0] != "a.mp3" || got[1] != "b.mp3" || got[2] != "c.mp3" {
		t.Fatalf("orderedInputs 顺序错误: %v", got)
	}
}
