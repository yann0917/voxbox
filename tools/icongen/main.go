// icongen 生成桌面版占位图标（1024×1024 PNG）：深空午夜蓝圆角底 + 电光青信号条。
// 视觉稿确认后替换源图重跑 cargo tauri icon 即可，生成链不变。
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
)

const size = 1024

var (
	bg  = color.RGBA{11, 30, 51, 255}   // 深空午夜蓝
	fg  = color.RGBA{34, 211, 238, 255} // 电光青
	dim = color.RGBA{34, 211, 238, 90}  // 青色弱化
)

func main() {
	out := flag.String("out", "icon-source.png", "输出 PNG 路径")
	flag.Parse()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	drawRoundedRect(img, 64, 64, 896, 896, 180, bg)
	heights := []int{280, 440, 640, 440, 280} // 五根信号条，中间最高
	const barW, gap = 64, 48
	x := (size - (len(heights)*barW + (len(heights)-1)*gap)) / 2
	for i, h := range heights {
		c := fg
		if i == 0 || i == len(heights)-1 {
			c = dim
		}
		drawRect(img, x, (size-h)/2, barW, h, c)
		x += barW + gap
	}
	f, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func drawRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for i := x; i < x+w; i++ {
		for j := y; j < y+h; j++ {
			img.Set(i, j, c)
		}
	}
}

// 圆角矩形 = 两根横条 + 两根竖条 + 四角整圆取并集。
func drawRoundedRect(img *image.RGBA, x, y, w, h, r int, c color.RGBA) {
	drawRect(img, x+r, y, w-2*r, h, c)
	drawRect(img, x, y+r, w, h-2*r, c)
	for _, p := range [][2]int{{x + r, y + r}, {x + w - r, y + r}, {x + r, y + h - r}, {x + w - r, y + h - r}} {
		for i := -r; i <= r; i++ {
			for j := -r; j <= r; j++ {
				if i*i+j*j <= r*r {
					img.Set(p[0]+i, p[1]+j, c)
				}
			}
		}
	}
}
