// 包络（垫音分段控制）：点列 [[秒,dB]…] 与 ffmpeg volume 表达式编译。
// 语义与前端 linearRampToValueAtTime 严格一致（spec §6.4）：
// 首点前=首点值、末点后=末点值、段内线性、同刻相邻双点=硬跳变。
package audiotool

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// envPoint 单个包络点：T 秒时刻的增益 DB（dB，可负）。
type envPoint struct {
	T  float64
	DB float64
}

// envNum 数值格式化：最短十进制表示（整数不带小数点），负数天然带负号。
func envNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// envMaxPoints 包络点列硬上限：防御表达式超长与逐帧求值性能退化；超限报参数错误，
// 不做静默截断（spec §9 终审裁决：静默截断比硬失败更危险）。
const envMaxPoints = 120

// parseEnvParam 解析包络参数：接受原生数组（Web body）或 JSON 字符串（CLI/MCP）。
// 键缺省或空数组返回 (nil, nil)=恒 vocal_gain；元素非法报错；负秒钳 0；时间升序稳定排序；
// 点数超 envMaxPoints 报错（硬上限，不截断）。
func parseEnvParam(params map[string]any, key string) ([]envPoint, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return nil, nil
	}
	var raw []any
	switch e := v.(type) {
	case string:
		if e == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(e), &raw); err != nil {
			return nil, fmt.Errorf("%s 不是合法 JSON: %w", key, err)
		}
	case []any:
		raw = e
	default:
		return nil, fmt.Errorf("%s 须为数组", key)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > envMaxPoints {
		return nil, fmt.Errorf("垫音包络点数超上限（%d）", envMaxPoints)
	}
	pts := make([]envPoint, 0, len(raw))
	for i, item := range raw {
		pair, ok := item.([]any)
		if !ok || len(pair) != 2 {
			return nil, fmt.Errorf("%s[%d] 须为 [秒,dB] 数值对", key, i)
		}
		t, err1 := toFloat(pair[0])
		db, err2 := toFloat(pair[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%s[%d] 须为 [秒,dB] 数值对", key, i)
		}
		// NaN/Inf 拒绝：JSON 字符串通道 encoding/json 本身拒收，原生数组通道（Go 侧
		// 直构/未来参数通道）可达，放行会污染排序与表达式；与既有错误风格同形。
		if math.IsNaN(t) || math.IsInf(t, 0) || math.IsNaN(db) || math.IsInf(db, 0) {
			return nil, fmt.Errorf("%s[%d] 须为有限数值", key, i)
		}
		if t < 0 {
			t = 0
		}
		pts = append(pts, envPoint{T: t, DB: db})
	}
	// 稳定排序：同刻相邻双点（硬跳变）必须保持提供顺序，才能区分跳前值/跳后值
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].T < pts[j].T })
	return pts, nil
}

// toFloat 宽容取数：Web JSON 解出 float64，CLI 侧可能传 int 或 json.Number。
func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	}
	return 0, errors.New("非数值")
}

// dbToLin dB → 线性幅度倍率：ffmpeg volume 表达式的求值结果按线性倍率解释（无 dB 后缀
// 语义，那是常量路径 volume='-16dB' 的解析行为），表达式路径必须编译期完成 10^(dB/20)。
// 与前端 dbToLin（web/src/lib/mixer.ts）同式。
func dbToLin(db float64) float64 { return math.Pow(10, db/20) }

// compileEnvExpr 编译为嵌套 if 分段线性表达式（volume '…':eval=frame 用）。
// 表达式值为线性幅度：每个点的 dB 发射前换算 L=10^(dB/20)，段内在幅度上线性插值——
// 与前端 linearRampToValueAtTime(dbToLin 端点) 严格一致（spec §6.4）。
// 统一结构：n 点 → n 层 if，条件依次 lt(t,T0)…lt(t,T_{n-1})（全部点时刻都出条件，
// 含 T0=0，不做分支跳过），分支体依次为 L0、seg(P0→P1)…seg(P_{n-2}→P_{n-1})，
// else 兜底 L_{n-1}；单点退化为常量。恒值段（同刻对）分支体退化为常量；
// 调用方负责整值单引号包裹。
func compileEnvExpr(pts []envPoint) (string, error) {
	if len(pts) == 0 {
		return "", errors.New("包络点列为空")
	}
	if len(pts) == 1 {
		return "(" + envNum(dbToLin(pts[0].DB)) + ")", nil
	}
	// seg 一段的分支体：同刻=硬跳变，落 to 点线性幅度常量；否则幅度线性段模板
	// (L1)+((L2)-(L1))*(t-(T1))/(T2-T1)，L=10^(dB/20) 与 T 均为 envNum 输出
	seg := func(from, to envPoint) string {
		l1, l2 := dbToLin(from.DB), dbToLin(to.DB)
		if from.T == to.T {
			return "(" + envNum(l2) + ")"
		}
		return "(" + envNum(l1) + ")+((" + envNum(l2) + ")-(" + envNum(l1) + "))*(t-(" +
			envNum(from.T) + "))/(" + envNum(to.T-from.T) + ")"
	}
	var b []byte
	// 最外层：条件 lt(t,T0)，分支体 L0
	b = append(b, ("if(lt(t," + envNum(pts[0].T) + "),(" + envNum(dbToLin(pts[0].DB)) + ")")...)
	for k := 1; k < len(pts); k++ {
		b = append(b, (",if(lt(t," + envNum(pts[k].T) + ")," + seg(pts[k-1], pts[k]))...)
	}
	// 最内层 else 兜底 = 末点线性幅度（末点后=末点值）
	b = append(b, (",(" + envNum(dbToLin(pts[len(pts)-1].DB)) + ")")...)
	// 收口：n 层 if 需要 n 个右括号
	for k := 1; k < len(pts); k++ {
		b = append(b, ')')
	}
	b = append(b, ')')
	return string(b), nil
}
