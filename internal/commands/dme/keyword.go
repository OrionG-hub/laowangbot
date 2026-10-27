package dme

import (
	"fmt"
	"strings"
	"time"

	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/commands/kit"
	"github.com/gotd/td/tg"
)

// Search own history without a tokenized Q: Telegram word search can omit
// substring matches. Apply the literal keyword to text/captions locally.
func (r *dmeRun) keywordSearch(peer tg.InputPeerClass, key string, maxID int, keyword string) error {
	offset := 0
	needle := strings.ToLower(keyword)
	for {
		req := &tg.MessagesSearchRequest{Peer: peer, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offset, MaxID: maxID, MaxDate: r.cutoff + 1, Limit: min(100, r.cfg.SearchLimit)}
		req.SetFromID(&tg.InputPeerSelf{})
		var result tg.MessagesMessagesClass
		err := r.retry(func() error { var e error; result, e = r.api.MessagesSearch(r.ctx, req); return e })
		if err != nil {
			return err
		}
		page, _ := r.client.Unpack(result)
		if len(page) == 0 {
			return nil
		}
		next := 0
		batch := []*tg.Message{}
		seen := map[int]bool{}
		for _, v := range page {
			id := v.GetID()
			if id > 0 && (next == 0 || id < next) {
				next = id
			}
			m, ok := v.(*tg.Message)
			if !ok || seen[id] || (offset > 0 && id >= offset) || (maxID > 0 && id >= maxID) || !r.eligible(m, key, false) {
				continue
			}
			// Out alone cannot override an explicitly different author (e.g. send-as).
			if from, ok := m.GetFromID(); ok {
				u, ok := from.(*tg.PeerUser)
				if !ok || u.UserID != r.client.SelfID() {
					continue
				}
			}
			if !strings.Contains(strings.ToLower(m.Message), needle) {
				continue
			}
			seen[id] = true
			batch = append(batch, m)
		}
		if next == 0 || (offset > 0 && next >= offset) {
			return fmt.Errorf("DME keyword cursor did not advance")
		}
		offset = next
		if err := r.process(peer, batch); err != nil {
			return err
		}
		if err := kit.Sleep(r.ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

type keywordDialog struct {
	peer  tg.InputPeerClass
	key   string
	maxID int
}

// Snapshot both folders before deletion: deleting top messages while paginating
// dialogs would reorder them and could skip whole conversations.
func (r *dmeRun) keywordDialogs() ([]keywordDialog, error) {
	dialogs := []keywordDialog{}
	seen := map[string]bool{}
	for _, folder := range []int{0, 1} {
		offsetID, offsetDate := 0, 0
		offsetKey := ""
		var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
		for {
			req := &tg.MessagesGetDialogsRequest{OffsetID: offsetID, OffsetDate: offsetDate, OffsetPeer: offsetPeer, Limit: 100, ExcludePinned: offsetID != 0}
			req.SetFolderID(folder)
			var result tg.MessagesDialogsClass
			if err := r.retry(func() error { var e error; result, e = r.api.MessagesGetDialogs(r.ctx, req); return e }); err != nil {
				return nil, err
			}
			var page []tg.DialogClass
			var messages []tg.MessageClass
			done := false
			switch v := result.(type) {
			case *tg.MessagesDialogs:
				page, messages = v.Dialogs, v.Messages
				r.client.Peers().RememberUsers(v.Users)
				r.client.Peers().RememberChats(v.Chats)
				done = true
			case *tg.MessagesDialogsSlice:
				page, messages = v.Dialogs, v.Messages
				r.client.Peers().RememberUsers(v.Users)
				r.client.Peers().RememberChats(v.Chats)
			default:
				return nil, fmt.Errorf("DME dialogs returned no usable page")
			}
			if len(page) == 0 {
				break
			}
			var last *tg.Dialog
			for _, v := range page {
				d, ok := v.(*tg.Dialog)
				if !ok {
					continue
				}
				last = d
				key := bot.PeerID(d.Peer)
				if key == "" || seen[key] {
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
				dialogs = append(dialogs, keywordDialog{peer, key, maxID})
			}
			if done {
				break
			}
			if last == nil {
				return nil, fmt.Errorf("DME dialogs cursor missing")
			}
			nextPeer, err := r.client.InputPeer(last.Peer)
			if err != nil {
				return nil, err
			}
			if offsetID == last.TopMessage && bot.PeerID(last.Peer) == offsetKey {
				return nil, fmt.Errorf("DME dialogs cursor did not advance")
			}
			nextDate := 0
			for _, v := range messages {
				if m, ok := v.(*tg.Message); ok && m.ID == last.TopMessage && bot.PeerID(m.PeerID) == bot.PeerID(last.Peer) {
					nextDate = m.Date
					break
				}
			}
			if nextDate <= 0 {
				return nil, fmt.Errorf("DME dialogs cursor date missing")
			}
			offsetID, offsetDate, offsetPeer = last.TopMessage, nextDate, nextPeer
			offsetKey = bot.PeerID(last.Peer)
			if err := kit.Sleep(r.ctx, 100*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	return dialogs, nil
}
func (r *dmeRun) keywordGlobal(keyword string) error {
	dialogs, err := r.keywordDialogs()
	if err != nil {
		return err
	}
	for _, d := range dialogs {
		if err := r.keywordSearch(d.peer, d.key, d.maxID, keyword); err != nil {
			if r.ctx.Err() != nil {
				return r.ctx.Err()
			}
			r.scanFailed++
		}
	}
	return nil
}
