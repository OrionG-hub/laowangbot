package ai

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gotd/td/tg"

	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
)

// 这里把 Markdown 转成 Rich Message 的块（MTProto 的 PageBlock / RichText）：
// 标题是真正的一到六级标题，列表是可以嵌套的列表，代码块、引用都用对应的块，
// 不再像 HTML 那样拿粗体和缩进模拟。解析复用本包已有的 parseMarkdown，
// 所以 Rich 版和 HTML 版看到的块结构一致；表格、公式块解析器没有，
// 这类内容在 Rich 里和 HTML 里一样退成段落文字。

// MarkdownRich 把 Markdown 转成 Rich Message 的块。章节默认不折叠，
// 折叠只在 answerRich 里按 collapse 设置对长回答做开头预览。
func MarkdownRich(markdown string) []tg.PageBlockClass {
	return blocksToRich(parseMarkdown(mdCitePattern.ReplaceAllString(markdown, "")))
}

func blocksToRich(blocks []mdBlock) []tg.PageBlockClass {
	var out []tg.PageBlockClass
	for _, block := range blocks {
		switch block.kind {
		case mdRule:
			out = append(out, &tg.PageBlockDivider{})
		case mdHeading:
			out = append(out, richHeading(block.level, inlineRich(parseInline(block.text, false))))
		case mdCode:
			if strings.TrimSpace(block.text) == "" {
				continue
			}
			lang := strings.ToLower(strings.TrimSpace(block.lang))
			if lang == "math" {
				out = append(out, &tg.PageBlockMath{Source: block.text})
				continue
			}
			if !mdLanguage.MatchString(lang) {
				lang = ""
			}
			out = append(out, &tg.PageBlockPreformatted{Text: bot.RichPlain(block.text), Language: lang})
		case mdQuote:
			var inner []string
			for _, line := range block.lines {
				_, rest := quoteDepth(line)
				inner = append(inner, rest)
			}
			if converted := blocksToRich(parseMarkdown(strings.Join(inner, "\n"))); len(converted) > 0 {
				out = append(out, &tg.PageBlockBlockquoteBlocks{Blocks: converted, Caption: &tg.TextEmpty{}})
			}
		case mdList:
			out = append(out, listRich(block.items)...)
		default:
			out = append(out, &tg.PageBlockParagraph{Text: richLines(block.lines)})
		}
	}
	return out
}

func richHeading(level int, text tg.RichTextClass) tg.PageBlockClass {
	switch level {
	case 1:
		return &tg.PageBlockHeading1{Text: text}
	case 2:
		return &tg.PageBlockHeading2{Text: text}
	case 3:
		return &tg.PageBlockHeading3{Text: text}
	case 4:
		return &tg.PageBlockHeading4{Text: text}
	case 5:
		return &tg.PageBlockHeading5{Text: text}
	}
	return &tg.PageBlockHeading6{Text: text}
}

// richHeadingOf 认出标题块，返回级别和文字。
func richHeadingOf(block tg.PageBlockClass) (int, tg.RichTextClass, bool) {
	switch value := block.(type) {
	case *tg.PageBlockHeading1:
		return 1, value.Text, true
	case *tg.PageBlockHeading2:
		return 2, value.Text, true
	case *tg.PageBlockHeading3:
		return 3, value.Text, true
	case *tg.PageBlockHeading4:
		return 4, value.Text, true
	case *tg.PageBlockHeading5:
		return 5, value.Text, true
	case *tg.PageBlockHeading6:
		return 6, value.Text, true
	}
	return 0, nil, false
}

// listRich 把相邻的同类项合成一个无序或有序列表，每项是一段文字加上它的子列表。
func listRich(items []*mdItem) []tg.PageBlockClass {
	var out []tg.PageBlockClass
	for start := 0; start < len(items); {
		end := start
		for end < len(items) && items[end].ordered == items[start].ordered {
			end++
		}
		group := items[start:end]
		if !group[0].ordered {
			list := &tg.PageBlockList{}
			for _, item := range group {
				list.Items = append(list.Items, &tg.PageListItemBlocks{Blocks: itemRich(item)})
			}
			out = append(out, list)
		} else {
			list := &tg.PageBlockOrderedList{Start: group[0].number}
			for _, item := range group {
				list.Items = append(list.Items, &tg.PageListOrderedItemBlocks{Value: item.number, Blocks: itemRich(item)})
			}
			out = append(out, list)
		}
		start = end
	}
	return out
}

func itemRich(item *mdItem) []tg.PageBlockClass {
	blocks := []tg.PageBlockClass{&tg.PageBlockParagraph{Text: richLines(item.lines)}}
	return append(blocks, listRich(item.children)...)
}

// richLines 把几行文字排成一段，行间换行，行首缩进换成不换行空格。
func richLines(lines []string) tg.RichTextClass {
	var parts []tg.RichTextClass
	for index, line := range lines {
		if index > 0 {
			parts = append(parts, bot.RichPlain("\n"))
		}
		indent, rest := leadingIndent(line)
		if indent != "" {
			parts = append(parts, bot.RichPlain(indent))
		}
		parts = append(parts, inlineRich(parseInline(rest, false)))
	}
	return bot.RichConcat(parts...)
}

// inlineRich 把行内节点转成富文本。图片和 HTML 版一样只给链接：Rich Message 的图片
// 要先上传成单独的媒体块。
func inlineRich(nodes []mdInline) tg.RichTextClass {
	parts := make([]tg.RichTextClass, 0, len(nodes))
	for _, node := range nodes {
		children := func() tg.RichTextClass { return inlineRich(node.children) }
		switch node.kind {
		case mdText:
			parts = append(parts, bot.RichPlain(node.text))
		case mdCodeSpan:
			parts = append(parts, &tg.TextFixed{Text: bot.RichPlain(node.text)})
		case mdBold:
			parts = append(parts, &tg.TextBold{Text: children()})
		case mdItalic:
			parts = append(parts, &tg.TextItalic{Text: children()})
		case mdStrike:
			parts = append(parts, &tg.TextStrike{Text: children()})
		case mdUnderline:
			parts = append(parts, &tg.TextUnderline{Text: children()})
		case mdSpoiler:
			parts = append(parts, &tg.TextSpoiler{Text: children()})
		case mdLink:
			parts = append(parts, &tg.TextURL{Text: children(), URL: node.url})
		case mdImage:
			label := node.text
			if label == "" {
				label = node.url
			}
			if node.url == "" {
				parts = append(parts, bot.RichPlain(node.text))
				continue
			}
			parts = append(parts, &tg.TextURL{Text: bot.RichPlain(label), URL: node.url})
		}
	}
	return bot.RichConcat(parts...)
}

// answerFoldLength 是开启折叠时回答多长（按字符算）才折起来，短回答直接显示。
const answerFoldLength = 400

// answerRich 是 Rich Message 版的回答，一条最多 32768 个字符，不用分页。版式和 answerPages
// 一样是 Q: 问题、A: 回答：开启折叠时问题是可展开的引用，长回答只露出开头，其余收进
// 「展开全文」详情块，和 HTML 版可展开的引用一样先给个预览。来源收进详情块，模型名写在页脚。
func answerRich(question, answer string, sources []aiSource, model string, collapse bool) []tg.PageBlockClass {
	blocks := []tg.PageBlockClass{&tg.PageBlockParagraph{Text: bot.RichPlain("Q:")}}
	switch {
	case question == "":
	case collapse:
		blocks = append(blocks, &tg.PageBlockBlockquote{Collapsed: true, Text: bot.RichPlain(question), Caption: &tg.TextEmpty{}})
	default:
		blocks = append(blocks, &tg.PageBlockParagraph{Text: bot.RichPlain(question)})
	}
	blocks = append(blocks, &tg.PageBlockParagraph{Text: bot.RichPlain("A:")})
	body := MarkdownRich(answer)
	if collapse && utf8.RuneCountInString(answer) > answerFoldLength {
		// 露出开头直到第一个不是标题的块，免得预览里只有一个标题。
		shown := 0
		for shown < len(body) {
			_, _, heading := richHeadingOf(body[shown])
			shown++
			if !heading {
				break
			}
		}
		if shown < len(body) {
			body = append(body[:shown:shown], &tg.PageBlockDetails{Title: bot.RichPlain("展开全文"), Blocks: body[shown:]})
		}
	}
	blocks = append(blocks, body...)
	if len(sources) > 0 {
		sources = sources[:min(8, len(sources))]
		list := &tg.PageBlockOrderedList{Start: 1}
		for index, item := range sources {
			title := item.Title
			if title == "" {
				title = item.URL
			}
			list.Items = append(list.Items, &tg.PageListOrderedItemBlocks{Value: index + 1,
				Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: &tg.TextURL{Text: bot.RichPlain(title), URL: item.URL}}}})
		}
		blocks = append(blocks, &tg.PageBlockDetails{Title: bot.RichPlain(fmt.Sprintf("来源（%d）", len(sources))), Blocks: []tg.PageBlockClass{list}})
	}
	if model != "" {
		blocks = append(blocks, &tg.PageBlockFooter{Text: &tg.TextItalic{Text: bot.RichPlain("🍀Powered by " + model)}})
	}
	return blocks
}

// deliverRich 和 deliverAnswer 一样把回答作为新消息发出、再删掉命令，只是整个回答是一条 Rich Message。
func deliverRich(ctx context.Context, inv *command.Invocation, blocks []tg.PageBlockClass, anchor int) error {
	peer, err := inv.Client.InputPeer(inv.Message.Peer)
	if err != nil {
		return err
	}
	if _, err := inv.Client.SendRich(ctx, peer, blocks, bot.SendOptions{ReplyTo: anchor}); err != nil {
		return err
	}
	ids := []int{inv.Message.ID}
	if inv.Message.Raw != nil {
		if grouped, ok := inv.Message.Raw.GetGroupedID(); ok && grouped != 0 {
			ids = ids[:0]
			for _, member := range albumMembers(ctx, inv.Client, inv.Message, grouped) {
				ids = append(ids, member.ID)
			}
		}
	}
	if err := inv.Client.Delete(ctx, peer, ids); err != nil {
		inv.Log.Warn("ai.delete_command_failed", "error", err.Error())
	}
	return nil
}
