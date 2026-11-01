package core

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
)

// helpRichList 直接用 registry 数据拼命令列表的 Rich 块：标题、命令列表项、前缀说明，
// 和 renderHelpList 是同一份内容，换了个表现形式。
func helpRichList(registry *command.Registry, prefix string) []tg.PageBlockClass {
	blocks := []tg.PageBlockClass{&tg.PageBlockHeading1{Text: bot.RichPlain("命令列表")}}
	list := &tg.PageBlockList{}
	for _, cmd := range registry.Commands() {
		usage := prefix + cmd.Name
		if cmd.Usage != "" {
			usage += " " + cmd.Usage
		}
		list.Items = append(list.Items, &tg.PageListItemBlocks{
			Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: bot.RichConcat(
				bot.RichCode(usage), bot.RichPlain(" — "+cmd.Description))}},
		})
	}
	blocks = append(blocks, list)
	var prefixParts []tg.RichTextClass
	prefixParts = append(prefixParts, bot.RichPlain("前缀: "))
	for index, value := range registry.Prefixes() {
		if index > 0 {
			prefixParts = append(prefixParts, bot.RichPlain(" "))
		}
		prefixParts = append(prefixParts, bot.RichCode(value))
	}
	blocks = append(blocks, &tg.PageBlockParagraph{Text: bot.RichConcat(prefixParts...)})
	blocks = append(blocks, &tg.PageBlockParagraph{Text: bot.RichConcat(
		bot.RichPlain("用 "), bot.RichCode(prefix+"help 命令"), bot.RichPlain(" 查看单条说明。"))})
	return blocks
}

// helpRichSingle 把单条命令的帮助转成 Rich 块。帮助正文是各命令的 Help 函数
// 生成的自由 HTML，没法从 registry 直接拼结构，所以用 sum 同款的 HTML 子集转换。
func helpRichSingle(registry *command.Registry, prefix, name string) []tg.PageBlockClass {
	return bot.HTMLToRichBlocks(renderCommandHelp(registry, prefix, name))
}

// editHelpRich 试着把帮助换成 Rich Message 发到命令消息上；没 Premium、
// 块为空、超限、被拒收时都回落原来的 EditHelp，其余失败直接返回，不丢消息。
func editHelpRich(ctx context.Context, inv *command.Invocation, blocks []tg.PageBlockClass, fallback string) error {
	if inv.Client.CanSendRich() && len(blocks) > 0 && bot.RichFits(blocks) {
		if peer, err := inv.Client.InputPeer(inv.Message.Peer); err == nil {
			if err := inv.Client.EditRich(ctx, peer, inv.Message.ID, blocks); !bot.RichRejected(err) {
				return err
			} else {
				inv.Log.Warn("core.rich_rejected", "error", err.Error())
			}
		}
	}
	return inv.EditHelp(ctx, fallback)
}
