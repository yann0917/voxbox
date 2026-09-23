package audiotool

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func mustJSON(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}

func TestParseEnvParam(t *testing.T) {
	// 键缺省与空数组同义：nil, nil
	if pts, err := parseEnvParam(map[string]any{}, "vocal_env"); pts != nil || err != nil {
		t.Errorf("缺省键 = %v, %v", pts, err)
	}
	if pts, err := parseEnvParam(map[string]any{"vocal_env": []any{}}, "vocal_env"); pts != nil || err != nil {
		t.Errorf("空数组 = %v, %v", pts, err)
	}
	// 原生数组（Web JSON body 直达）
	pts, err := parseEnvParam(map[string]any{"vocal_env": mustJSON(`[[10,-16],[5,-12],[5,-60]]`)}, "vocal_env")
	if err != nil {
		t.Fatal(err)
	}
	// 排序：时间升序；同刻保持提供顺序（稳定排序）
	if pts[0].T != 5 || pts[0].DB != -12 || pts[1].T != 5 || pts[1].DB != -60 || pts[2].T != 10 {
		t.Errorf("排序/保序错误: %+v", pts)
	}
	// 负秒钳 0；字符串 JSON（CLI --env）等价
	pts2, err := parseEnvParam(map[string]any{"vocal_env": "[[-3,0],[8,6]]"}, "vocal_env")
	if err != nil || pts2[0].T != 0 || pts2[0].DB != 0 || pts2[1].DB != 6 {
		t.Errorf("字符串 JSON/负秒钳制错误: %+v, %v", pts2, err)
	}
	// 非法：长度≠2 / 非数值
	if _, err := parseEnvParam(map[string]any{"vocal_env": mustJSON(`[[1,2,3]]`)}, "vocal_env"); err == nil {
		t.Error("长度 3 应报错")
	}
	if _, err := parseEnvParam(map[string]any{"vocal_env": mustJSON(`[["a",2]]`)}, "vocal_env"); err == nil {
		t.Error("非数值应报错")
	}
	// 非有限数值拒绝（JSON 字符串通道 encoding/json 本身拒 NaN/Inf，原生数组通道可达）
	if _, err := parseEnvParam(map[string]any{"vocal_env": []any{[]any{math.NaN(), 0.0}}}, "vocal_env"); err == nil || !strings.Contains(err.Error(), "须为有限数值") {
		t.Errorf("NaN 秒应报有限数值错误: %v", err)
	}
	if _, err := parseEnvParam(map[string]any{"vocal_env": []any{[]any{0.0, math.Inf(1)}}}, "vocal_env"); err == nil || !strings.Contains(err.Error(), "须为有限数值") {
		t.Errorf("+Inf dB 应报有限数值错误: %v", err)
	}
	if _, err := parseEnvParam(map[string]any{"vocal_env": []any{[]any{math.Inf(-1), 0.0}}}, "vocal_env"); err == nil {
		t.Error("-Inf 秒应报错")
	}
	// 点数硬上限 120：121 点报参数错误，不做静默截断（spec §9 终审裁决）
	many := make([]any, 121)
	for i := range many {
		many[i] = []any{float64(i), 0.0}
	}
	if _, err := parseEnvParam(map[string]any{"vocal_env": many}, "vocal_env"); err == nil || !strings.Contains(err.Error(), "超上限（120）") {
		t.Errorf("121 点应报超上限: %v", err)
	}
	ok120 := make([]any, 120)
	for i := range ok120 {
		ok120[i] = []any{float64(i), 0.0}
	}
	if _, err := parseEnvParam(map[string]any{"vocal_env": ok120}, "vocal_env"); err != nil {
		t.Errorf("120 点应恰在界内通过: %v", err)
	}
}

func TestCompileEnvExpr(t *testing.T) {
	// 单点=常量（线性幅度，volume 值，不带 if）；10^(-16/20)=0.15848931924611134（envNum 最短表示）
	if got, err := compileEnvExpr([]envPoint{{T: 0, DB: -16}}); err != nil || got != "(0.15848931924611134)" {
		t.Errorf("单点 = %q, %v", got, err)
	}
	// 两点线性：统一结构 = if(lt(t,T0),L0,if(lt(t,T1),seg(P0→P1),L1))
	// （控制器裁决的统一嵌套 if：全部点时刻都出条件，含 T0；期望串已用 ffmpeg aevalsrc 验证可解析。
	//   表达式值为线性幅度倍率：L=10^(dB/20)，段内在幅度上线性插值——终审 C1 修正，
	//   旧版直接发射 dB 数字会被 ffmpeg 当倍率（-60 → 60 倍增益削波））
	pts := []envPoint{{T: 10, DB: -16}, {T: 20, DB: -60}}
	got, err := compileEnvExpr(pts)
	want := "if(lt(t,10),(0.15848931924611134),if(lt(t,20),(0.15848931924611134)+((0.001)-(0.15848931924611134))*(t-(10))/(10),(0.001)))"
	if err != nil || got != want {
		t.Errorf("两点:\n got  %q\n want %q", got, want)
	}
	// 同刻跳变：{30,-60}→{30,-16} 为硬跳变，t≥30 直接落 -16 线性幅度（阶跃段退化为常量分支 seg(P1→P2)=L(-16)）。
	// 统一结构逐层展开：L0 | seg(P0→P1) | seg(P1→P2)=(L16) | seg(P2→P3) | else L3。
	// 注：任务裁决文本中的 4 点示例串最内层误写为 seg(P1→P3)（-60 起坡）且多一个右括号，
	// 被 ffmpeg 拒绝（Missing ')' or too many args）；此处按裁决的规范结构（bodies=
	// L0,seg(P0→P1),seg(P1→P2),seg(P2→P3),else L3）推导，已用 ffmpeg aevalsrc 验证可解析。
	pts = []envPoint{{T: 0, DB: -16}, {T: 30, DB: -60}, {T: 30, DB: -16}, {T: 240, DB: -16}}
	got, err = compileEnvExpr(pts)
	want = "if(lt(t,0),(0.15848931924611134),if(lt(t,30),(0.15848931924611134)+((0.001)-(0.15848931924611134))*(t-(0))/(30),if(lt(t,30),(0.15848931924611134),if(lt(t,240),(0.15848931924611134)+((0.15848931924611134)-(0.15848931924611134))*(t-(30))/(210),(0.15848931924611134)))))"
	if err != nil || got != want {
		t.Errorf("跳变:\n got  %q\n want %q", got, want)
	}
	if _, err := compileEnvExpr(nil); err == nil {
		t.Error("空点列应报错")
	}
}

// TestCompileEnvExprLinearGain C1 回归锚：表达式系数逐字等于 10^(dB/20)（ffmpeg volume
// 表达式结果按线性倍率解释），防止回退为直接发射 dB 数字。期望串在测试侧用 math.Pow
// 独立推导（与实现零共享的换算路径），再拼同一嵌套结构逐字比对。
func TestCompileEnvExprLinearGain(t *testing.T) {
	l16 := envNum(math.Pow(10, -16.0/20.0))
	l60 := envNum(math.Pow(10, -60.0/20.0))
	got, err := compileEnvExpr([]envPoint{{T: 10, DB: -16}, {T: 20, DB: -60}})
	if err != nil {
		t.Fatal(err)
	}
	want := "if(lt(t,10),(" + l16 + "),if(lt(t,20),(" + l16 + ")+((" + l60 + ")-(" + l16 + "))*(t-(10))/(10),(" + l60 + ")))"
	if got != want {
		t.Errorf("线性增益语义:\n got  %q\n want %q", got, want)
	}
	// 幅度线性由结构自证：插值作用在 (L1)+((L2)-(L1))*… 即两端的线性幅度上，
	// dB 域的线性插值（旧 bug 形态）在此串型下不可能逐字匹配。
}
