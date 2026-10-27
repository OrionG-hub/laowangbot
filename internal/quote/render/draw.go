package render

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

// hex 解析 "#rrggbb" 或 "rrggbb"，失败时返回 fallback。
func hex(value string, fallback color.RGBA) color.RGBA {
	value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if len(value) != 6 {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return fallback
	}
	return color.RGBA{R: uint8(parsed >> 16), G: uint8(parsed >> 8), B: uint8(parsed), A: 255}
}

// withAlpha 把不透明色换成预乘过的半透明色。
func withAlpha(value color.RGBA, alpha float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(value.R) * alpha),
		G: uint8(float64(value.G) * alpha),
		B: uint8(float64(value.B) * alpha),
		A: uint8(255 * alpha),
	}
}

// arcPoints 是圆心 (x, y)、半径 r、从角 a0 到 a1（弧度）的一串点。
func arcPoints(x, y, r, a0, a1 float64) [][2]float64 {
	steps := max(8, int(math.Abs(a1-a0)*r/4))
	points := make([][2]float64, 0, steps+1)
	for step := 0; step <= steps; step++ {
		angle := a0 + (a1-a0)*float64(step)/float64(steps)
		points = append(points, [2]float64{x + r*math.Cos(angle), y + r*math.Sin(angle)})
	}
	return points
}

// roundedRectPoints 是圆角矩形的轮廓点，按顺时针排列。
func roundedRectPoints(x, y, w, h, r float64) [][2]float64 {
	r = math.Min(r, math.Min(w/2, h/2))
	var points [][2]float64
	points = append(points, arcPoints(x+w-r, y+r, r, -math.Pi/2, 0)...)
	points = append(points, arcPoints(x+w-r, y+h-r, r, 0, math.Pi/2)...)
	points = append(points, arcPoints(x+r, y+h-r, r, math.Pi/2, math.Pi)...)
	points = append(points, arcPoints(x+r, y+r, r, math.Pi, 3*math.Pi/2)...)
	return points
}

// fillRoundedRect 在 dst 上填充一个圆角矩形（抗锯齿，叠加在已有内容上）。
func fillRoundedRect(dst *image.RGBA, x, y, w, h, r int, col color.Color) {
	if w <= 0 || h <= 0 {
		return
	}
	points := roundedRectPoints(float64(x), float64(y), float64(w), float64(h), float64(r))
	box := image.Rect(x, y, x+w+1, y+h+1).Intersect(dst.Bounds())
	if box.Empty() {
		return
	}
	raster := vector.NewRasterizer(box.Dx(), box.Dy())
	raster.DrawOp = draw.Over
	for index, point := range points {
		px, py := float32(point[0]-float64(box.Min.X)), float32(point[1]-float64(box.Min.Y))
		if index == 0 {
			raster.MoveTo(px, py)
		} else {
			raster.LineTo(px, py)
		}
	}
	raster.ClosePath()
	raster.Draw(dst, box, image.NewUniform(col), image.Point{})
}

// fillRect 在 dst 上填充一个普通矩形。
func fillRect(dst *image.RGBA, x, y, w, h int, col color.Color) {
	rect := image.Rect(x, y, x+w, y+h).Intersect(dst.Bounds())
	if rect.Empty() {
		return
	}
	draw.Draw(dst, rect, image.NewUniform(col), image.Point{}, draw.Over)
}

// circleMask 把方形图裁成圆形：圆外透明，边缘 1px 平滑。
func circleMask(src image.Image, size int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	// 先把源图按 cover 缩放到 size 方形，再逐像素乘圆形遮罩。
	sq := resizeCover(src, size, size)
	center := float64(size) / 2
	radius := center
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-center, float64(y)+0.5-center
			d := math.Hypot(dx, dy)
			var alpha float64
			switch {
			case d <= radius-0.5:
				alpha = 1
			case d >= radius+0.5:
				alpha = 0
			default:
				alpha = radius + 0.5 - d
			}
			if alpha == 0 {
				continue
			}
			si := sq.PixOffset(x, y)
			di := out.PixOffset(x, y)
			for channel := 0; channel < 4; channel++ {
				out.Pix[di+channel] = uint8(math.Round(float64(sq.Pix[si+channel]) * alpha))
			}
		}
	}
	return out
}

// gradientAvatar 是没有头像时的 fallback：按 ID 取一对渐变色，中间写名字首字。
func gradientAvatar(c *canvas, name string, id int64, size int) *image.RGBA {
	pair := avatarGradients[absID(id)%int64(len(avatarGradients))]
	top, bottom := pair[0], pair[1]
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		k := float64(y) / float64(max(1, size-1))
		mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*k) }
		col := color.RGBA{R: mix(top.R, bottom.R), G: mix(top.G, bottom.G), B: mix(top.B, bottom.B), A: 255}
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, col)
		}
	}
	initial := "?"
	if runes := []rune(name); len(runes) > 0 {
		initial = string(runes[0])
	}
	// 首字按头像的一半字号居中写，白字加粗（双画偏移）。
	fontSize := float64(size) * 0.45
	face := c.faceFor(fontSize, fontCJK)
	if face != nil {
		w := c.measure(initial, fontSize)
		metrics := face.Metrics()
		ascent := float64(metrics.Ascent) / 64
		descent := float64(metrics.Descent) / 64
		baseline := float64(size)/2 + (ascent-descent)/2
		c.drawRunes(img, initial, size/2-w/2, int(baseline), fontSize, color.RGBA{R: 255, G: 255, B: 255, A: 255}, true)
	}
	return circleMask(img, size)
}

// nameColor 按发送者 ID 取名字颜色（TeleBox 的暗色配色，id % 7）。
func nameColor(id int64) color.RGBA {
	return nameColors[absID(id)%int64(len(nameColors))]
}

// colorForName 按名字字符串取颜色：回复块没有发送者 ID 时用名字哈希。
func colorForName(name string) color.RGBA {
	var sum int64
	for _, r := range name {
		sum += int64(r)
	}
	return nameColors[absID(sum)%int64(len(nameColors))]
}

func absID(id int64) int64 {
	if id < 0 {
		return -id
	}
	return id
}

// pillWidth 是标签 pill 的宽度：文字宽 + 两边内边距（输出像素）。
func (c *canvas) pillWidth(text string, fontSize float64) int {
	return c.measure(text, fontSize) + 2*pillPadX*c.scale
}

// 标签文字的身份色：群主紫、管理员绿、普通成员灰字无底；管理员和群主有 15% 透明度的同色背景。
var (
	tagCreatorText = color.RGBA{R: 186, G: 104, B: 200, A: 255}
	tagAdminText   = color.RGBA{R: 102, G: 187, B: 106, A: 255}
	tagMemberText  = color.RGBA{R: 160, G: 160, B: 170, A: 255}
)

// tagTextColor 按身份给标签文字配色。
func tagTextColor(role string) color.RGBA {
	switch role {
	case "creator":
		return tagCreatorText
	case "admin":
		return tagAdminText
	default:
		return tagMemberText
	}
}

// drawPill 在 (x, y) 画一个标签，y 是标签顶；管理员/群主画淡色圆角底，
// 返回占宽（文字宽 + 两侧留白，版式与之前 pill 相同）。
func (c *canvas) drawPill(x, y int, text string, fontSize float64, textColor color.RGBA) int {
	width := c.pillWidth(text, fontSize)
	height := int(fontSize) + 2*pillPadY*c.scale
	if textColor == tagAdminText || textColor == tagCreatorText {
		fillRoundedRect(c.img, x, y, width, height, height/2, withAlpha(textColor, 0.15))
	}
	face := c.faceFor(fontSize, fontCJK)
	if face != nil {
		metrics := face.Metrics()
		baseline := y + (height-int(float64(metrics.Ascent+metrics.Descent)/64))/2 + int(float64(metrics.Ascent)/64)
		c.drawRunes(c.img, text, x+pillPadX*c.scale, baseline, fontSize, textColor, false)
	}
	return width
}

// accentBlockBG 画回复块背景：accent 色 14% 底 + 左边实色胶囊条。
func (c *canvas) accentBlockBG(x, y, w, h, barW int, accent color.RGBA) {
	fillRoundedRect(c.img, x, y, w, h, blockRadius*c.scale, withAlpha(accent, 0.14))
	barY, barH := barInset*c.scale, h-2*barInset*c.scale
	if barH > 0 {
		fillRoundedRect(c.img, x, y+barY, barW, barH, barW/2, accent)
	}
}

// fitImage 把图按比例放进 w×h 的框（不放大），返回缩放后的图。
func fitImage(src image.Image, w, h int) image.Image {
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return src
	}
	scale := math.Min(float64(w)/float64(bounds.Dx()), float64(h)/float64(bounds.Dy()))
	if scale >= 1 {
		return src
	}
	return resizeFit(src, max(1, int(float64(bounds.Dx())*scale)), max(1, int(float64(bounds.Dy())*scale)))
}

// drawImageAt 把图按 alpha 贴到 (x, y)。
func drawImageAt(dst *image.RGBA, src image.Image, x, y int) {
	bounds := src.Bounds()
	rect := image.Rect(x, y, x+bounds.Dx(), y+bounds.Dy()).Intersect(dst.Bounds())
	if rect.Empty() {
		return
	}
	draw.Draw(dst, rect, src, bounds.Min.Add(rect.Min.Sub(image.Pt(x, y))), draw.Over)
}

// resizeCover 缩放图像以铺满 w×h，较长的一边以中心为准裁掉两头。
func resizeCover(src image.Image, w, h int) *image.RGBA {
	bounds := src.Bounds()
	scale := math.Max(float64(w)/float64(bounds.Dx()), float64(h)/float64(bounds.Dy()))
	cropW := min(max(int(math.Round(float64(w)/scale)), 1), bounds.Dx())
	cropH := min(max(int(math.Round(float64(h)/scale)), 1), bounds.Dy())
	crop := image.Rect(0, 0, cropW, cropH).
		Add(image.Pt(bounds.Min.X+(bounds.Dx()-cropW)/2, bounds.Min.Y+(bounds.Dy()-cropH)/2))
	target := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(target, target.Bounds(), src, crop, draw.Src, nil)
	return target
}

// resizeFit 把图像缩小到能放进 w×h，保持宽高比；本来就更小的不放大。
func resizeFit(src image.Image, w, h int) *image.RGBA {
	bounds := src.Bounds()
	scale := math.Min(float64(w)/float64(bounds.Dx()), float64(h)/float64(bounds.Dy()))
	if scale >= 1 {
		scale = 1
	}
	tw := max(1, int(math.Round(float64(bounds.Dx())*scale)))
	th := max(1, int(math.Round(float64(bounds.Dy())*scale)))
	target := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.CatmullRom.Scale(target, target.Bounds(), src, bounds, draw.Src, nil)
	return target
}
