package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	api "github.com/OrionG-hub/laowangbot/pkg/pluginapi"
	"github.com/dlclark/regexp2"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var numeric = regexp.MustCompile(`^-?\d+$`)
var messageLink = regexp.MustCompile(`t\.me/(c/)?([a-zA-Z0-9_]+)/(\d+)`)
var deepLink = regexp.MustCompile(`(?i)(?:https?://)?t\.me/([a-zA-Z0-9_]+)\?(?:start|startapp)=([^&#\s]+)`)
var groupLink = regexp.MustCompile(`(?i)(?:https?://)?(?:t\.me/|telegram\.me/|@)(\+?[a-zA-Z0-9_]+)`)
var uuid = regexp.MustCompile(`[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}`)

func compileKeyword(k string) (*regexp2.Regexp, error) {
	k = strings.TrimSpace(k)
	if k == "" {
		return nil, fmt.Errorf("关键词不能为空")
	}
	if !strings.HasPrefix(strings.ToLower(k), "re:") {
		return nil, nil
	}
	exp := strings.TrimSpace(k[3:])
	opts := regexp2.RegexOptions(regexp2.ECMAScript | regexp2.IgnoreCase)
	if strings.HasPrefix(exp, "/") {
		i := strings.LastIndex(exp, "/")
		if i <= 0 {
			return nil, fmt.Errorf("正则表达式无效")
		}
		flags := exp[i+1:]
		exp = exp[1:i]
		opts = regexp2.ECMAScript
		seen := map[rune]bool{}
		for _, f := range flags {
			if seen[f] {
				return nil, fmt.Errorf("重复正则标记")
			}
			seen[f] = true
			switch f {
			case 'i':
				opts |= regexp2.IgnoreCase
			case 'm':
				opts |= regexp2.Multiline
			case 's':
				opts |= regexp2.Singleline
			case 'y':
				exp = `\A(?:` + exp + `)`
			case 'g', 'u', 'd':
			case 'v':
				return nil, fmt.Errorf("暂不支持 JS v Unicode 集合标记")
			default:
				return nil, fmt.Errorf("无效正则标记")
			}
		}
	}
	if exp == "" {
		return nil, fmt.Errorf("正则表达式不能为空")
	}
	r, e := regexp2.Compile(exp, opts)
	if e == nil {
		r.MatchTimeout = 100 * time.Millisecond
	}
	return r, e
}
func keywordMatches(text, k string) bool {
	r, e := compileKeyword(k)
	if e != nil {
		return false
	}
	if r == nil {
		return strings.Contains(strings.ToLower(text), strings.ToLower(k))
	}
	v, e := r.MatchString(text)
	return e == nil && v
}
func replace(s, p, r string) string { return regexp.MustCompile(p).ReplaceAllString(s, r) }
func normalizeField(s string) string {
	s = replace(s, `\d{4}[-/]\d{1,2}[-/]\d{1,2}[^\s|]*`, "")
	s = replace(s, `\d+\s*%`, "")
	s = replace(s, `\d[\d,._\s]*`, "")
	return strings.TrimSpace(replace(s, `\s+`, " "))
}
func normalizeLotteryKey(k string) string {
	if !strings.HasPrefix(k, "lottery:") {
		return k
	}
	p, d := strings.Index(k, ":prize="), strings.Index(k, ":draw=")
	if p < 0 || d < p {
		return k
	}
	return k[:p] + ":prize=" + normalizeField(k[p+7:d]) + ":draw=" + normalizeField(k[d+6:])
}
func tagged(text string, labels string) string {
	lines := strings.Split(text, "\n")
	start := regexp.MustCompile(`(?:` + labels + `)\s*[:：]?\s*(.*)`)
	next := regexp.MustCompile(`^\s*(?:[^\w\s]{0,4}\s*)?(?:抽奖|奖品|奖励|开奖|参与|条件|口令|关键词|留言|发起|活动|备注)[^:：\n]{0,12}[:：]`)
	for i, line := range lines {
		m := start.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var v []string
		if strings.TrimSpace(m[1]) != "" {
			v = append(v, strings.TrimSpace(m[1]))
		}
		for j := i + 1; j < len(lines) && len(v) < 4; j++ {
			l := strings.TrimSpace(lines[j])
			if l == "" {
				if len(v) > 0 {
					break
				}
				continue
			}
			if next.MatchString(l) {
				break
			}
			v = append(v, l)
		}
		return strings.Join(v, " | ")
	}
	return ""
}
func dedupKeys(msg api.Message, text, kw string) []string {
	deep, lottery := "", ""
	p := regexp.MustCompile(`(?i)(?:抽奖\s*ID|抽奖编号|活动\s*ID|开奖\s*ID)\s*[:：]\s*([0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}|[A-Za-z0-9_-]{10,})`)
	if m := p.FindStringSubmatch(text); m != nil {
		lottery = "lottery-id:" + strings.ToLower(m[1])
	}
	if lottery == "" {
		p = regexp.MustCompile(`(?i)(?:join|lottery|draw|take)\s*[_=-]\s*([0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12})`)
		if m := p.FindStringSubmatch(text); m != nil {
			lottery = "lottery-id:" + strings.ToLower(m[1])
		}
	}
	for _, b := range msg.Buttons {
		if m := deepLink.FindStringSubmatch(b.URL); m != nil {
			param, e := url.QueryUnescape(m[2])
			if e != nil {
				param = m[2]
			}
			if deep == "" {
				deep = "deep:" + m[1] + ":" + param
			}
			if lottery == "" {
				if id := uuid.FindString(param); id != "" {
					lottery = "lottery-id:" + strings.ToLower(id)
				}
			}
		}
	}
	var keys []string
	if lottery != "" {
		keys = append(keys, lottery)
		if deep != "" {
			keys = append(keys, deep)
		}
		return keys
	}
	if deep != "" {
		return []string{deep}
	}
	if msg.ForwardID != "" {
		mid, date := "no-msg-id", "no-date"
		if msg.ForwardMessageID != 0 {
			mid = strconv.Itoa(msg.ForwardMessageID)
		}
		if msg.ForwardDate != 0 {
			date = strconv.Itoa(msg.ForwardDate)
		}
		keys = append(keys, fmt.Sprintf("fwd:%s:%s:%s", msg.ForwardID, mid, date))
	}
	if kw != "" && !strings.HasPrefix(kw, "/start ") {
		prize, draw := tagged(text, "奖品内容|奖品|奖励|奖项"), tagged(text, "开奖日期|开奖时间|开奖")
		if prize != "" || draw != "" {
			k := []rune("lottery:" + kw + ":prize=" + normalizeField(prize) + ":draw=" + normalizeField(draw))
			if len(k) > 200 {
				k = k[:200]
			}
			keys = append(keys, string(k))
		}
	}
	n := replace(text, `(?:剩余|倒计时|开奖倒计时|还有)\s*[^\n]*`, "")
	n = replace(n, `\d+\s*(?:天|小时|时|分钟|分|秒)(?:\s*\d+\s*(?:天|小时|时|分钟|分|秒))*`, "")
	n = strings.TrimSpace(replace(n, `\s+`, " "))
	if len([]rune(n)) > 5 {
		keys = append(keys, fmt.Sprintf("txt:%x", sha256.Sum256([]byte(n))))
	}
	return keys
}
func (m *Monitor) extract(ctx context.Context, msg api.Message, text, matched string) (string, string) {
	for _, b := range msg.Buttons {
		if strings.Contains(b.Text, "抽奖") || strings.Contains(b.Text, "参与") || matched == b.Text {
			if d := deepLink.FindStringSubmatch(b.URL); d != nil {
				return d[1], "/start " + d[2]
			}
		}
	}
	clean := strings.NewReplacer("*", "", "_", "", "`", "").Replace(text)
	p := regexp.MustCompile("(?:关键词|口令|留言)\\s*[:：]\\s*(?:[「“\"']([^「」“”\"'\\n]+)[」”\"']?|([^\\n]+))")
	k := p.FindStringSubmatch(clean)
	if k == nil {
		return msg.ChatID, ""
	}
	kw := k[1]
	if kw == "" {
		kw = k[2]
	}
	kw = strings.Trim(kw, " \t\r\n>")
	r := []rune(kw)
	if len(r) > 60 {
		kw = string(r[:60])
	}
	urls := append([]string{}, msg.URLs...)
	urls = append(urls, groupLink.FindAllString(text, -1)...)
	for _, u := range urls {
		g := groupLink.FindStringSubmatch(u)
		if g == nil {
			continue
		}
		name := g[1]
		lower := strings.ToLower(name)
		if lower == "share" || lower == "c" || strings.HasSuffix(lower, "bot") {
			continue
		}
		r, e := m.call(ctx, api.Call{Method: "resolve", Target: name})
		if e != nil {
			return name, kw
		}
		if r.Entity != nil && r.Entity.Group && !r.Entity.Broadcast {
			return name, kw
		}
	}
	return msg.ChatID, kw
}
func (m *Monitor) origin(ctx context.Context, msg api.Message) map[string]string {
	r, _ := m.call(ctx, api.Call{Method: "resolve", Target: msg.ChatID})
	if !strings.HasPrefix(msg.ChatID, "-") {
		if r.Entity == nil || r.Entity.Username == "" {
			return nil
		}
		label := "🔗 打开原私聊"
		if r.Entity.Bot {
			label = "🔗 打开原 Bot"
		}
		return map[string]string{"text": label, "url": "https://t.me/" + r.Entity.Username}
	}
	u := ""
	if r.Entity != nil && r.Entity.Username != "" {
		u = "https://t.me/" + r.Entity.Username + "/" + strconv.Itoa(msg.ID)
	} else if strings.HasPrefix(msg.ChatID, "-100") {
		u = "https://t.me/c/" + strings.TrimPrefix(msg.ChatID, "-100") + "/" + strconv.Itoa(msg.ID)
	}
	if u == "" {
		return nil
	}
	return map[string]string{"text": "🔗 查看原消息", "url": u}
}
func (m *Monitor) event(ctx context.Context, e api.Event) error {
	if stale(e) {
		return nil
	}
	if strings.HasPrefix(e.Text, ".monitor sync ") {
		return m.syncCommand(ctx, e)
	}
	if strings.HasPrefix(e.Text, ".") {
		return nil
	}
	s := m.state.Settings
	if !s.IsGlobalEnabled {
		return nil
	}
	ids := []string{e.ChatID}
	if !strings.HasPrefix(e.ChatID, "-") {
		ids = append(ids, "-100"+e.ChatID)
	}
	allowed := s.MonitorAllGroups
	for _, id := range ids {
		if slices.Contains(s.ExcludedGroups, id) {
			return nil
		}
		allowed = allowed || slices.Contains(s.EnabledGroups, id)
	}
	if !allowed {
		return nil
	}
	msg := api.Message{ID: e.MessageID, ChatID: e.ChatID, SenderID: strconv.FormatInt(e.SenderID, 10), Text: e.Text, Date: e.Date, Out: e.Out}
	var err error
	if e.Message != nil {
		msg = *e.Message
	} else {
		var r api.Result
		r, err = m.call(ctx, api.Call{Method: "messages", Target: e.ChatID, IDs: []int{e.MessageID}})
		if err == nil && len(r.Messages) > 0 {
			msg = r.Messages[0]
		}
	}
	if s.BotID != "" && msg.SenderID == s.BotID {
		return nil
	}
	text := msg.Text
	for _, b := range msg.Buttons {
		text += " " + b.Text
	}
	specific := false
	keys := append([]string{}, s.Keywords...)
	for _, id := range ids {
		specific = specific || slices.Contains(s.GroupUsers[id], msg.SenderID)
		keys = append(keys, s.GroupKeywords[id]...)
	}
	matched := ""
	for _, k := range keys {
		if keywordMatches(text, k) {
			matched = k
			break
		}
	}
	if matched == "" {
		if !specific {
			return nil
		}
		matched = "👤 指定人发言"
	}
	if !specific && !(s.MonitorAdminsMessages && s.MonitorUsersMessages && !s.IgnoreBotMessages) {
		identity := "owner"
		if !msg.Out {
			r, er := m.call(ctx, api.Call{Method: "identity", Target: msg.ChatID, User: msg.SenderID})
			if er != nil {
				return er
			}
			identity = r.Identity
		}
		if identity != "owner" && !(identity == "bot" && !s.IgnoreBotMessages) && !(identity == "admin" && s.MonitorAdminsMessages) && !(identity == "user" && s.MonitorUsersMessages) {
			return nil
		}
	}
	if len(s.TargetGroups) == 0 {
		return nil
	}
	target, kw := m.extract(ctx, msg, text, matched)
	dedup := dedupKeys(msg, text, kw)
	for _, k := range dedup {
		if s.EnableDedup && m.state.Dedup[k] > time.Now().Add(-24*time.Hour).UnixMilli() {
			return nil
		}
	}
	for _, k := range dedup {
		m.state.Dedup[k] = time.Now().UnixMilli()
		if strings.HasPrefix(k, "lottery:") {
			m.dumpSample(msg, text)
		}
	}
	keyboard := [][]map[string]string{}
	tip := "🚨 <b>关键词提醒</b> [<code>" + html.EscapeString(shortText(matched, 256)) + "</code>]"
	if kw != "" {
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		m.state.Pending[id] = Action{TargetChatID: target, Keyword: kw, SourceChatID: msg.ChatID, Time: time.Now().UnixMilli()}
		keyboard = append(keyboard, []map[string]string{{"text": "🚀 一键参加 (自动触发集群全员跟随)", "callback_data": "send_" + id}})
		command := html.EscapeString(".monitor sync " + target + " " + msg.ChatID + " " + kw)
		if len(command) < 2000 {
			tip += "\n\n🛡️ <b>群友代发指令 (点击复制):</b>\n<code>" + command + "</code>"
		} else {
			tip += "\n代发指令过长，请使用参加按钮。"
		}
	}
	if origin := m.origin(ctx, msg); origin != nil {
		keyboard = append(keyboard, []map[string]string{origin})
	}
	if matched == "👤 指定人发言" {
		tip = "🚨 <b>指定监控人发言</b>"
	}
	if err = m.save(); err != nil {
		return err
	}
	var notificationErrors []error
	for _, t := range s.TargetGroups {
		if s.BotToken != "" {
			payload := map[string]any{"chat_id": t.ID, "text": tip, "parse_mode": "HTML", "disable_web_page_preview": true, "reply_markup": map[string]any{"inline_keyboard": keyboard}}
			if _, err := m.bot(ctx, "sendMessage", payload); err != nil {
				notificationErrors = append(notificationErrors, err)
				if strings.Contains(err.Error(), "network failure") {
					b, _ := json.Marshal(payload)
					m.state.Jobs = append(m.state.Jobs, Job{Kind: "bot_retry", Due: time.Now().Add(500 * time.Millisecond).UnixMilli(), Payload: b, Attempt: 1})
					if err = m.save(); err != nil {
						return err
					}
				}
			}
		} else {
			if _, err := m.call(ctx, api.Call{Method: "send", Target: t.ID, Text: tip, HTML: true}); err != nil {
				notificationErrors = append(notificationErrors, err)
			}
		}
		if _, err = m.call(ctx, api.Call{Method: "forward", Target: t.ID, User: msg.ChatID, IDs: []int{msg.ID}}); err != nil {
			fallback := text
			if fallback == "" {
				fallback = "[多媒体消息]"
			}
			for _, part := range escapedChunks(fallback, 3000) {
				if _, err = m.call(ctx, api.Call{Method: "send", Target: t.ID, Text: "⚠️ <b>无法转发原消息</b>\n\n" + part, HTML: true}); err != nil {
					notificationErrors = append(notificationErrors, err)
					break
				}
			}
		}
	}
	return errors.Join(notificationErrors...)
}
func (m *Monitor) dumpSample(msg api.Message, text string) {
	path := filepath.Join(filepath.Dir(m.path), "lottery-samples.jsonl")
	if st, e := os.Stat(path); e == nil && st.Size() > 512*1024 {
		_ = os.Remove(path)
	}
	r := []rune(text)
	if len(r) > 900 {
		text = string(r[:900])
	}
	b, _ := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "why": "no-lottery-id", "chatId": msg.ChatID, "msgId": msg.ID, "text": text})
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e == nil {
		defer f.Close()
		_, _ = f.Write(append(b, '\n'))
	}
}
