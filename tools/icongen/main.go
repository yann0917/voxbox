// icongen 生成 voxbox 桌面版 app 图标的源图（默认 1024×1024 PNG）。
//
// 视觉语言见 design-system/voxbox/MASTER.md：「暖纸咖啡 × 暗夜烘焙」。
// 标记是「环形声纹」——16 根圆头声纹放射成环、长度错落，读起来像实时频谱。
// 刻意避开音频类 app 通用的「一排等宽竖条」电平表符号：撞脸的根本不是参数，
// 而是「等宽 + 竖直 + 居中 + 一行」这个排列方式，所以这里把线性换成放射。
//
// 用法：
//
//	go run ./tools/icongen -theme dark -out desktop/src-tauri/icons/icon-source.png
//	go run ./tools/icongen -theme light -out /tmp/icon-source-light.png
//	go run ./tools/icongen -theme mono -out /tmp/icon-source-mono.png
//
// 再用 cargo tauri icon 展开成全套尺寸。CLI 的默认输入是 ./app-icon.png（不是
// icons/icon-source.png），必须显式传参；在 desktop/ 下运行，产物落 src-tauri/icons/：
//
//	cd desktop && cargo tauri icon src-tauri/icons/icon-source.png -o src-tauri/icons
//
// 关于留白：默认满幅出图（图标体铺满画布），与 Ardot 视觉稿一致。
// macOS HIG 建议图标体占画布的 ~824/1024，留出四周透明边；需要时加 -inset 100。
// 满幅那版喂给 cargo tauri icon 在 Windows/Linux 上是对的，macOS 上会显得偏大。
//
// 几何与配色以 Ardot 视觉稿「voxbox App Icon · 最终稿」为准（fileId 730419634000820），
// 底/暖晕的参数是从母版 PNG 逐像素采样反解出来的，改动前先看注释里的推导。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
)

// ── 几何（一律以 1024 设计基准为单位，渲染时按目标画布等比缩放）──

const (
	baseSize     = 1024.0 // 设计基准画布
	cornerRadius = 224.0  // 圆角；实测母版 = 1024 × 21.875%
	rInner       = 215.0  // 声纹内端半径
	strokeWidth  = 44.0   // 声纹粗细（圆头帽）
)

// haloRadiusRatio：中心暖晕的半径占画布的比例。
// 母版实测：暖晕 alpha 沿半径是线性衰减，线性拟合（r=2..182 五个采样点）给出
// alpha(r) = 0.20 × (1 − r/R)，R ≈ 385px @1024 → 0.376。
// 与 Ardot 里 gradientTransform 的 1.35 倍缩放（0.5/1.35 = 0.370）一致。
const haloRadiusRatio = 0.376

// amps 是 16 根声纹的外端半径，索引 0 在 12 点方向，顺时针每 22.5° 一档。
// 振幅刻意压在中等区间：把范围拉到 <268 或 >456 会读成「爆炸/散开」，
// 有节奏的中等起伏才像实时频谱。
var amps = [16]float64{420, 360, 300, 340, 400, 440, 370, 310, 310, 370, 440, 400, 340, 300, 360, 420}

// 振幅 → 不透明度的映射区间（最短的 0.58，最长的 1.00），拉开层次。
const (
	ampMin = 300.0
	ampMax = 440.0
	opMin  = 0.58
	opMax  = 1.00
)

// ── 配色 ──

type rgba struct{ r, g, b, a float64 } // 分量 0..1

func hex(s string) rgba {
	s = strings.TrimPrefix(s, "#")
	var v [6]int
	for i := 0; i < 6; i++ {
		d := s[i]
		switch {
		case d >= '0' && d <= '9':
			v[i] = int(d - '0')
		case d >= 'a' && d <= 'f':
			v[i] = int(d-'a') + 10
		case d >= 'A' && d <= 'F':
			v[i] = int(d-'A') + 10
		}
	}
	return rgba{
		r: float64(v[0]<<4|v[1]) / 255,
		g: float64(v[2]<<4|v[3]) / 255,
		b: float64(v[4]<<4|v[5]) / 255,
		a: 1,
	}
}

type theme struct {
	name string
	// 底色：画布顶 → 底的纯线性渐变（材质律「无纯平面 · 暖顶光」）
	bgTop, bgBot rgba
	// 中心暖晕：色 + 中心不透明度（材质律「有空气感 · 暖光工作灯」）
	halo      rgba
	haloAlpha float64
	hasBg     bool
	// 声纹色随角度混合：顶端 → 底端。实色 + 按角度混色，
	// 而不是给每根声纹挂线性渐变——逐像素采样证明渐变会落在页面纵向上、
	// 与底色的顶光方向相反，还会在竖直声纹上留下「两端平色 + 中间软过渡」的断层。
	spokeTop, spokeBot rgba
	// 单色版：声纹平色不透明，不随角度/振幅变化（菜单栏 / favicon 场景）
	flat bool
}

var themes = map[string]theme{
	// 暗色（默认）：深夜的烘焙间。深烘咖啡棕黑 + 焦糖铜信号色。
	"dark": {
		name:      "dark",
		bgTop:     hex("2C2014"),
		bgBot:     hex("110B05"),
		halo:      hex("F2C184"),
		haloAlpha: 0.20,
		hasBg:     true,
		spokeTop:  hex("E9B87E"),
		spokeBot:  hex("C9843C"),
	},
	// 亮色：暖纸画布 + 深咖啡墨色。
	"light": {
		name:      "light",
		bgTop:     hex("FFFBF4"),
		bgBot:     hex("EFE3D2"),
		halo:      hex("C28E5A"),
		haloAlpha: 0.34,
		hasBg:     true,
		spokeTop:  hex("7C502F"),
		spokeBot:  hex("4F3119"),
	},
	// 单色：透明底 + 平色声纹，任意背景可用。
	"mono": {
		name:     "mono",
		hasBg:    false,
		spokeTop: hex("E2A35A"),
		spokeBot: hex("E2A35A"),
		flat:     true,
	},
}

func (t theme) opacity(i int) float64 {
	if t.flat {
		return 1
	}
	return opMin + (opMax-opMin)*(amps[i]-ampMin)/(ampMax-ampMin)
}

// ── 渲染 ──

func main() {
	name := flag.String("theme", "dark", "配色主题：dark | light | mono")
	out := flag.String("out", "icon-source.png", "输出 PNG 路径")
	size := flag.Int("size", 1024, "输出边长（正方形）")
	inset := flag.Int("inset", 0, "画布四周透明留白（px）；macOS HIG 建议图标体占 824/1024，传 100")
	svg := flag.String("svg", "", "同时输出矢量 SVG 到该路径（web favicon 用，几何与 PNG 同源）")
	flag.Parse()

	th, ok := themes[*name]
	if !ok {
		fmt.Fprintf(os.Stderr, "未知主题 %q，可选：dark | light | mono\n", *name)
		os.Exit(2)
	}
	if *size < 64 {
		fmt.Fprintln(os.Stderr, "size 太小；Tauri 至少需要 1024 的源图才能展开全尺寸")
		os.Exit(2)
	}
	if *inset < 0 || *inset*2 >= *size {
		fmt.Fprintln(os.Stderr, "inset 必须 >=0 且小于画布的一半")
		os.Exit(2)
	}

	img := render(th, *size, *inset)
	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
	fmt.Printf("%s  %s  %d×%d\n", *out, th.name, *size, *size)
	if *svg != "" {
		if err := os.WriteFile(*svg, []byte(renderSVG(th)), 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("%s  %s  SVG\n", *svg, th.name)
	}
}

func render(th theme, size, inset int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 图标体：默认铺满画布；-inset 时向内收缩，四周留透明边（平台惯例）
	body := float64(size - 2*inset)
	s := body / baseSize         // 设计基准 → 图标体
	c := float64(inset) + body/2 // 图标体中心
	half := strokeWidth * s / 2
	in := rInner * s
	haloR := haloRadiusRatio * body
	maxReach := in
	for _, a := range amps {
		if a*s > maxReach {
			maxReach = a * s
		}
	}
	maxReach += half + 1

	// 预计算 16 根声纹的端点与色/透明度
	var segs [16]struct {
		x1, y1, x2, y2 float64
		col            rgba
		alpha          float64
	}
	for i := range segs {
		th0 := float64(i) * 2 * math.Pi / float64(len(segs))
		dx, dy := math.Sin(th0), -math.Cos(th0)
		// 色随角度混合：顶端（cos=1）取 spokeTop，底端（cos=-1）取 spokeBot
		m := (1 - math.Cos(th0)) / 2
		segs[i] = struct {
			x1, y1, x2, y2 float64
			col            rgba
			alpha          float64
		}{
			x1: c + in*dx, y1: c + in*dy,
			x2: c + amps[i]*s*dx, y2: c + amps[i]*s*dy,
			col:   lerpRGBA(th.spokeTop, th.spokeBot, m),
			alpha: th.opacity(i),
		}
	}

	// 底色渐变的起点/终点（图标体顶部 → 底部）
	z0, z1 := float64(inset), float64(size-inset)
	for y := 0; y < size; y++ {
		fy := float64(y)
		// 底色在这一行的取值（纯线性、与 x 无关）
		base := lerpRGBA(th.bgTop, th.bgBot, (fy-z0)/(z1-z0))
		for x := 0; x < size; x++ {
			fx := float64(x)

			var acc rgba
			if th.hasBg {
				acc = base
				acc.a = roundRectCoverage(fx, fy, z0, z1, cornerRadius*s)
			}

			// 中心暖晕：线性径向衰减
			if th.haloAlpha > 0 {
				r := math.Hypot(fx-c, fy-c)
				if a := th.haloAlpha * math.Max(0, 1-r/haloR); a > 0 {
					acc = over(rgba{th.halo.r, th.halo.g, th.halo.b, a}, acc)
				}
			}

			// 声纹
			r := math.Hypot(fx-c, fy-c)
			if r <= maxReach && r >= in-half-1 {
				for i := range segs {
					g := segCoverage(fx, fy, segs[i].x1, segs[i].y1, segs[i].x2, segs[i].y2, half)
					if g <= 0 {
						continue
					}
					sc := segs[i].col
					sc.a = segs[i].alpha * g
					acc = over(sc, acc)
				}
			}

			if acc.a <= 0 {
				continue
			}
			img.SetRGBA(x, y, color.RGBA{
				R: u8(acc.r), G: u8(acc.g), B: u8(acc.b), A: u8(acc.a),
			})
		}
	}
	return img
}

// renderSVG 用与 render() 完全同源的几何参数输出矢量版（web favicon）：
// 圆角线性渐变底 + 中心径向暖晕 + 16 根圆头声纹线段，满幅无留白。
func renderSVG(th theme) string {
	var b strings.Builder
	b.WriteString("<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 1024 1024\">\n")
	b.WriteString("  <defs>\n")
	fmt.Fprintf(&b, "    <linearGradient id=\"bg\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1024\" gradientUnits=\"userSpaceOnUse\">\n      <stop offset=\"0\" stop-color=\"%s\"/>\n      <stop offset=\"1\" stop-color=\"%s\"/>\n    </linearGradient>\n", hexStr(th.bgTop), hexStr(th.bgBot))
	if th.haloAlpha > 0 {
		fmt.Fprintf(&b, "    <radialGradient id=\"halo\" cx=\"512\" cy=\"512\" r=\"%.0f\" gradientUnits=\"userSpaceOnUse\">\n      <stop offset=\"0\" stop-color=\"%s\" stop-opacity=\"%.2f\"/>\n      <stop offset=\"1\" stop-color=\"%s\" stop-opacity=\"0\"/>\n    </radialGradient>\n", haloRadiusRatio*baseSize, hexStr(th.halo), th.haloAlpha, hexStr(th.halo))
	}
	b.WriteString("  </defs>\n")
	fmt.Fprintf(&b, "  <rect width=\"1024\" height=\"1024\" rx=\"%.0f\" fill=\"url(#bg)\"/>\n", cornerRadius)
	if th.haloAlpha > 0 {
		fmt.Fprintf(&b, "  <rect width=\"1024\" height=\"1024\" rx=\"%.0f\" fill=\"url(#halo)\"/>\n", cornerRadius)
	}
	c := baseSize / 2
	in := rInner
	for i := 0; i < len(amps); i++ {
		th0 := float64(i) * 2 * math.Pi / float64(len(amps))
		dx, dy := math.Sin(th0), -math.Cos(th0)
		m := (1 - math.Cos(th0)) / 2
		col := lerpRGBA(th.spokeTop, th.spokeBot, m)
		fmt.Fprintf(&b, "  <line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"%s\" stroke-opacity=\"%.2f\" stroke-width=\"%.0f\" stroke-linecap=\"round\"/>\n",
			c+in*dx, c+in*dy, c+amps[i]*dx, c+amps[i]*dy, hexStr(col), th.opacity(i), strokeWidth)
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// hexStr rgba → "#RRGGBB"（favicon/调试两用）。
func hexStr(c rgba) string {
	return fmt.Sprintf("#%02X%02X%02X", u8(c.r), u8(c.g), u8(c.b))
}

// over 直通道 alpha 合成：src 叠在 dst 之上。
// 母版实测证实 Ardot 是在 sRGB 数值上直接线性混合（暖晕中心不透明度按 R/G/B 三通道
// 反解均为 0.196），所以这里不做线性空间转换，保持一致。
func over(src, dst rgba) rgba {
	if src.a <= 0 {
		return dst
	}
	out := (src.a + dst.a*(1-src.a))
	if out <= 0 {
		return rgba{}
	}
	mix := func(s, d float64) float64 {
		return (s*src.a + d*dst.a*(1-src.a)) / out
	}
	return rgba{mix(src.r, dst.r), mix(src.g, dst.g), mix(src.b, dst.b), out}
}

func lerpRGBA(a, b rgba, t float64) rgba {
	t = clamp(t, 0, 1)
	return rgba{
		a.r + (b.r-a.r)*t,
		a.g + (b.g-a.g)*t,
		a.b + (b.b-a.b)*t,
		a.a + (b.a-a.a)*t,
	}
}

// segCoverage 是点到圆头线段的覆盖率，1px 平滑带做抗锯齿。
func segCoverage(px, py, x1, y1, x2, y2, half float64) float64 {
	dx, dy := x2-x1, y2-y1
	t := 0.0
	if l2 := dx*dx + dy*dy; l2 > 0 {
		t = clamp(((px-x1)*dx+(py-y1)*dy)/l2, 0, 1)
	}
	return clamp(half+0.5-math.Hypot(px-(x1+t*dx), py-(y1+t*dy)), 0, 1)
}

// roundRectCoverage 是正方形 [lo,hi]² 的圆角覆盖率，
// 用有符号距离场算，1px 平滑带做抗锯齿。
func roundRectCoverage(px, py, lo, hi, r float64) float64 {
	hc := (hi - lo) / 2
	cc := (lo + hi) / 2
	qx := math.Abs(px-cc) - (hc - r)
	qy := math.Abs(py-cc) - (hc - r)
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
	return clamp(0.5-d, 0, 1)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func u8(v float64) uint8 {
	x := int(math.Round(v * 255))
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return uint8(x)
}
