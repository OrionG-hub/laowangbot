// Package bgp 实现 .bgp：查 IP、前缀或 AS 号在公网上的路由——所在前缀、起源 AS、上游 AS、
// 地区和反向解析；.bgp dns 只查反向解析。
//
// TeleBox 的 bgp 插件抓的是 bgp.tools 的前缀页和路由图。现在 bgp.tools 把这些页面一律跳转到
// 登录页（从生产主机上确认过），不登录什么也拿不到。这里改用 RIPE NCC 的 RIPEstat Data API：
// 公开、不用登录、返回 JSON。代价是只有文字，不再有路由图。
package bgp

import (
	"context"
	"strings"
	"time"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/commands/kit"
)

// Register 注册 .bgp。不存东西，每次现查。
func Register(a *app.App) {
	// 查询本身最多 lookupBudget（20 秒），剩下的留给改消息。
	a.Registry.Register(&command.Command{Name: "bgp", Description: "查询 IP 的 BGP 路由",
		Usage: "[dns] [IP|前缀|AS]", Help: help, Timeout: 30 * time.Second, Handle: handle})
}

func help(prefix string) string {
	p := command.Escape(prefix)
	return "🛰 <b>BGP 路由信息</b>\n\n查 IP、前缀或 AS 号在公网上的路由：所在前缀、起源 AS、上游 AS、地区和反向解析。\n\n" +
		"• <code>" + p + "bgp 1.1.1.1</code>\n" +
		"• <code>" + p + "bgp 2606:4700::/32</code>\n" +
		"• <code>" + p + "bgp AS13335</code>\n" +
		"• 回复一条含 IP 的消息发 <code>" + p + "bgp</code>\n" +
		"• <code>" + p + "bgp dns 8.8.8.8</code> 只查反向解析（PTR）\n\n" +
		"数据来自 RIPEstat，只有文字，没有路由图。"
}

func handle(ctx context.Context, inv *command.Invocation) error {
	args := inv.Args
	dns := len(args) > 0 && strings.EqualFold(args[0], "dns")
	if dns {
		args = args[1:]
	}
	var t target
	if text := strings.TrimSpace(strings.Join(args, " ")); text != "" {
		found, ok := parseTarget(text)
		if !ok {
			return kit.Fail("看不出要查什么，写 IP、前缀或 AS 号，例如 1.1.1.1、1.1.1.0/24、AS13335")
		}
		t = found
	} else {
		reply, err := replyMessage(ctx, inv)
		if err != nil {
			return err
		}
		if reply == nil {
			return inv.Edit(ctx, help(inv.Prefix))
		}
		found, ok := findTarget(messageText(reply))
		if !ok {
			return kit.Fail("被回复的消息里没有 IP、前缀或 AS 号")
		}
		t = found
	}
	if reason := t.reserved(); reason != "" {
		return kit.Fail(reason)
	}
	if dns {
		return handleDNS(ctx, inv, t)
	}
	if err := inv.EditText(ctx, "⏳ 正在查询 "+t.String()+"…"); err != nil {
		return err
	}
	lookupCtx, cancel := context.WithTimeout(ctx, lookupBudget)
	defer cancel()
	r, err := lookup(lookupCtx, inv.Log, t)
	if err != nil {
		return kit.Failf("BGP 数据查询失败（%s）", command.Brief(err))
	}
	return inv.Edit(ctx, render(r))
}

// handleDNS 是 .bgp dns：只查一个 IP 的 PTR 记录。RIPEstat 的反向解析只收单个地址，
// 前缀和 AS 号没法查。
func handleDNS(ctx context.Context, inv *command.Invocation, t target) error {
	if t.kind != kindIP {
		return kit.Fail("反向解析只能查单个 IP")
	}
	if err := inv.EditText(ctx, "⏳ 正在查询 "+t.String()+" 的反向解析…"); err != nil {
		return err
	}
	lookupCtx, cancel := context.WithTimeout(ctx, lookupBudget)
	defer cancel()
	ptr, err := fetchPTR(lookupCtx, t.String())
	if err != nil {
		return kit.Failf("反向解析查询失败（%s）", command.Brief(err))
	}
	return inv.Edit(ctx, renderPTR(t.String(), ptr))
}

// replyMessage 读回命令所回复的那条消息；没有回复时返回 (nil, nil)。
func replyMessage(ctx context.Context, inv *command.Invocation) (*bot.Message, error) {
	if inv.Message.ReplyToID == 0 {
		return nil, nil
	}
	reply, err := inv.Client.GetReply(ctx, inv.Message)
	if err != nil {
		return nil, kit.Failf("读不到被回复的消息（%s）", command.Brief(err))
	}
	if reply == nil {
		return nil, kit.Fail("被回复的消息已经不在了")
	}
	return reply, nil
}

// messageText 取一条消息的文字；纯文本为空时按富文本渲染。
func messageText(m *bot.Message) string {
	if m == nil {
		return ""
	}
	if m.Text != "" {
		return m.Text
	}
	return bot.RichMessageText(m.Raw)
}
