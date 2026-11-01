package bot

import (
	"html"
	"regexp"
	"strings"

	"github.com/gotd/td/tg"
)

// HTMLToRichBlocks 把一小段 Telegram HTML 转成 Rich Message 的块，只认 sum 输出和
// 帮助文本里会出现的子集：段落、粗体、斜体、下划线、删除线、行内代码、链接、
// 代码块、可展开引用（→详情块）、普通引用（→引用块）、项目符号列表。
// 其余标签只留文字；标签没闭合时当在文末闭合处理；空输入返回 nil，调用方不得
// 拿空块去发 Rich Message。
func HTMLToRichBlocks(input string) []tg.PageBlockClass {
	return htmlToRichBlocks(input, 0)
}

const richHTMLMaxQuoteDepth = 8

type richHTMLState int

const (
	richHTMLNormal richHTMLState = iota
	richHTMLPre
	richHTMLQuote
)

type richHTMLParser struct {
	blocks []tg.PageBlockClass
	para   []string
	items  []string
	depth  int
}

func htmlToRichBlocks(input string, depth int) []tg.PageBlockClass {
	input = strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(input, "\n")
	parser := &richHTMLParser{depth: depth}
	state := richHTMLNormal
	var preLines []string
	var preLang string
	var quoteLines []string
	var quoteExpandable bool
	for index := 0; index < len(lines); {
		line := lines[index]
		switch state {
		case richHTMLPre:
			if cut, found := cutCloseTag(line, "</pre>"); found {
				preLines = append(preLines, cut)
				if block := richHTMLPreBlock(preLines, preLang); block != nil {
					parser.blocks = append(parser.blocks, block)
				}
				preLines, preLang = nil, ""
				state = richHTMLNormal
				lines[index] = line[len(cut)+len("</pre>"):]
				continue
			}
			preLines = append(preLines, line)
			index++
		case richHTMLQuote:
			if cut, found := cutCloseTag(line, "</blockquote>"); found {
				quoteLines = append(quoteLines, cut)
				if block := richHTMLQuoteBlock(quoteLines, quoteExpandable, depth); block != nil {
					parser.blocks = append(parser.blocks, block)
				}
				quoteLines, quoteExpandable = nil, false
				state = richHTMLNormal
				lines[index] = line[len(cut)+len("</blockquote>"):]
				continue
			}
			quoteLines = append(quoteLines, line)
			index++
		default:
			before, name, attrs, rest, ok := splitOpenBlockTag(line)
			if !ok {
				parser.normalLine(line)
				index++
				continue
			}
			if strings.TrimSpace(before) != "" {
				parser.normalLine(before)
			}
			parser.flushAll()
			if name == "pre" {
				state = richHTMLPre
				preLang = richHTMLPreLanguage(attrs)
			} else {
				state = richHTMLQuote
				quoteExpandable = strings.Contains(strings.ToLower(attrs), "expandable")
			}
			lines[index] = rest
		}
	}
	// 文末还没闭合的块也照常发出，内容不丢。
	if state == richHTMLPre {
		if block := richHTMLPreBlock(preLines, preLang); block != nil {
			parser.blocks = append(parser.blocks, block)
		}
	}
	if state == richHTMLQuote {
		if block := richHTMLQuoteBlock(quoteLines, quoteExpandable, depth); block != nil {
			parser.blocks = append(parser.blocks, block)
		}
	}
	parser.flushAll()
	return parser.blocks
}

// cutCloseTag 在行里找结束标签，返回标签前的内容和是否找到。
func cutCloseTag(line, tag string) (string, bool) {
	if index := strings.Index(line, tag); index >= 0 {
		return line[:index], true
	}
	return "", false
}

// splitOpenBlockTag 在行里找第一个 <pre> 或 <blockquote> 开始标签，返回标签前的
// 文字、标签名、属性和标签后的文字。找不到完整的开始标签时返回 ok=false。
func splitOpenBlockTag(line string) (before, name, attrs, rest string, ok bool) {
	pre := strings.Index(line, "<pre")
	quote := strings.Index(line, "<blockquote")
	index, name := -1, ""
	switch {
	case pre >= 0 && (quote < 0 || pre < quote):
		index, name = pre, "pre"
	case quote >= 0:
		index, name = quote, "blockquote"
	default:
		return "", "", "", "", false
	}
	after := line[index+1+len(name):]
	end := strings.IndexByte(after, '>')
	if end < 0 {
		return "", "", "", "", false
	}
	attrs = strings.TrimSpace(after[:end])
	// 标签名后面必须跟空白、> 或 /，不然只是正文里凑巧出现的 "<prexxx"。
	if attrs != "" && !strings.HasPrefix(after, " ") && !strings.HasPrefix(after, "\t") && !strings.HasPrefix(after, "/") {
		return "", "", "", "", false
	}
	return line[:index], name, strings.TrimSuffix(attrs, "/"), after[end+1:], true
}

var richHTMLPreLanguagePattern = regexp.MustCompile(`^[a-z0-9_+-]+$`)

// richHTMLPreLanguage 从 <pre> 的属性里取 language，非法时留空。
func richHTMLPreLanguage(attrs string) string {
	for _, field := range strings.Fields(attrs) {
		if key, value, ok := strings.Cut(field, "="); ok && strings.ToLower(key) == "language" {
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			value = strings.ToLower(value)
			if richHTMLPreLanguagePattern.MatchString(value) {
				return value
			}
			return ""
		}
	}
	return ""
}

func richHTMLPreBlock(lines []string, lang string) tg.PageBlockClass {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	unescaped := make([]string, len(lines))
	for index, line := range lines {
		unescaped[index] = html.UnescapeString(line)
	}
	if strings.TrimSpace(strings.Join(unescaped, "")) == "" {
		return nil
	}
	return &tg.PageBlockPreformatted{Text: RichPlain(strings.Join(unescaped, "\n")), Language: lang}
}

func richHTMLQuoteBlock(lines []string, expandable bool, depth int) tg.PageBlockClass {
	inner := strings.Join(lines, "\n")
	if strings.TrimSpace(inner) == "" {
		return nil
	}
	if depth >= richHTMLMaxQuoteDepth {
		// 嵌套太深时不再分块，只留纯文字，内容不丢。
		return &tg.PageBlockParagraph{Text: RichPlain(html.UnescapeString(stripHTMLTags(inner)))}
	}
	blocks := htmlToRichBlocks(inner, depth+1)
	if len(blocks) == 0 {
		return nil
	}
	if expandable {
		return &tg.PageBlockDetails{Title: RichPlain("展开全文"), Blocks: blocks}
	}
	return &tg.PageBlockBlockquoteBlocks{Blocks: blocks, Caption: &tg.TextEmpty{}}
}

// stripHTMLTags 只是去掉尖括号标签，留给嵌套过深时的纯文字回落用。
func stripHTMLTags(input string) string {
	var b strings.Builder
	inTag := false
	for _, r := range input {
		switch {
		case r == '<':
			inTag = true
		case r == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (p *richHTMLParser) flushPara() {
	if len(p.para) == 0 {
		return
	}
	p.blocks = append(p.blocks, &tg.PageBlockParagraph{Text: richHTMLLines(p.para)})
	p.para = nil
}

func (p *richHTMLParser) flushList() {
	if len(p.items) == 0 {
		return
	}
	list := &tg.PageBlockList{}
	for _, item := range p.items {
		list.Items = append(list.Items, &tg.PageListItemBlocks{
			Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: richHTMLInline(item)}},
		})
	}
	p.blocks = append(p.blocks, list)
	p.items = nil
}

func (p *richHTMLParser) flushAll() {
	p.flushPara()
	p.flushList()
}

// normalLine 处理普通状态下的一行：空行断段，项目符号进列表，其余进段落。
func (p *richHTMLParser) normalLine(line string) {
	if strings.TrimSpace(line) == "" {
		p.flushAll()
		return
	}
	stripped := strings.TrimLeft(line, " \t")
	if rest, ok := strings.CutPrefix(stripped, "• "); ok {
		p.flushPara()
		p.items = append(p.items, rest)
		return
	}
	if rest, ok := strings.CutPrefix(stripped, "- "); ok {
		p.flushPara()
		p.items = append(p.items, rest)
		return
	}
	p.flushList()
	p.para = append(p.para, line)
}

// richHTMLLines 把几行文字排成一段，行间换行。
func richHTMLLines(lines []string) tg.RichTextClass {
	var parts []tg.RichTextClass
	for index, line := range lines {
		if index > 0 {
			parts = append(parts, RichPlain("\n"))
		}
		parts = append(parts, richHTMLInline(line))
	}
	return RichConcat(parts...)
}

type richInlineFrame struct {
	kind  string
	url   string
	parts []tg.RichTextClass
}

// richHTMLInline 把一行 HTML 转成行内富文本：粗体、斜体、下划线、删除线、
// 行内代码、剧透、链接（只认 http、https、tg），其余标签只留文字；
// 标签不配对时当在行末闭合处理。
func richHTMLInline(line string) tg.RichTextClass {
	stack := []richInlineFrame{{}}
	add := func(text tg.RichTextClass) {
		top := &stack[len(stack)-1]
		top.parts = append(top.parts, text)
	}
	for index := 0; index < len(line); {
		if line[index] != '<' {
			end := strings.IndexByte(line[index:], '<')
			var text string
			if end < 0 {
				text, index = line[index:], len(line)
			} else {
				text, index = line[index:index+end], index+end
			}
			if text != "" {
				add(RichPlain(html.UnescapeString(text)))
			}
			continue
		}
		end := strings.IndexByte(line[index:], '>')
		if end < 0 {
			add(RichPlain("<"))
			index++
			continue
		}
		raw := line[index+1 : index+end]
		index += end + 1
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if raw == "br" || raw == "br/" {
			add(RichPlain("\n"))
			continue
		}
		if strings.HasPrefix(raw, "/") {
			kind := richInlineKind(strings.TrimSpace(raw[1:]))
			if kind != "" && len(stack) > 1 && stack[len(stack)-1].kind == kind {
				frame := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				add(richInlineWrap(frame))
			}
			continue
		}
		name, attrs, _ := strings.Cut(raw, " ")
		name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "/"))
		kind := richInlineKind(name)
		if kind == "" {
			continue
		}
		frame := richInlineFrame{kind: kind}
		if kind == "link" {
			frame.url = richInlineHref(attrs)
			if frame.url == "" {
				frame.kind = "span"
			}
		}
		stack = append(stack, frame)
	}
	for len(stack) > 1 {
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		add(richInlineWrap(frame))
	}
	return RichConcat(stack[0].parts...)
}

// richInlineKind 把标签名换成行内格式，不知道的标签返回 ""（只留文字）。
func richInlineKind(name string) string {
	switch strings.ToLower(name) {
	case "b", "strong":
		return "bold"
	case "i", "em":
		return "italic"
	case "u", "ins":
		return "underline"
	case "s", "strike", "del":
		return "strike"
	case "code":
		return "fixed"
	case "tg-spoiler":
		return "spoiler"
	case "a":
		return "link"
	}
	return ""
}

func richInlineWrap(frame richInlineFrame) tg.RichTextClass {
	children := RichConcat(frame.parts...)
	switch frame.kind {
	case "bold":
		return &tg.TextBold{Text: children}
	case "italic":
		return &tg.TextItalic{Text: children}
	case "underline":
		return &tg.TextUnderline{Text: children}
	case "strike":
		return &tg.TextStrike{Text: children}
	case "fixed":
		return &tg.TextFixed{Text: children}
	case "spoiler":
		return &tg.TextSpoiler{Text: children}
	case "link":
		return &tg.TextURL{Text: children, URL: frame.url}
	}
	return children
}

// richInlineHref 从 <a> 的属性里取 href，只认 http、https、tg 开头的链接。
func richInlineHref(attrs string) string {
	attrs = strings.TrimSpace(attrs)
	for _, field := range strings.Fields(attrs) {
		if key, value, ok := strings.Cut(field, "="); ok && strings.ToLower(key) == "href" {
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			lower := strings.ToLower(value)
			if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "tg://") {
				return value
			}
			return ""
		}
	}
	return ""
}
