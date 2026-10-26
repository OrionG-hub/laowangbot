package render

import (
	"image"
	"image/color"
	"math"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// cell 是排版的最小单位：一个字形、一个空格，或一张行内表情图。
type cellKind int

const (
	cellRune cellKind = iota
	cellSpace
	cellEmoji
)

type cell struct {
	kind  cellKind
	r     rune
	width int
	bold  bool
	// italic 整段一起做 shear，不逐字画。
	italic    bool
	underline bool
	strike    bool
	code      bool
	link      bool
	tint      color.RGBA
	emoji     image.Image
}

// line 是一行排好的 cell，width 是内容宽（不含行首行尾空格）。
type line struct {
	cells  []cell
	width  int
	height int
	// baseline 是基线相对行顶的偏移。
	baseline int
}

// utf16Units 返回每个 rune 起始的 UTF-16 单位下标：实体 offset/length
// 用的是这套单位（Bot API 格式），要先换算才能切 Go 字符串。
func utf16Units(text string) []int {
	runes := []rune(text)
	units := make([]int, len(runes))
	unit := 0
	for i, r := range runes {
		units[i] = unit
		if r >= 0x10000 {
			unit += 2
		} else {
			unit++
		}
	}
	return units
}

// covers 判断第 index 个 rune 是否被该实体覆盖。
func (e Entity) covers(units []int, index int) bool {
	if e.Length <= 0 || index >= len(units) {
		return false
	}
	start := units[index]
	return start >= e.Offset && start < e.Offset+e.Length
}

// emojiAt 返回覆盖第 index 个 rune 的自定义表情实体，没有则返回 nil。
func emojiAt(entities []Entity, units []int, index int) *Entity {
	for i := range entities {
		e := &entities[i]
		if e.Type == "custom_emoji" && e.CustomEmoji != nil && e.covers(units, index) {
			return e
		}
	}
	return nil
}

// buildCells 把一段文字 + 实体排成 cell 序列。sizePx 是字号（输出像素），
// emojiSize 是行内表情的边长，linkTint 是链接类实体的颜色。
func (c *canvas) buildCells(text string, entities []Entity, sizePx float64, emojiSize int, linkTint, baseTint color.RGBA) []cell {
	text = strings.ReplaceAll(text, "\r", "")
	runes := []rune(text)
	units := utf16Units(text)
	cells := make([]cell, 0, len(runes))
	index := 0
	for index < len(runes) {
		// 自定义表情实体整段换成一张图：只跳过同一个实体覆盖的 rune，
		// 相邻的不同表情互不干扰。
		if e := emojiAt(entities, units, index); e != nil {
			end := index
			for end < len(runes) && e.covers(units, end) {
				end++
			}
			cells = append(cells, cell{kind: cellEmoji, width: emojiSize, emoji: e.CustomEmoji})
			index = end
			continue
		}
		r := runes[index]
		kind := cellRune
		if r == ' ' || r == '\t' {
			kind = cellSpace
		}
		cl := cell{kind: kind, r: r, tint: baseTint}
		for i := range entities {
			e := &entities[i]
			if !e.covers(units, index) {
				continue
			}
			switch e.Type {
			case "bold":
				cl.bold = true
			case "italic":
				cl.italic = true
			case "underline":
				cl.underline = true
			case "strikethrough":
				cl.strike = true
			case "code", "pre":
				cl.code = true
			case "url", "text_link", "mention", "text_mention", "hashtag", "cashtag", "bot_command", "email", "phone_number":
				cl.link = true
				cl.tint = linkTint
			}
		}
		if kind == cellSpace {
			cl.width = c.measure(" ", sizePx)
		} else {
			cl.width = c.measure(string(r), sizePx)
		}
		cells = append(cells, cl)
		index++
	}
	return cells
}

// trimLine 去掉行首行尾空格，重新算宽。
func trimLine(cells []cell) []cell {
	start := 0
	for start < len(cells) && cells[start].kind == cellSpace {
		start++
	}
	end := len(cells)
	for end > start && cells[end-1].kind == cellSpace {
		end--
	}
	return cells[start:end]
}

func lineWidth(cells []cell) int {
	width := 0
	for _, cl := range cells {
		width += cl.width
	}
	return width
}

// wrapCells 把 cell 序列按 maxWidth 换行：优先在空格处断行，
// 没有空格时（中文）按字硬断；超宽的单个 cell 也照常放下。
func (c *canvas) wrapCells(cells []cell, maxWidth int, sizePx float64) []line {
	lineHeight, baseline := c.lineMetrics(sizePx)
	var lines []line
	current := []cell{}
	currentWidth := 0
	lastSpace := -1
	flush := func(list []cell) {
		trimmed := trimLine(list)
		if len(trimmed) == 0 {
			return
		}
		lines = append(lines, line{cells: trimmed, width: lineWidth(trimmed), height: lineHeight, baseline: baseline})
	}
	index := 0
	for index < len(cells) {
		cl := cells[index]
		if currentWidth+cl.width <= maxWidth || len(current) == 0 {
			current = append(current, cl)
			currentWidth += cl.width
			if cl.kind == cellSpace {
				lastSpace = len(current) - 1
			}
			index++
			continue
		}
		if lastSpace >= 0 {
			flush(current[:lastSpace])
			rest := current[lastSpace+1:]
			current = rest
			currentWidth = lineWidth(current)
			lastSpace = -1
			for j, cc := range current {
				if cc.kind == cellSpace {
					lastSpace = j
				}
			}
			continue
		}
		flush(current)
		current = nil
		currentWidth = 0
		lastSpace = -1
	}
	flush(current)
	return lines
}

// wrapParagraphs 按 \n 分段、每段换行，返回全部行。
func (c *canvas) wrapParagraphs(text string, entities []Entity, maxWidth int, sizePx float64, emojiSize int, linkTint, baseTint color.RGBA) []line {
	paragraphs := strings.Split(text, "\n")
	var lines []line
	for _, paragraph := range paragraphs {
		if paragraph == "" {
			// 空行仍占一行高，避免段落粘在一起。
			lineHeight, baseline := c.lineMetrics(sizePx)
			lines = append(lines, line{height: lineHeight, baseline: baseline})
			continue
		}
		cells := c.buildCells(paragraph, entities, sizePx, emojiSize, linkTint, baseTint)
		lines = append(lines, c.wrapCells(cells, maxWidth, sizePx)...)
	}
	return lines
}

// lineMetrics 返回字号对应的行高与基线（相对行顶）。
func (c *canvas) lineMetrics(sizePx float64) (lineHeight, baseline int) {
	face := c.faceFor(sizePx, fontCJK)
	if face == nil {
		return int(sizePx * 1.3), int(sizePx)
	}
	metrics := face.Metrics()
	ascent := float64(metrics.Ascent) / 64
	descent := float64(metrics.Descent) / 64
	lineHeight = int(math.Round(sizePx * 1.3))
	baseline = int(math.Round((float64(lineHeight)-(ascent+descent))/2 + ascent))
	return lineHeight, baseline
}

// truncateLines 最多留 maxLines 行，超出的在最后一行末尾加省略号
// （先把末尾的 cell 挪掉，直到省略号放得下）。
func (c *canvas) truncateLines(lines []line, maxLines, maxWidth int, sizePx float64) []line {
	if len(lines) <= maxLines {
		return lines
	}
	lines = lines[:maxLines]
	last := &lines[len(lines)-1]
	ellipsis := c.measure("…", sizePx)
	for len(last.cells) > 0 && last.width+ellipsis > maxWidth {
		last.width -= last.cells[len(last.cells)-1].width
		last.cells = last.cells[:len(last.cells)-1]
	}
	last.cells = append(last.cells, cell{kind: cellRune, r: '…', width: ellipsis})
	last.width += ellipsis
	return lines
}

// drawLine 在 (x, yTop) 画一行：先画 code 底色，再画字形（含斜体 shear），
// 再画行内表情，最后画下划线与删除线。
func (c *canvas) drawLine(x, yTop int, ln line, sizePx float64) {
	// code 底色：连续的 code cell 合并成一段画。
	for start := 0; start < len(ln.cells); {
		if !ln.cells[start].code {
			start++
			continue
		}
		end := start
		for end < len(ln.cells) && ln.cells[end].code {
			end++
		}
		rx := x
		for _, cl := range ln.cells[:start] {
			rx += cl.width
		}
		rw := 0
		for _, cl := range ln.cells[start:end] {
			rw += cl.width
		}
		fillRoundedRect(c.img, rx-codePad*c.scale, yTop+codePadY*c.scale, rw+2*codePad*c.scale, ln.height-2*codePadY*c.scale, codeRadius*c.scale, withAlpha(color.RGBA{R: 255, G: 255, B: 255, A: 255}, 0.10))
		start = end
	}
	// 字形与表情：斜体段单独做 shear，非斜体直接画。
	cursor := x
	for start := 0; start < len(ln.cells); {
		cl := ln.cells[start]
		if cl.kind == cellEmoji {
			if cl.emoji != nil {
				fitted := fitImage(cl.emoji, cl.width, ln.height)
				bounds := fitted.Bounds()
				drawImageAt(c.img, fitted, cursor+(cl.width-bounds.Dx())/2, yTop+(ln.height-bounds.Dy())/2)
			}
			cursor += cl.width
			start++
			continue
		}
		if cl.kind == cellSpace {
			cursor += cl.width
			start++
			continue
		}
		if !cl.italic {
			c.drawRunes(c.img, string(cl.r), cursor, yTop+ln.baseline, sizePx, cl.tint, cl.bold)
			cursor += cl.width
			start++
			continue
		}
		end := start
		for end < len(ln.cells) && ln.cells[end].italic && ln.cells[end].kind == cellRune {
			end++
		}
		runWidth := 0
		for _, rc := range ln.cells[start:end] {
			runWidth += rc.width
		}
		c.drawItalicRun(cursor, yTop, ln, ln.cells[start:end], runWidth, sizePx)
		cursor += runWidth
		start = end
	}
	// 下划线与删除线：连续同类 cell 合并成一段画。
	c.drawLineMarks(x, yTop, ln, func(cl cell) bool { return cl.underline }, yTop+ln.baseline+lineThickness(c.scale)*2)
	c.drawLineMarks(x, yTop, ln, func(cl cell) bool { return cl.strike }, yTop+ln.baseline-ln.height/3)
}

// drawLineMarks 给连续同类 cell 画一条横线（下划线或删除线）。
func (c *canvas) drawLineMarks(x, yTop int, ln line, match func(cell) bool, y int) {
	for start := 0; start < len(ln.cells); {
		if !match(ln.cells[start]) {
			start++
			continue
		}
		end := start
		for end < len(ln.cells) && match(ln.cells[end]) {
			end++
		}
		rx := x
		for _, cl := range ln.cells[:start] {
			rx += cl.width
		}
		rw := 0
		var tint color.RGBA
		for _, cl := range ln.cells[start:end] {
			rw += cl.width
			tint = cl.tint
		}
		fillRect(c.img, rx, y, rw, lineThickness(c.scale), tint)
		start = end
	}
}

// drawItalicRun 把一段斜体字先画到临时图上，再按行做 shear 贴回主图。
func (c *canvas) drawItalicRun(x, yTop int, ln line, run []cell, runWidth int, sizePx float64) {
	slack := int(math.Ceil(italicShear*float64(ln.height))) + 2
	temp := image.NewRGBA(image.Rect(0, 0, runWidth+2*slack, ln.height))
	cursor := slack
	for _, cl := range run {
		c.drawRunes(temp, string(cl.r), cursor, ln.baseline, sizePx, cl.tint, cl.bold)
		cursor += cl.width
	}
	for y := 0; y < ln.height; y++ {
		shift := int(math.Round(italicShear * float64(ln.baseline-y)))
		for tx := 0; tx < temp.Bounds().Dx(); tx++ {
			si := temp.PixOffset(tx, y)
			sa := temp.Pix[si+3]
			if sa == 0 {
				continue
			}
			dx := x - slack + tx + shift
			dy := yTop + y
			if dx < 0 || dy < 0 || dx >= c.img.Bounds().Dx() || dy >= c.img.Bounds().Dy() {
				continue
			}
			di := c.img.PixOffset(dx, dy)
			fa := float64(sa) / 255
			da := float64(c.img.Pix[di+3]) / 255
			outA := fa + da*(1-fa)
			if outA <= 0 {
				continue
			}
			for channel := 0; channel < 3; channel++ {
				c.img.Pix[di+channel] = uint8(math.Round(float64(temp.Pix[si+channel]) + float64(c.img.Pix[di+channel])*(1-fa)))
			}
			c.img.Pix[di+3] = uint8(math.Round(outA * 255))
		}
	}
}

// drawRunes 在 img 的 (x, baseline) 处逐字画一串字；bold 时横向双画
// （第二遍必须回到原 Dot 再 +1px，不能用 DrawString 推进后的 Dot）。
// 有对应图片的 emoji 段整段画成图（与字号等高，底部略低于基线），
// 苹果 logo 段用内嵌 mask 按当前 tint 着色画（客户端里它就是按名字
// 颜色显示的单色字形），没有图片的段退回按字画（有字形的字正常画，
// 没有的跳过）。
func (c *canvas) drawRunes(img *image.RGBA, text string, x, baseline int, sizePx float64, tint color.RGBA, bold bool) {
	cursor := x
	for _, seg := range splitSegments(text) {
		if seg.emoji {
			size := int(sizePx + 0.5)
			if size < 1 {
				size = 1
			}
			if seg.text == AppleLogo {
				if mask := appleMask(); mask != nil {
					fitted := fitImage(mask, size, size)
					bounds := fitted.Bounds()
					bottom := baseline + int(sizePx*0.2)
					rect := image.Rect(cursor, bottom-bounds.Dy(), cursor+bounds.Dx(), bottom)
					draw.DrawMask(img, rect, image.NewUniform(tint), image.Point{}, fitted, bounds.Min, draw.Over)
					cursor += size
					continue
				}
				// 内嵌 mask 解码失败时退回按字画（无字形则跳过）。
			} else if emojiImg, ok := c.nameEmojis[seg.text]; ok {
				fitted := fitImage(emojiImg, size, size)
				bounds := fitted.Bounds()
				bottom := baseline + int(sizePx*0.2)
				drawImageAt(img, fitted, cursor, bottom-bounds.Dy())
				cursor += size
				continue
			}
		}
		for _, r := range seg.text {
			if !c.hasGlyph(r) {
				continue
			}
			face := c.faceForRune(sizePx, r)
			if face == nil {
				continue
			}
			dot := fixed.Point26_6{X: fixed.I(cursor), Y: fixed.I(baseline)}
			drawer := font.Drawer{Dst: img, Src: image.NewUniform(tint), Face: face, Dot: dot}
			drawer.DrawString(string(r))
			if bold {
				drawer.Dot = dot
				drawer.Dot.X += fixed.I(1)
				drawer.DrawString(string(r))
			}
			cursor += c.measure(string(r), sizePx)
		}
	}
}
