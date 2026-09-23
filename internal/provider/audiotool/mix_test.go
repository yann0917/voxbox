package audiotool

import (
	"strings"
	"testing"
)

func TestMixBaseName(t *testing.T) {
	cases := map[string]string{
		"/data/editor/01/稻香_instrumental.mp3":  "稻香",
		"/data/editor/01/track_background.wav": "track",
		"/data/editor/01/稻香_vocals.flac":       "稻香",
		"/data/editor/01/稻香_voice.mp3":         "稻香",
		"/data/editor/01/裸名.mp3":               "裸名",       // 无后缀不剥
		"/data/editor/01/稻香_remix.mp3":         "稻香_remix", // 非轨道后缀不剥
	}
	for in, want := range cases {
		if got := mixBaseName(in); got != want {
			t.Errorf("mixBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildMixArgs(t *testing.T) {
	p := mixParams{
		VocalGain:     -16,
		VocalHighpass: 120,
		MusicGain:     0,
		Loudness:      -14,
		EnvExpr:       "", // 无包络 → 常量 volume
		Format:        "mp3",
	}
	args, err := buildMixArgs(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	// 整段锚定：伴奏增益链恒在（music_gain=0 也保留 volume='0dB' 直通，滤镜图形状恒定）、
	// 人声链（低切在前）、amix 两轨序 [伴奏][人声] 不可颠倒、响度归一追加段
	for _, want := range []string{
		"[0:a]volume='0dB'[m];[1:a]highpass=f=120,volume='-16dB'[v];",
		"[m][v]amix=inputs=2:duration=longest:normalize=0[am]",
		";[am]loudnorm=I=-14:TP=-1.5:LRA=11[out]",
		"-map [out]",
		"-c:a libmp3lame -b:a 128k", // mp3 固定 128k（默认档纪律，无 bitrate 参数）
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("滤镜图/编码参数缺失 %q: %s", want, joined)
		}
	}
	// 高切 0 → 无 highpass；响度 0 → 无 loudnorm；包络 → eval=frame 表达式（线性幅度，
	// 终审 C1：volume 表达式结果按倍率解释，系数=10^(dB/20)）；wav → pcm_s16le
	p2 := mixParams{VocalGain: -6, VocalHighpass: 0, Loudness: 0,
		EnvExpr: "if(lt(t,20),(0.15848931924611134),(0.15848931924611134)+((0.001)-(0.15848931924611134))*(t-(10))/(10))", Format: "wav"}
	args2, err := buildMixArgs(p2)
	if err != nil {
		t.Fatal(err)
	}
	j2 := strings.Join(args2, " ")
	if strings.Contains(j2, "highpass") || strings.Contains(j2, "loudnorm") ||
		!strings.Contains(j2, "[0:a]volume='0dB'[m];[1:a]volume='if(lt(t,20),(0.15848931924611134),(0.15848931924611134)+((0.001)-(0.15848931924611134))*(t-(10))/(10))':eval=frame[v]") ||
		!strings.Contains(j2, "[m][v]amix=inputs=2:duration=longest:normalize=0[am]") ||
		!strings.Contains(j2, "-map [am]") ||
		!strings.Contains(j2, "-c:a pcm_s16le") {
		t.Errorf("开关/包络/编码错误: %s", j2)
	}
	// music_gain≠0：[0:a] 位置 envNum 插值生效（-3dB），与恒 0 直通形态区分；开关关→无 highpass/loudnorm
	p3 := mixParams{VocalGain: -6, VocalHighpass: 0, Loudness: 0, MusicGain: -3, Format: "mp3"}
	args3, err := buildMixArgs(p3)
	if err != nil {
		t.Fatal(err)
	}
	j3 := strings.Join(args3, " ")
	if !strings.Contains(j3, "[0:a]volume='-3dB'[m];[1:a]volume='-6dB'[v];") ||
		strings.Contains(j3, "highpass") || strings.Contains(j3, "loudnorm") {
		t.Errorf("伴奏增益插值/开关错误: %s", j3)
	}
}

// TestParseMixParams 参数域钳制（终审 I2）：vocal_gain [-99,6]、music_gain [-60,12]、
// vocal_highpass [0,20000]Hz；master_loudness ∈ loudnorm 合法域 [-70,-5]（域外 ffmpeg
// 直接失败 Result too large），0=关不钳；format 非 wav 一律 mp3。
func TestParseMixParams(t *testing.T) {
	// 缺省=垫音默认档
	if p := parseMixParams(map[string]any{}); p.VocalGain != -16 || p.VocalHighpass != 120 ||
		p.MusicGain != 0 || p.Loudness != -14 || p.Format != "mp3" {
		t.Errorf("缺省参数错误: %+v", p)
	}
	// 域外钳到边界
	if p := parseMixParams(map[string]any{"master_loudness": -99.0, "vocal_highpass": 50000.0,
		"vocal_gain": -200.0, "music_gain": 99.0}); p.Loudness != -70 || p.VocalHighpass != 20000 ||
		p.VocalGain != -99 || p.MusicGain != 12 {
		t.Errorf("下/上界钳制错误: %+v", p)
	}
	// loudness=0=关（不钳到 -5）；highpass 负值钳 0（关低切）
	if p := parseMixParams(map[string]any{"master_loudness": 0.0, "vocal_highpass": -5.0}); p.Loudness != 0 || p.VocalHighpass != 0 {
		t.Errorf("0=关/负低切钳制错误: %+v", p)
	}
	// 正向越界：loudness 钳上界 -5
	if p := parseMixParams(map[string]any{"master_loudness": -1.0}); p.Loudness != -5 {
		t.Errorf("loudness 上界钳制错误: %+v", p)
	}
	// format 仅认 wav
	if p := parseMixParams(map[string]any{"format": "flac"}); p.Format != "mp3" {
		t.Errorf("format 非 wav 应归 mp3: %+v", p)
	}
}
