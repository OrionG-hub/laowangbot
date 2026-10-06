package webdav

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/gotd/td/tg"
)

func help(prefix string) string {
	p := command.Escape(prefix)
	return "☁️ <b>WebDAV 文件归档</b>\n回复单条图片、视频、文件：<code>" + p + "dav</code>\n<code>" + p + "dav list</code> 全部记录（每页10条）\n<code>" + p + "dav list YYYY-MM-DD</code> 按上传日期查询\n<code>" + p + "dav next</code> / <code>" + p + "dav prev</code> / <code>" + p + "dav page 3</code>\n列表固定5分钟有效，翻页不续期；每个聊天独立。\n<code>" + p + "dav info 记录ID</code> 查看完整路径\n<code>" + p + "dav test</code> 只读检查连接\n<code>" + p + "dav config</code> 配置方式（仅收藏夹）\n普通 WebDAV（默认）：整文件上传；CloudDrive：HTTP 按 80 MB 分片，无需 Token。\n在配置面板选择上传模式；普通服务不一定支持分片。\n<code>" + p + "dav cancel</code> 取消当前上传\n仅账号本人可用；日期为北京时间，群组/频道改名后，下次上传同步重命名目录。"
}

var configValue = regexp.MustCompile(`(?s)^\S+\s+config\s+\S+\s+(.+)$`)

// Register installs the owner-only .dav command; uploads have a one-hour limit.
func Register(a *app.App) {
	s := newService(a.Root)
	a.OnClose(s.shutdown)
	a.Registry.Register(&command.Command{Name: "dav", Description: "WebDAV 文件归档", Usage: "[list|next|prev|page|info|test|config|cancel|help]", Help: help, Timeout: time.Hour, Handle: func(ctx context.Context, inv *command.Invocation) error {
		r := &request{args: inv.Args, chatID: inv.Message.ChatID, prefix: inv.Prefix, owner: inv.Trigger == nil && (inv.Message.Out || inv.Message.SenderID() == inv.Client.SelfID()), saved: inv.Message.Saved, edit: inv.Edit, reply: func(ctx context.Context) (*bot.Message, error) { return inv.Client.GetReply(ctx, inv.Message) }, download: inv.Client.DownloadTo}
		if match := configValue.FindStringSubmatch(inv.Text); len(match) > 1 {
			r.value = match[1]
		}
		r.hide = func(ctx context.Context) error { return inv.EditText(ctx, "🔐 凭据配置命令已隐藏") }
		r.name = func(message *bot.Message) string {
			switch p := message.Peer.(type) {
			case *tg.PeerChannel:
				if c, ok := inv.Client.Peers().Channel(p.ChannelID); ok {
					return c.Title
				}
			case *tg.PeerChat:
				if c, ok := inv.Client.Peers().Chat(p.ChatID); ok {
					return c.Title
				}
			case *tg.PeerUser:
				if u, ok := inv.Client.Peers().User(p.UserID); ok {
					if u.Username != "" {
						return u.Username
					}
					return u.DisplayName()
				}
			}
			return ""
		}
		r.notice = func(ctx context.Context) {
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = inv.Client.DeleteMessage(cleanup, inv.Message)
		}
		if err := s.handle(ctx, r); err != nil {
			text := err.Error()
			if errors.Is(err, context.Canceled) {
				text = "任务已取消；远端可能保留未完成文件"
			} else if errors.Is(err, context.DeadlineExceeded) {
				text = "任务或网络请求超时；远端可能保留未完成文件"
			}
			// All transport and storage errors above are deliberately replaced with safe messages.
			// Never return raw errors into the dispatcher log or expose URL credentials.
			reply, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			return inv.EditText(reply, "❌ "+text)
		}
		return nil
	}})
}
