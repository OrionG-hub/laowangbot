package sum

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
)

// sum 输出的是 Telegram HTML，不是 Markdown，所以这里不碰提示词和 HTML 输出格式，
// 只在发送时把每页 HTML 转成 Rich Message 的块。转换只认输出子集：粗体、
// 可展开引用（→详情块）、项目符号列表、链接、代码；其余只留文字，内容不丢。

// sendSumPage 发一页摘要：有 Premium 且 Rich 块在限内时先发 Rich Message，
// 被拒收时回落普通 HTML；其余失败直接返回，消息不会重发。
func (s *sumService) sendSumPage(ctx context.Context, client *bot.Client, peer tg.InputPeerClass, page string, options bot.SendOptions) error {
	if client.CanSendRich() {
		if blocks := bot.HTMLToRichBlocks(page); len(blocks) > 0 && bot.RichFits(blocks) {
			_, err := client.SendRich(ctx, peer, blocks, options)
			if !bot.RichRejected(err) {
				return err
			}
			s.a.Logger.Warn("sum.rich_rejected", "error", err.Error())
		}
	}
	_, err := client.SendHTML(ctx, peer, page, options)
	return err
}

// sendSumPages 是 instant 非回复模式的分页发送：第一页编辑进命令消息，
// 其余各页回复命令消息；每页都先试 Rich Message，失败回落原有方式。
// 分页本身（HTMLPages 的切分）保持不变。
func (s *sumService) sendSumPages(ctx context.Context, inv *command.Invocation, pages []string) error {
	for index, page := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index == 0 {
			if err := s.sendSumEditPage(ctx, inv, page); err != nil {
				return err
			}
			continue
		}
		if err := s.sendSumReplyPage(ctx, inv, page); err != nil {
			return err
		}
	}
	return nil
}

// sendSumEditPage 把一页摘要编辑进命令消息，先试 Rich Message，被拒收时回落 inv.Edit。
func (s *sumService) sendSumEditPage(ctx context.Context, inv *command.Invocation, page string) error {
	if inv.Client.CanSendRich() {
		if blocks := bot.HTMLToRichBlocks(page); len(blocks) > 0 && bot.RichFits(blocks) {
			if peer, err := inv.Client.InputPeer(inv.Message.Peer); err == nil {
				if err := inv.Client.EditRich(ctx, peer, inv.Message.ID, blocks); !bot.RichRejected(err) {
					return err
				} else {
					inv.Log.Warn("sum.rich_rejected", "error", err.Error())
				}
			}
		}
	}
	return inv.Edit(ctx, page)
}

// sendSumReplyPage 把一页摘要作为新消息回复命令消息，先试 Rich Message，被拒收时回落 inv.Reply。
func (s *sumService) sendSumReplyPage(ctx context.Context, inv *command.Invocation, page string) error {
	if inv.Client.CanSendRich() {
		if blocks := bot.HTMLToRichBlocks(page); len(blocks) > 0 && bot.RichFits(blocks) {
			if peer, err := inv.Client.InputPeer(inv.Message.Peer); err == nil {
				_, err := inv.Client.SendRich(ctx, peer, blocks, bot.SendOptions{ReplyTo: inv.Message.ID})
				if !bot.RichRejected(err) {
					return err
				}
				inv.Log.Warn("sum.rich_rejected", "error", err.Error())
			}
		}
	}
	return inv.Reply(ctx, page)
}
