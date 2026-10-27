// Package dme deletes own messages, scrubbing eligible media before deletion.
package dme

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/commands/kit"
	"github.com/gotd/td/tg"
)

// defaultImage is shipped with the binary so releases carry the same DME image
// to every deployment instead of relying on an ignored data/ file.
//
//go:embed dme_troll_image.png
var defaultImage []byte

type dmeConfig struct {
	BatchSize     int `json:"batchSize"`
	SearchLimit   int `json:"searchLimit"`
	RetryAttempts int `json:"retryAttempts"`
}

func dmeDefaults() dmeConfig { return dmeConfig{50, 100, 3} }
func (c *dmeConfig) normalize() {
	if c.BatchSize < 5 || c.BatchSize > 100 {
		c.BatchSize = 50
	}
	if c.SearchLimit < 10 || c.SearchLimit > 500 {
		c.SearchLimit = 100
	}
	if c.RetryAttempts < 0 || c.RetryAttempts > 10 {
		c.RetryAttempts = 3
	}
}
func dmeHelp(prefix string) string {
	p := command.Escape(prefix)
	return "🗑️ <b>智能防撤回删除</b>\n批量删除：纯文本直接双向删除，媒体先替换成占位图再删除。回复删除：单条或整组均保留替换流程。\n\n<code>" + p + "dme 数量</code> 当前聊天最近 N 条自己的消息\n<code>" + p + "dme 999999</code> 当前聊天全部可找到的自己的消息\n回复 + <code>" + p + "dme</code> 自己的单条消息或整组图\n回复 + <code>" + p + "dme -r</code> 从回复位置到命令之前，仅自己的消息\n<code>" + p + "dme -a 数量</code> 或 <code>" + p + "dme --all 数量</code> 显式全局最新 N 条，含归档（普通及归档各最多 500 会话，每会话 80 条）\n<code>" + p + "dme -k 关键词</code> 当前聊天全部匹配的自己的消息\n<code>" + p + "dme -a -k 关键词</code> 全部对话（含归档）匹配的自己的消息\n关键词按连续文字包含匹配，不区分大小写；-k 后整段为关键词，-a 须放在 -k 前。不与数量、回复或 -r 混用；不扩展删除相册里未匹配的消息。为保证包含匹配完整，会分页扫描自己的历史消息。\n仅处理命令开始前的消息。转发、贴纸、网页预览及超过 48 小时的消息不替换；替换失败仍尝试删除，并报告失败。"
}

type options struct {
	count                     int
	keyword                   string
	global, span, reply, help bool
}

func parse(args []string, hasReply bool) (options, error) {
	o := options{reply: hasReply}
	if len(args) == 1 && (args[0] == "help" || args[0] == "h" || args[0] == "--help") {
		o.help = true
		return o, nil
	}
	if len(args) == 0 {
		o.help = !hasReply
		return o, nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-k", "--keyword":
			o.keyword = strings.TrimSpace(strings.Join(args[i+1:], " "))
			if o.keyword == "" || o.count > 0 || o.span || hasReply {
				return o, fmt.Errorf("关键词不能为空，且不能与回复、数量或 -r 混用；-a 请放在 -k 前")
			}
			return o, nil
		case "-a", "--all":
			if o.global {
				return o, fmt.Errorf("重复的全局参数")
			}
			o.global = true
		case "-r":
			if o.span {
				return o, fmt.Errorf("重复的范围参数")
			}
			o.span = true
		default:
			if o.count != 0 || arg == "" {
				return o, fmt.Errorf("请只指定一个正整数数量")
			}
			for _, c := range arg {
				if c < '0' || c > '9' {
					return o, fmt.Errorf("未知参数 %s", arg)
				}
			}
			n, err := strconv.Atoi(arg)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("请指定有效正整数数量")
			}
			o.count = n
		}
	}
	if hasReply {
		if o.global || o.count > 0 {
			return o, fmt.Errorf("回复模式不能与数量或全局模式混用")
		}
	} else if o.span || o.count == 0 {
		return o, fmt.Errorf("-r 必须回复消息；数量模式必须指定数量")
	}
	return o, nil
}
func Register(a *app.App) {
	cfgStore := kit.NewStore(a, "dme.json", dmeDefaults)
	var mu sync.Mutex
	active := map[string]bool{}
	a.Registry.Register(&command.Command{Name: "dme", Description: "删除自己的消息，支持关键词清理及媒体替换", Usage: "数量 | 回复 [-r] | -a 数量 | [-a] -k 关键词", Help: dmeHelp, Timeout: -1, Handle: func(ctx context.Context, inv *command.Invocation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		o, err := parse(inv.Args, inv.Message.ReplyToID > 0)
		if err != nil {
			return inv.EditText(ctx, "参数错误："+err.Error())
		}
		if o.help {
			return inv.Edit(ctx, dmeHelp(inv.Prefix))
		}
		if p := inv.Message.ReplyToPeer; p != nil && bot.PeerID(p) != inv.Message.ChatID {
			return inv.EditText(ctx, "不支持跨聊天回复，请在目标聊天中回复消息")
		}
		mu.Lock()
		if active["*"] || active[inv.Message.ChatID] || (o.global && len(active) > 0) {
			mu.Unlock()
			return inv.EditText(ctx, "已有重叠范围的 DME 任务正在执行")
		}
		key := inv.Message.ChatID
		if o.global {
			key = "*"
		}
		active[key] = true
		mu.Unlock()
		defer func() { mu.Lock(); delete(active, key); mu.Unlock() }()
		cfg, err := cfgStore.Read()
		if err != nil {
			return err
		}
		cfg.normalize()
		r, err := newRun(ctx, inv, cfg, "")
		if err != nil {
			return err
		}
		defer r.report(o.count)
		if err = r.execute(o); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			inv.Log.Error("dme.failed", slog.String("error", err.Error()))
			_, sendErr := inv.Client.SendSelf(ctx, command.Escape("DME 操作未完成："+command.Brief(err)))
			return sendErr
		}
		return nil
	}})
}

type dmeRun struct {
	ctx                                           context.Context
	inv                                           *command.Invocation
	client                                        *bot.Client
	api                                           *tg.Client
	cfg                                           dmeConfig
	peer                                          tg.InputPeerClass
	peerID                                        string
	cutoff                                        int
	imagePath                                     string
	imageData                                     []byte
	matched, deleted, failed, edited, failedEdits int
	scanFailed                                    int
	missingImage                                  bool
	replaceText                                   bool
}

func newRun(ctx context.Context, inv *command.Invocation, cfg dmeConfig, imagePath string) (*dmeRun, error) {
	p, err := inv.Client.InputPeer(inv.Message.Peer)
	if err != nil {
		return nil, err
	}
	cutoff := int(time.Now().Unix())
	if inv.Message.Raw != nil && inv.Message.Raw.Date > 0 && inv.Message.Raw.Date < cutoff {
		cutoff = inv.Message.Raw.Date
	}
	imageData := defaultImage
	if imagePath != "" {
		imageData = nil
	}
	return &dmeRun{ctx: ctx, inv: inv, client: inv.Client, api: inv.Client.API(), cfg: cfg, peer: p, peerID: bot.PeerID(inv.Message.Peer), cutoff: cutoff, imagePath: imagePath, imageData: imageData}, nil
}

// Every retry checks cancellation before issuing any mutating RPC.
func (r *dmeRun) retry(fn func() error) error {
	return kit.RetryFlood(r.ctx, r.cfg.RetryAttempts, func() error {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		return fn()
	})
}
func (r *dmeRun) own(m *tg.Message, strict bool) bool {
	from, ok := m.GetFromID()
	if !ok {
		return !strict && m.Out
	}
	u, isUser := from.(*tg.PeerUser)
	return isUser && u.UserID == r.client.SelfID()
}
func (r *dmeRun) eligible(m *tg.Message, peerID string, strict bool) bool {
	if m.ID <= 0 || bot.PeerID(m.PeerID) != peerID || !r.own(m, strict) {
		return false
	}
	if peerID == r.peerID {
		return m.ID < r.inv.Message.ID && m.Date <= r.cutoff
	}
	// Telegram dates have one-second resolution. Excluding this second in other
	// dialogs prevents a concurrent message from slipping into a global run.
	return m.Date > 0 && m.Date < r.cutoff
}
func canScrub(m *tg.Message, now int) bool {
	if m.Date > 0 && now-m.Date > 172800 {
		return false
	}
	if _, ok := m.GetFwdFrom(); ok {
		return false
	}
	switch media := m.Media.(type) {
	case *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		return false
	case *tg.MessageMediaDocument:
		if doc, ok := media.Document.(*tg.Document); ok {
			for _, a := range doc.Attributes {
				if _, sticker := a.(*tg.DocumentAttributeSticker); sticker {
					return false
				}
			}
		}
	}
	return true
}
func (r *dmeRun) scrub(peer tg.InputPeerClass, m *tg.Message) error {
	if !canScrub(m, int(time.Now().Unix())) {
		return nil
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	data := r.imageData
	var err error
	if data == nil {
		data, err = os.ReadFile(r.imagePath)
	}
	if err != nil || len(data) == 0 {
		r.failedEdits++
		r.missingImage = true
		return nil
	}
	file, err := r.client.Uploader().FromBytes(r.ctx, "dme_troll_image.png", data)
	if err == nil {
		if err = r.ctx.Err(); err != nil {
			return err
		}
		req := &tg.MessagesEditMessageRequest{Peer: peer, ID: m.ID}
		req.SetMessage("")
		req.SetMedia(&tg.InputMediaUploadedPhoto{File: file})
		_, err = r.api.MessagesEditMessage(r.ctx, req)
	}
	if r.ctx.Err() != nil {
		return r.ctx.Err()
	}
	if err != nil {
		r.failedEdits++
		return kit.Sleep(r.ctx, 500*time.Millisecond)
	}
	r.edited++
	return kit.Sleep(r.ctx, 1500*time.Millisecond)
}
func (r *dmeRun) deleteBatch(peer tg.InputPeerClass, ids []int) error {
	size := r.cfg.BatchSize
	for offset := 0; offset < len(ids); {
		batch := ids[offset:min(len(ids), offset+size)]
		err := r.retry(func() error { return r.client.Delete(r.ctx, peer, batch) })
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		if err != nil {
			if len(batch) == 1 {
				r.failed++
				offset++
			} else {
				size = max(1, len(batch)/2)
			}
		} else {
			r.deleted += len(batch)
			offset += len(batch)
			size = min(100, size+10)
			_, _ = r.api.UpdatesGetState(r.ctx)
		}
		if err := kit.Sleep(r.ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}
func (r *dmeRun) process(peer tg.InputPeerClass, batch []*tg.Message) error {
	if len(batch) == 0 {
		return nil
	}
	before := r.edited
	ids := make([]int, 0, len(batch))
	r.matched += len(batch)
	for _, m := range batch {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		_, emptyMedia := m.Media.(*tg.MessageMediaEmpty)
		if r.replaceText || (m.Media != nil && !emptyMedia) {
			if err := r.scrub(peer, m); err != nil {
				return err
			}
		}
		ids = append(ids, m.ID)
	}
	if r.edited > before {
		if err := kit.Sleep(r.ctx, time.Second); err != nil {
			return err
		}
	}
	return r.deleteBatch(peer, ids)
}
func (r *dmeRun) history(peer tg.InputPeerClass, offset, limit, maxID, minID int) ([]tg.MessageClass, error) {
	var result tg.MessagesMessagesClass
	err := r.retry(func() error {
		var err error
		result, err = r.api.MessagesGetHistory(r.ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offset, Limit: limit, MaxID: maxID, MinID: minID})
		return err
	})
	if err != nil {
		return nil, err
	}
	messages, _ := r.client.Unpack(result)
	return messages, nil
}
func (r *dmeRun) execute(o options) error {
	r.replaceText = o.reply && !o.span
	var reply *tg.Message
	if o.reply {
		found, err := r.client.GetMessages(r.ctx, r.peer, []int{r.inv.Message.ReplyToID})
		if err != nil {
			return err
		}
		for _, m := range found {
			if m.ID == r.inv.Message.ReplyToID && bot.PeerID(m.PeerID) == r.peerID && m.ID > 0 && m.ID < r.inv.Message.ID && m.Date <= r.cutoff && (o.span || r.own(m, true)) {
				reply = m
				break
			}
		}
		if reply == nil {
			return r.inv.EditText(r.ctx, "目标不存在或并非自己发送的消息，未执行删除")
		}
	}
	if err := r.retry(func() error { return r.client.Delete(r.ctx, r.peer, []int{r.inv.Message.ID}) }); err != nil {
		return err
	}
	if o.keyword != "" {
		if o.global {
			return r.keywordGlobal(o.keyword)
		}
		return r.keywordSearch(r.peer, r.peerID, r.inv.Message.ID, o.keyword)
	}
	if o.global {
		return r.global(o.count)
	}
	if o.reply && !o.span {
		return r.album(reply)
	}
	start := 0
	if reply != nil {
		start = reply.ID
	}
	return r.stream(o.count, start, o.span)
}
func (r *dmeRun) album(reply *tg.Message) error {
	batch := []*tg.Message{reply}
	if group, ok := reply.GetGroupedID(); ok && group != 0 {
		page, err := r.history(r.peer, min(reply.ID+10, r.inv.Message.ID), 20, r.inv.Message.ID, 0)
		if err != nil {
			if r.ctx.Err() != nil {
				return r.ctx.Err()
			}
			r.scanFailed++
			return r.process(r.peer, batch)
		}
		seen := map[int]bool{reply.ID: true}
		for _, v := range page {
			m, ok := v.(*tg.Message)
			if !ok || seen[m.ID] || !r.eligible(m, r.peerID, true) {
				continue
			}
			if id, ok := m.GetGroupedID(); ok && id == group {
				batch = append(batch, m)
				seen[m.ID] = true
			}
		}
		sort.Slice(batch, func(i, j int) bool { return batch[i].ID > batch[j].ID })
	}
	return r.process(r.peer, batch)
}

// Stream history using a strictly descending cursor; memory never grows with count.
func (r *dmeRun) stream(count, start int, strict bool) error {
	unlimited := strict || count == 999999
	offset, selected := r.inv.Message.ID, 0
	for unlimited || selected < count {
		page, err := r.history(r.peer, offset, min(100, r.cfg.SearchLimit), r.inv.Message.ID, max(0, start-1))
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		next := offset
		batch := []*tg.Message{}
		seen := map[int]bool{}
		for _, v := range page {
			id := v.GetID()
			if id > 0 && id < next {
				next = id
			}
			m, ok := v.(*tg.Message)
			if !ok || m.ID >= offset || m.ID < start || seen[m.ID] || !r.eligible(m, r.peerID, strict) {
				continue
			}
			if !unlimited && selected+len(batch) >= count {
				continue
			}
			seen[m.ID] = true
			batch = append(batch, m)
		}
		if next >= offset {
			return fmt.Errorf("DME history cursor did not advance")
		}
		offset = next
		sort.Slice(batch, func(i, j int) bool { return batch[i].ID > batch[j].ID })
		if len(batch) > 0 {
			selected += len(batch)
			if err := r.process(r.peer, batch); err != nil {
				return err
			}
		}
		if start > 0 && offset <= start {
			break
		}
		if err := kit.Sleep(r.ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}
func (r *dmeRun) report(requested int) {
	r.inv.Log.Info("dme.complete", slog.Int("requested", requested), slog.Int("matched", r.matched), slog.Int("deleted", r.deleted), slog.Int("edited", r.edited), slog.Int("failed", r.failed), slog.Int("failed_edits", r.failedEdits))
	if r.ctx.Err() == nil && (r.failed > 0 || r.failedEdits > 0 || r.scanFailed > 0) {
		note := ""
		if r.missingImage {
			note = " 占位图缺失或不可读，相关媒体未替换。"
		}
		_, _ = r.client.SendSelf(r.ctx, command.Escape(fmt.Sprintf("DME %s：已删除 %d 条；删除失败 %d 条；成功替换媒体 %d 条；防撤回编辑失败 %d 条；扫描失败 %d 处。%s", r.peerID, r.deleted, r.failed, r.edited, r.failedEdits, r.scanFailed, note)))
	}
}

// Global scope is opt-in. Each folder is limited to 500 entries; candidates are bounded to 80,000.
type dialogCursor struct {
	folder, date, id, count int
	peer                    tg.InputPeerClass
	done                    bool
}
type candidate struct {
	peer    tg.InputPeerClass
	key     string
	message *tg.Message
}

func (r *dmeRun) global(count int) error {
	cursors := []dialogCursor{{folder: 0, peer: &tg.InputPeerEmpty{}}, {folder: 1, peer: &tg.InputPeerEmpty{}}}
	seen := map[string]bool{}
	var candidates []candidate
	for !cursors[0].done || !cursors[1].done {
		for i := range cursors {
			c := &cursors[i]
			if c.done || c.count >= 500 {
				continue
			}
			req := &tg.MessagesGetDialogsRequest{OffsetDate: c.date, OffsetID: c.id, OffsetPeer: c.peer, Limit: min(100, 500-c.count), ExcludePinned: c.id != 0}
			req.SetFolderID(c.folder)
			var result tg.MessagesDialogsClass
			err := r.retry(func() error { var err error; result, err = r.api.MessagesGetDialogs(r.ctx, req); return err })
			if err != nil {
				if r.ctx.Err() != nil {
					return r.ctx.Err()
				}
				r.scanFailed++
				c.done = true
				continue
			}
			var dialogs []tg.DialogClass
			var messages []tg.MessageClass
			switch v := result.(type) {
			case *tg.MessagesDialogs:
				r.client.Peers().RememberUsers(v.Users)
				r.client.Peers().RememberChats(v.Chats)
				dialogs, messages = v.Dialogs, v.Messages
				c.done = true
			case *tg.MessagesDialogsSlice:
				r.client.Peers().RememberUsers(v.Users)
				r.client.Peers().RememberChats(v.Chats)
				dialogs, messages = v.Dialogs, v.Messages
				c.done = len(dialogs) < req.Limit
			default:
				c.done = true
			}
			if len(dialogs) > req.Limit {
				dialogs = dialogs[:req.Limit]
			}
			c.count += len(dialogs)
			if c.count >= 500 {
				c.done = true
			}
			for _, v := range dialogs {
				d, ok := v.(*tg.Dialog)
				if !ok {
					continue
				}
				key := bot.PeerID(d.Peer)
				if seen[key] || key == "" {
					continue
				}
				seen[key] = true
				peer, err := r.client.InputPeer(d.Peer)
				if err != nil {
					r.scanFailed++
					continue
				}
				maxID := d.TopMessage + 1
				if key == r.peerID {
					maxID = r.inv.Message.ID
				}
				page, err := r.history(peer, 0, 80, maxID, 0)
				if err != nil {
					if r.ctx.Err() != nil {
						return r.ctx.Err()
					}
					r.scanFailed++
					continue
				}
				if len(page) > 80 {
					page = page[:80]
				}
				ids := map[int]bool{}
				for _, v := range page {
					m, ok := v.(*tg.Message)
					if ok && !ids[m.ID] && r.eligible(m, key, false) {
						ids[m.ID] = true
						candidates = append(candidates, candidate{peer, key, m})
					}
				}
				if err := kit.Sleep(r.ctx, 30*time.Millisecond); err != nil {
					return err
				}
			}
			if len(dialogs) == 0 {
				c.done = true
				continue
			}
			last, ok := dialogs[len(dialogs)-1].(*tg.Dialog)
			if !ok {
				c.done = true
				continue
			}
			p, err := r.client.InputPeer(last.Peer)
			if err != nil {
				r.scanFailed++
				c.done = true
				continue
			}
			if c.id == last.TopMessage && fmt.Sprint(c.peer) == fmt.Sprint(p) {
				c.done = true
				continue
			}
			c.id, c.peer = last.TopMessage, p
			for _, v := range messages {
				if m, ok := v.(*tg.Message); ok && m.ID == c.id && bot.PeerID(m.PeerID) == bot.PeerID(last.Peer) {
					c.date = m.Date
				}
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].message, candidates[j].message
		if a.Date != b.Date {
			return a.Date > b.Date
		}
		return a.ID > b.ID
	})
	if count != 999999 && len(candidates) > count {
		candidates = candidates[:count]
	}
	// Preserve global chronological selection and edit order; contiguous peers
	// can still share a deletion batch without reordering other dialogs.
	for start := 0; start < len(candidates); {
		end := start + 1
		for end < len(candidates) && candidates[end].key == candidates[start].key && end-start < 100 {
			end++
		}
		batch := make([]*tg.Message, 0, end-start)
		for _, v := range candidates[start:end] {
			batch = append(batch, v.message)
		}
		if err := r.process(candidates[start].peer, batch); err != nil {
			return err
		}
		start = end
	}
	return nil
}
