package render

import (
	"fmt"
	"strings"
)

// emoji 分段：名字行里的 Unicode 表情按序列切分，整段换成一张图画，
// 而不是按字画（内嵌字体没有彩色 emoji 字形，按字画只会出方框或被跳过）。
//
// 序列规则（常见情况）：
//   - 单个 emoji 基字 + 可选 FE0F + 可选肤色 +
//     （ZWJ + 基字 + 可选 FE0F + 可选肤色）*
//   - 旗帜：两个区域指示符成一对
//   - 按键帽：数字/#/* + 可选 FE0F + U+20E3
//
// 分段本身不改变渲染：没有对应图片的段退回按字画（有字形的字正常画，
// 没有的跳过），与之前行为一致；只有配到图的段才画成图。

const (
	vs16   = '\uFE0F'
	zwj    = '\u200D'
	keycap = '\u20E3'
)

// AppleLogo 是苹果 logo 的私有区字符 U+F8FF：客户端（尤其苹果设备）
// 会把它画成苹果标志，但它不是标准 emoji，Twemoji 等图库没有它，内嵌
// 字体也没有它的字形。渲染器内嵌了 Simple Icons（CC0）的苹果标志作
// mask，画名字时按名字颜色着色，不走 Twemoji 下载。
const AppleLogo = "\uf8ff"

func isEmojiBase(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2300 && r <= 0x23FF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	}
	return false
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}

func isSkinTone(r rune) bool {
	return r >= 0x1F3FB && r <= 0x1F3FF
}

func isKeycapBase(r rune) bool {
	return (r >= '0' && r <= '9') || r == '#' || r == '*'
}

// emojiSequenceAt 返回从 runes[i] 开始的 emoji 序列与消耗的 rune 数；
// 不是 emoji 序列时返回 "", 0。
func emojiSequenceAt(runes []rune, i int) (string, int) {
	r := runes[i]
	// 苹果 logo：单个私有区字符，整段画内嵌 mask。
	if r == '\uF8FF' {
		return string(r), 1
	}
	// 旗帜：两个区域指示符成一对。
	if isRegionalIndicator(r) {
		if i+1 < len(runes) && isRegionalIndicator(runes[i+1]) {
			return string(runes[i : i+2]), 2
		}
		return "", 0
	}
	// 按键帽：基字 + 可选 FE0F + U+20E3。
	if isKeycapBase(r) {
		j := i + 1
		if j < len(runes) && runes[j] == vs16 {
			j++
		}
		if j < len(runes) && runes[j] == keycap {
			return string(runes[i : j+1]), j + 1 - i
		}
		return "", 0
	}
	if !isEmojiBase(r) {
		return "", 0
	}
	j := i + 1
	if j < len(runes) && runes[j] == vs16 {
		j++
	}
	if j < len(runes) && isSkinTone(runes[j]) {
		j++
	}
	// ZWJ 链：ZWJ + 基字 + FE0F + 肤色，反复。
	for j+1 < len(runes) && runes[j] == zwj && isEmojiBase(runes[j+1]) {
		j += 2
		if j < len(runes) && runes[j] == vs16 {
			j++
		}
		if j < len(runes) && isSkinTone(runes[j]) {
			j++
		}
	}
	return string(runes[i:j]), j - i
}

type segment struct {
	emoji bool
	text  string
}

// splitSegments 把字符串切成文本段与 emoji 段；emoji 段整体画图，不拆散。
func splitSegments(s string) []segment {
	runes := []rune(s)
	var segs []segment
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			segs = append(segs, segment{text: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(runes); {
		if seq, n := emojiSequenceAt(runes, i); n > 0 {
			flush()
			segs = append(segs, segment{emoji: true, text: seq})
			i += n
			continue
		}
		text.WriteRune(runes[i])
		i++
	}
	flush()
	return segs
}

// EmojiSegments 返回字符串里所有的 emoji 段（按出现顺序，不去重），
// 供调用方下载对应的表情图。
func EmojiSegments(s string) []string {
	var out []string
	for _, seg := range splitSegments(s) {
		if seg.emoji {
			out = append(out, seg.text)
		}
	}
	return out
}

// TwemojiFileName 返回某 emoji 段对应的 Twemoji 文件名（不含 .png）：
// 码位去掉 FE0F 后转小写十六进制，以 "-" 连接。Twemoji 为 CC-BY 4.0，
// 图源 https://cdn.jsdelivr.net/gh/twitter/twemoji@14.0.2/assets/72x72/。
func TwemojiFileName(segment string) string {
	var parts []string
	for _, r := range segment {
		if r == vs16 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%x", r))
	}
	return strings.Join(parts, "-")
}
