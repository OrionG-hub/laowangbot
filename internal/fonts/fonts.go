// Package fonts 是共享的内嵌字体：Noto Sans CJK SC、Noto Sans Symbols 2
// 与 Noto Sans Math，均为 SIL OFL 1.1，许可证见同目录的 OFL 文本。
// statuscard 与 quote 渲染器共用这一份，避免 16MB 的 CJK 字体在二进制里存两份。
package fonts

import (
	_ "embed"
	"sync"

	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

//go:embed NotoSansCJKsc-Regular.otf
var cjkData []byte

//go:embed NotoSansSymbols2-Regular.ttf
var symbolsData []byte

//go:embed NotoSansMath-Regular.ttf
var mathData []byte

var (
	cjkFont     *sfnt.Font
	symbolsFont *sfnt.Font
	mathFont    *sfnt.Font
	parseOnce   sync.Once
	parseErr    error
)

func load() error {
	parseOnce.Do(func() {
		cjkFont, parseErr = opentype.Parse(cjkData)
		if parseErr == nil {
			symbolsFont, parseErr = opentype.Parse(symbolsData)
		}
		if parseErr == nil {
			mathFont, parseErr = opentype.Parse(mathData)
		}
	})
	return parseErr
}

// CJK 返回正文字体。
func CJK() (*sfnt.Font, error) {
	if err := load(); err != nil {
		return nil, err
	}
	return cjkFont, nil
}

// Symbols 返回符号回退字体：正文字体没有的字形（符号、部分 emoji）
// 到这里找。
func Symbols() (*sfnt.Font, error) {
	if err := load(); err != nil {
		return nil, err
	}
	return symbolsFont, nil
}

// Math 返回数学字母回退字体：花体名字（粗体/花体/双线体等数学字母
// 数字符号）到这里找，原样输出花体字形。
func Math() (*sfnt.Font, error) {
	if err := load(); err != nil {
		return nil, err
	}
	return mathFont, nil
}
