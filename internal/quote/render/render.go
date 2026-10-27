// Package render 是 yvlu 的本地语录渲染器：输入消息列表，输出 PNG。
//
// 它替代原来发往远程 quote 服务的路径：远程服务会丢弃 senderTag，
// 群成员标签和管理员头衔永远画不出来；本地渲染把标签 pill、
// 名字旁的表情状态都画出来。只用 golang.org/x/image，与 statuscard
// 同一套绘图路子，不引入 canvas/sharp/Node 等重型依赖。
//
// v1 范围：深色圆角气泡、圆形头像（无头像时渐变首字）、名字行
// （彩色名字 + 右端标签 pill + 表情状态图）、正文（换行与粗体、
// 斜体、链接、删除线、下划线、code、行内自定义表情）、回复块、
// 转发 label。媒体图片、语音/文件/音频行、TGS 动画不在 v1 内：
// 含这些的消息仍走远程渲染兜底。
package render

import (
	"bytes"
	_ "embed"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"

	"github.com/OrionG-hub/laowangbot/internal/fonts"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Author 是消息作者：名字、头像、名字旁的表情状态图（nil 表示没有）。
type Author struct {
	ID          int64
	Name        string
	Avatar      image.Image
	EmojiStatus image.Image
}

// Entity 是正文里的一段格式：offset/length 用 Telegram/Bot API 的
// UTF-16 单位；CustomEmoji 是已下载好的行内表情图（nil 表示画不出，
// 此时退回按原字符画）。
type Entity struct {
	Offset      int
	Length      int
	Type        string
	URL         string
	CustomEmoji image.Image
}

// Reply 是被引用消息的回复块：名字、正文与格式。
type Reply struct {
	Name     string
	Text     string
	Entities []Entity
}

// Message 是一条要画的消息：作者、正文、格式、是否显示头像、
// 回复块、转发 label、群标签。TagRole 是标签的身份：creator（群主）、
// admin（管理员）、member（普通成员）或空（未知），决定标签 pill 的颜色。
type Message struct {
	From       Author
	Text       string
	Entities   []Entity
	ShowAvatar bool
	Reply      *Reply
	Forward    string
	Tag        string
	TagRole    string
}

//go:embed apple.png
var applePNG []byte

// appleMask 是内嵌的苹果 logo mask（Simple Icons，CC0）：黑色剪影、
// 透明底，alpha 即覆盖率；画名字时按名字颜色着色。
var appleMask = sync.OnceValue(func() image.Image {
	img, err := png.Decode(bytes.NewReader(applePNG))
	if err != nil {
		return nil
	}
	return img
})

type Options struct {
	// Format 是 "quote"（贴纸）、"image"（卡片）或 "stories"（故事卡）：
	// stories 的气泡更窄，其余相同。
	Format string
	// BubbleColor 是气泡底色，"#rrggbb"，空时用默认深色。
	BubbleColor string
	// Scale 是输出倍率，默认 2（与原来远程 payload 的 Scale 一致）。
	Scale int
	// NameEmojis 是名字类文本（作者名、标签、转发 label、回复名）里
	// emoji 段对应的图，键为 emoji 段原文；没有图的段退回按字画。
	NameEmojis map[string]image.Image
}

// 以下尺寸都是逻辑单位，实际输出像素 = 逻辑单位 × Scale。
const (
	maxBubbleW  = 512
	storiesW    = 320
	minBubbleW  = 100
	padX        = 16
	padY        = 12
	blockGap    = 5
	msgGap      = 10
	headerGap   = 8
	maxHeader   = 300
	avatarD     = 50
	avatarGap   = 10
	bubbleRad   = 25
	nameSize    = 18
	textSize    = 24
	tagSize     = 13
	replyNameSz = 16
	replyTextSz = 16
	fwdSize     = 13
	statusSize  = 20
	statusGap   = 3
	inlineEmoji = 26
	canvasMarg  = 6
	pillPadX    = 8
	pillPadY    = 3
	blockPadL   = 10
	blockPadR   = 10
	blockPadY   = 6
	blockGapIn  = 3
	blockRadius = 7
	barInset    = 2
	codePad     = 4
	codePadY    = 2
	codeRadius  = 4
	italicShear = 0.22
)

// nameColors 是暗色背景上的名字配色（TeleBox 的配色，id % 7）。
var nameColors = []color.RGBA{
	{R: 0xFF, G: 0x8E, B: 0x86, A: 255},
	{R: 0xFF, G: 0xA3, B: 0x57, A: 255},
	{R: 0xB1, G: 0x8F, B: 0xFF, A: 255},
	{R: 0x4D, G: 0xD6, B: 0xBF, A: 255},
	{R: 0x45, G: 0xE8, B: 0xD1, A: 255},
	{R: 0x7A, G: 0xC9, B: 0xFF, A: 255},
	{R: 0xFF, G: 0x7F, B: 0xD5, A: 255},
}

// avatarGradients 是无头像时的渐变首字配色（TeleBox 的配色，id % 7）。
var avatarGradients = [][2]color.RGBA{
	{{R: 0xFF, G: 0x88, B: 0x5E, A: 255}, {R: 0xFF, G: 0x51, B: 0x6A, A: 255}},
	{{R: 0xFF, G: 0xCD, B: 0x6A, A: 255}, {R: 0xFF, G: 0xA8, B: 0x5C, A: 255}},
	{{R: 0xE0, G: 0xA2, B: 0xF3, A: 255}, {R: 0xD6, G: 0x69, B: 0xED, A: 255}},
	{{R: 0xA0, G: 0xDE, B: 0x7E, A: 255}, {R: 0x54, G: 0xCB, B: 0x68, A: 255}},
	{{R: 0x53, G: 0xED, B: 0xD6, A: 255}, {R: 0x28, G: 0xC9, B: 0xB7, A: 255}},
	{{R: 0x72, G: 0xD5, B: 0xFD, A: 255}, {R: 0x2A, G: 0x9E, B: 0xF1, A: 255}},
	{{R: 0xFF, G: 0xA8, B: 0xA8, A: 255}, {R: 0xFF, G: 0x71, B: 0x9A, A: 255}},
}

var linkTint = color.RGBA{R: 0x7A, G: 0xC9, B: 0xFF, A: 255}
var textTint = color.RGBA{R: 255, G: 255, B: 255, A: 255}

// 字体回退链的序号：正文 → 符号 → 数学字母。
const (
	fontCJK = iota
	fontSym
	fontMath
)

type faceKey struct {
	size float64
	idx  int
}

// canvas 是渲染中的画布与字体缓存。
type canvas struct {
	img        *image.RGBA
	scale      int
	cjk        *sfnt.Font
	sym        *sfnt.Font
	math       *sfnt.Font
	buf        *sfnt.Buffer
	faces      map[faceKey]font.Face
	nameEmojis map[string]image.Image
}

func (c *canvas) faceFor(size float64, idx int) font.Face {
	key := faceKey{size: size, idx: idx}
	if face, ok := c.faces[key]; ok {
		return face
	}
	f := c.cjk
	switch idx {
	case fontSym:
		f = c.sym
	case fontMath:
		f = c.math
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	c.faces[key] = face
	return face
}

func (c *canvas) faceForRune(size float64, r rune) font.Face {
	if glyph, _ := c.cjk.GlyphIndex(c.buf, r); glyph != 0 {
		return c.faceFor(size, fontCJK)
	}
	if c.sym != nil {
		if glyph, _ := c.sym.GlyphIndex(c.buf, r); glyph != 0 {
			return c.faceFor(size, fontSym)
		}
	}
	if c.math != nil {
		if glyph, _ := c.math.GlyphIndex(c.buf, r); glyph != 0 {
			return c.faceFor(size, fontMath)
		}
	}
	return c.faceFor(size, fontCJK)
}

// hasGlyph 报告三种内嵌字体里是否有这个字的字形；都没有时画出来只会是
// 方框，调用方应直接跳过该字而不是画方框。
func (c *canvas) hasGlyph(r rune) bool {
	if glyph, _ := c.cjk.GlyphIndex(c.buf, r); glyph != 0 {
		return true
	}
	if c.sym != nil {
		if glyph, _ := c.sym.GlyphIndex(c.buf, r); glyph != 0 {
			return true
		}
	}
	if c.math != nil {
		if glyph, _ := c.math.GlyphIndex(c.buf, r); glyph != 0 {
			return true
		}
	}
	return false
}

// measure 是字符串的像素宽（逐字 fallback 字体，忽略 kerning；三种字体都
// 没有的字形不占宽，与 drawRunes 的跳过行为一致；有对应图片的 emoji 段
// 按图片宽（字号）计）。
func (c *canvas) measure(text string, size float64) int {
	return c.measureWidth(text, size).Round()
}

func (c *canvas) measureWidth(text string, size float64) fixed.Int26_6 {
	width := fixed.Int26_6(0)
	for _, seg := range splitSegments(text) {
		if seg.emoji {
			if seg.text == AppleLogo {
				// 苹果 logo 用内嵌 mask 画，按字号占宽。
				width += fixed.I(int(size + 0.5))
				continue
			}
			if _, ok := c.nameEmojis[seg.text]; ok {
				width += fixed.I(int(size + 0.5))
				continue
			}
		}
		for _, r := range seg.text {
			if !c.hasGlyph(r) {
				continue
			}
			width += font.MeasureString(c.faceForRune(size, r), string(r))
		}
	}
	return width
}

// lineThickness 是下划线/删除线的粗细（输出像素）。
func lineThickness(scale int) int {
	return max(1, scale)
}

// Render 把消息列表画成一张 PNG。返回 PNG 字节。
func Render(messages []Message, opts Options) ([]byte, error) {
	if len(messages) == 0 {
		return nil, errors.New("render: no messages")
	}
	if len(messages) > 5 {
		return nil, errors.New("render: too many messages")
	}
	scale := opts.Scale
	if scale < 1 {
		scale = 2
	}
	if scale > 4 {
		return nil, errors.New("render: scale too large")
	}
	cjk, err := fonts.CJK()
	if err != nil {
		return nil, err
	}
	sym, err := fonts.Symbols()
	if err != nil {
		return nil, err
	}
	math, err := fonts.Math()
	if err != nil {
		return nil, err
	}
	bubble := hex(opts.BubbleColor, color.RGBA{R: 0x1b, G: 0x14, B: 0x29, A: 255})
	maxW := maxBubbleW
	if opts.Format == "stories" {
		maxW = storiesW
	}
	maxBubblePx := maxW * scale
	innerMax := maxBubblePx - 2*padX*scale

	c := &canvas{scale: scale, cjk: cjk, sym: sym, math: math, buf: &sfnt.Buffer{}, faces: map[faceKey]font.Face{}, nameEmojis: opts.NameEmojis}
	defer func() {
		for _, f := range c.faces {
			f.Close()
		}
	}()
	blocks := make([]*block, 0, len(messages))
	maxBlockW := 0
	totalH := 0
	for _, msg := range messages {
		blk := layoutMessage(c, msg, bubble, innerMax, maxBubblePx, scale)
		blocks = append(blocks, blk)
		if blk.width > maxBlockW {
			maxBlockW = blk.width
		}
		totalH += blk.height
	}
	totalH += msgGap * scale * (len(blocks) - 1)
	margin := canvasMarg * scale
	width := margin*2 + maxBlockW
	height := margin*2 + totalH
	if width > 4096 || height > 16384 || int64(width)*int64(height) > 20000000 {
		return nil, errors.New("render: canvas too large")
	}
	c.img = image.NewRGBA(image.Rect(0, 0, width, height))
	y := margin
	for _, blk := range blocks {
		drawBlock(c, blk, margin, y, scale)
		y += blk.height + msgGap*scale
	}
	if opts.Format == "stories" {
		story := image.NewRGBA(image.Rect(0, 0, 720, 1280))
		fillRect(story, 0, 0, 720, 1280, bubble)
		fitted := resizeFit(c.img, 672, 1232)
		drawImageAt(story, fitted, (720-fitted.Bounds().Dx())/2, (1280-fitted.Bounds().Dy())/2)
		c.img = story
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, c.img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// block 是一条消息排好的版：气泡尺寸、头像、各内容块。
type block struct {
	width      int // 含头像列
	height     int
	bubbleW    int
	bubbleH    int
	bubbleCol  color.RGBA
	showAvatar bool
	avatar     image.Image
	nameColor  color.RGBA
	header     *headerBlock
	forward    string
	forwardW   int
	reply      *replyBlock
	textLines  []line
}

type headerBlock struct {
	name     string
	nameW    int
	status   image.Image
	tag      string
	tagW     int
	tagRole  string
	height   int
	baseline int
}

type replyBlock struct {
	name      string
	nameColor color.RGBA
	lines     []line
	width     int
	height    int
	baseline  int
}

// truncateName 把名字截到 maxW 内，放不下加省略号。
// truncateName 把名字截到 maxW 内，放不下加省略号；emoji 段整段取舍，
// 不从序列中间截断。
func (c *canvas) truncateName(name string, maxW int, sizePx float64) string {
	if c.measure(name, sizePx) <= maxW {
		return name
	}
	var out strings.Builder
	width := fixed.Int26_6(0)
	ellipsis := c.measureWidth("…", sizePx)
	truncated := false
	for _, seg := range splitSegments(name) {
		if seg.emoji {
			advance := c.measureWidth(seg.text, sizePx)
			if (width + advance + ellipsis).Round() > maxW {
				truncated = true
				break
			}
			out.WriteString(seg.text)
			width += advance
			continue
		}
		cut := false
		for _, r := range seg.text {
			advance := c.measureWidth(string(r), sizePx)
			if (width + advance + ellipsis).Round() > maxW {
				truncated = true
				cut = true
				break
			}
			out.WriteRune(r)
			width += advance
		}
		if cut {
			break
		}
	}
	if out.Len() == 0 {
		return "…"
	}
	if truncated {
		return out.String() + "…"
	}
	return out.String()
}

// layoutMessage 排一条消息的版：先按最宽换行，再按实际内容收窄气泡。
func layoutMessage(c *canvas, msg Message, bubbleCol color.RGBA, innerMax, maxBubblePx, scale int) *block {
	blk := &block{nameColor: nameColor(msg.From.ID), bubbleCol: bubbleCol}
	namePx := float64(nameSize * scale)
	textPx := float64(textSize * scale)
	tagPx := float64(tagSize * scale)
	fwdPx := float64(fwdSize * scale)
	replyNamePx := float64(replyNameSz * scale)
	replyTextPx := float64(replyTextSz * scale)

	contentW := 0

	// 名字行：名字先让位，标签永远可见。花体字由数学字母字体原样输出。
	if msg.From.Name != "" {
		header := &headerBlock{name: msg.From.Name, status: msg.From.EmojiStatus, tag: msg.Tag, tagRole: msg.TagRole}
		statusW := 0
		if header.status != nil {
			statusW = statusSize*scale + statusGap*scale
		}
		tagW := 0
		if header.tag != "" {
			header.tag = c.truncateName(header.tag, min(innerMax/2, maxHeader*scale/2)-2*pillPadX*scale, tagPx)
			tagW = c.pillWidth(header.tag, tagPx) + headerGap*scale
		}
		avail := min(innerMax, maxHeader*scale) - statusW - tagW
		if avail < 40*scale {
			avail = 40 * scale
		}
		header.name = c.truncateName(header.name, avail, namePx)
		header.nameW = c.measure(header.name, namePx)
		header.tagW = tagW
		nameLH, nameBaseline := c.lineMetrics(namePx)
		header.height = nameLH
		header.baseline = nameBaseline
		if statusBox := statusSize * scale; statusBox > header.height {
			header.height = statusBox
		}
		if header.tag != "" {
			if pillH := int(tagPx) + 2*pillPadY*scale; pillH > header.height {
				header.height = pillH
			}
		}
		blk.header = header
		headerW := header.nameW + statusW
		if header.tag != "" {
			headerW += tagW
		}
		contentW = max(contentW, headerW)
	}

	// 转发 label：名字前加 "Forwarded from "，粗体 accent 色。
	if msg.Forward != "" {
		blk.forward = "Forwarded from " + msg.Forward
		blk.forwardW = c.measure(blk.forward, fwdPx)
		if blk.forwardW > innerMax {
			blk.forward = c.truncateName(blk.forward, innerMax, fwdPx)
			blk.forwardW = c.measure(blk.forward, fwdPx)
		}
		contentW = max(contentW, blk.forwardW)
	}

	// 回复块：名字 + 最多两行正文，accent 色由回复者名字哈希。
	if msg.Reply != nil {
		reply := &replyBlock{name: msg.Reply.Name, nameColor: colorForName(msg.Reply.Name)}
		replyTextW := innerMax - (blockPadL+blockPadR)*scale
		if replyTextW < 40*scale {
			replyTextW = 40 * scale
		}
		reply.name = c.truncateName(reply.name, replyTextW, replyNamePx)
		nameW := c.measure(reply.name, replyNamePx)
		lines := c.wrapParagraphs(msg.Reply.Text, msg.Reply.Entities, replyTextW, replyTextPx, inlineEmoji*scale, linkTint, textTint)
		lines = c.truncateLines(lines, 2, replyTextW, replyTextPx)
		textW := 0
		for _, ln := range lines {
			textW = max(textW, ln.width)
		}
		reply.lines = lines
		reply.width = max(nameW, textW) + (blockPadL+blockPadR)*scale
		nameLH, nameBaseline := c.lineMetrics(replyNamePx)
		reply.baseline = nameBaseline
		reply.height = blockPadY*2*scale + nameLH
		if len(lines) > 0 {
			reply.height += blockGapIn * scale
			for _, ln := range lines {
				reply.height += ln.height
			}
		}
		blk.reply = reply
		contentW = max(contentW, reply.width)
	}

	// 正文：按最宽换行，气泡按实际最宽行收窄。
	blk.textLines = c.wrapParagraphs(msg.Text, msg.Entities, innerMax, textPx, inlineEmoji*scale, linkTint, textTint)
	textW := 0
	for _, ln := range blk.textLines {
		textW = max(textW, ln.width)
	}
	contentW = max(contentW, textW)

	blk.bubbleW = min(maxBubblePx, max(minBubbleW*scale, contentW+2*padX*scale))
	blk.bubbleH = 2 * padY * scale
	blocks := 0
	if blk.forward != "" {
		fwdLH, _ := c.lineMetrics(fwdPx)
		blk.bubbleH += fwdLH
		blocks++
	}
	if blk.header != nil {
		blk.bubbleH += blk.header.height
		blocks++
	}
	if blk.reply != nil {
		blk.bubbleH += blk.reply.height
		blocks++
	}
	if len(blk.textLines) > 0 {
		for _, ln := range blk.textLines {
			blk.bubbleH += ln.height
		}
		blocks++
	}
	if blocks > 1 {
		blk.bubbleH += blockGap * scale * (blocks - 1)
	}

	// 头像列：显示头像时气泡右移，头像与气泡底部对齐。
	blk.showAvatar = msg.ShowAvatar
	blk.width = blk.bubbleW
	blk.height = blk.bubbleH
	if blk.showAvatar {
		if msg.From.Avatar != nil {
			blk.avatar = msg.From.Avatar
		} else {
			blk.avatar = gradientAvatar(c, msg.From.Name, msg.From.ID, avatarD*scale)
		}
		blk.width += (avatarD + avatarGap) * scale
		if blk.height < avatarD*scale {
			blk.height = avatarD * scale
		}
	}
	return blk
}

// drawBlock 把排好的消息画到画布上：气泡、转发、名字行、回复块、正文、头像。
func drawBlock(c *canvas, blk *block, blockX, blockY, scale int) {
	bubbleX := blockX
	if blk.showAvatar {
		bubbleX += (avatarD + avatarGap) * scale
		if blk.avatar != nil {
			avatar := circleMask(blk.avatar, avatarD*scale)
			drawImageAt(c.img, avatar, blockX, blockY+blk.height-avatarD*scale)
		}
	}
	bubble := blk.bubbleCol
	fillRoundedRect(c.img, bubbleX, blockY, blk.bubbleW, blk.bubbleH, bubbleRad*scale, bubble)
	y := blockY + padY*scale
	innerX := bubbleX + padX*scale

	if blk.forward != "" {
		fwdPx := float64(fwdSize * scale)
		fwdLH, fwdBaseline := c.lineMetrics(fwdPx)
		c.drawRunes(c.img, blk.forward, innerX, y+fwdBaseline, fwdPx, blk.nameColor, true)
		y += fwdLH + blockGap*scale
	}

	if blk.header != nil {
		header := blk.header
		namePx := float64(nameSize * scale)
		c.drawRunes(c.img, header.name, innerX, y+header.baseline, namePx, blk.nameColor, true)
		cursor := innerX + header.nameW
		if header.status != nil {
			fitted := fitImage(header.status, statusSize*scale, statusSize*scale)
			bounds := fitted.Bounds()
			drawImageAt(c.img, fitted, cursor+statusGap*scale, y+(header.height-bounds.Dy())/2)
			cursor += statusSize*scale + statusGap*scale
		}
		if header.tag != "" {
			tagPx := float64(tagSize * scale)
			pillH := int(tagPx) + 2*pillPadY*scale
			pillX := bubbleX + blk.bubbleW - padX*scale - (header.tagW - headerGap*scale)
			c.drawPill(pillX, y+(header.height-pillH)/2, header.tag, tagPx, tagTextColor(header.tagRole))
		}
		y += header.height + blockGap*scale
	}

	if blk.reply != nil {
		reply := blk.reply
		barW := int(3.5 * float64(scale))
		c.accentBlockBG(innerX, y, reply.width, reply.height, barW, reply.nameColor)
		rx := innerX + blockPadL*scale
		ry := y + blockPadY*scale
		replyNamePx := float64(replyNameSz * scale)
		c.drawRunes(c.img, reply.name, rx, ry+reply.baseline, replyNamePx, reply.nameColor, true)
		replyNameLH, _ := c.lineMetrics(replyNamePx)
		ry += replyNameLH
		ry += blockGapIn * scale
		replyTextPx := float64(replyTextSz * scale)
		for _, ln := range reply.lines {
			c.drawLine(rx, ry, ln, replyTextPx)
			ry += ln.height
		}
		y += reply.height + blockGap*scale
	}

	textPx := float64(textSize * scale)
	for _, ln := range blk.textLines {
		c.drawLine(innerX, y, ln, textPx)
		y += ln.height
	}
}
