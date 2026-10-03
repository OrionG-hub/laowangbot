// Package monitor ports the AyuGram monitor V3.21 plugin to the isolated Go API.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	api "github.com/OrionG-hub/laowangbot/pkg/pluginapi"
	"html"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Target struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}
type Settings struct {
	IsGlobalEnabled       bool                `json:"isGlobalEnabled"`
	MonitorAllGroups      bool                `json:"monitorAllGroups"`
	EnableDedup           bool                `json:"enableDedup"`
	EnabledGroups         []string            `json:"enabledGroups"`
	ExcludedGroups        []string            `json:"excludedGroups"`
	Keywords              []string            `json:"keywords"`
	GroupKeywords         map[string][]string `json:"groupKeywords"`
	GroupUsers            map[string][]string `json:"groupUsers"`
	TargetGroups          []Target            `json:"targetGroups"`
	IgnoreBotMessages     bool                `json:"ignoreBotMessages"`
	MonitorAdminsMessages bool                `json:"monitorAdminsMessages"`
	MonitorUsersMessages  bool                `json:"monitorUsersMessages"`
	BotToken              string              `json:"botToken"`
	BotID                 string              `json:"botId"`
	TrustedLeaderID       string              `json:"trustedLeaderId"`
}
type Action struct {
	TargetChatID string `json:"targetChatId"`
	Keyword      string `json:"keyword"`
	Time         int64  `json:"time"`
	SourceChatID string `json:"sourceChatId"`
}
type Job struct {
	Due      int64           `json:"due"`
	Kind     string          `json:"kind"`
	Target   string          `json:"target"`
	Fallback string          `json:"fallback,omitempty"`
	Text     string          `json:"text,omitempty"`
	Report   string          `json:"report,omitempty"`
	IDs      []int           `json:"ids,omitempty"`
	Owner    bool            `json:"owner,omitempty"`
	Attempt  int             `json:"attempt,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}
type State struct {
	Settings     Settings          `json:"monitor_settings"`
	Pending      map[string]Action `json:"-"`
	PendingPairs []json.RawMessage `json:"monitor_pending_actions"`
	Dedup        map[string]int64  `json:"monitor_dedup"`
	Jobs         []Job             `json:"monitor_jobs,omitempty"`
	Suppress     map[string]int64  `json:"monitor_sync_suppressions,omitempty"`
	UpdateID     int64             `json:"monitor_update_id,omitempty"`
}
type Monitor struct {
	mu          sync.Mutex
	host        api.Host
	path        string
	state       State
	owner       string
	botBase     string
	nextPoll    int64
	conflicts   int
	lastCleanup int64
}

func New(h api.Host, path string) (*Monitor, error) {
	m := &Monitor{host: h, path: path, botBase: "https://api.telegram.org"}
	m.state.Settings = Settings{IsGlobalEnabled: true, EnableDedup: true, MonitorAdminsMessages: true}
	b, e := os.ReadFile(path)
	if e == nil {
		if e = json.Unmarshal(b, &m.state); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	s := &m.state
	if s.Settings.GroupKeywords == nil {
		s.Settings.GroupKeywords = map[string][]string{}
	}
	if s.Settings.GroupUsers == nil {
		s.Settings.GroupUsers = map[string][]string{}
	}
	s.Pending = map[string]Action{}
	for _, raw := range s.PendingPairs {
		var pair []json.RawMessage
		if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 {
			continue
		}
		var key string
		var a Action
		if json.Unmarshal(pair[0], &key) == nil && json.Unmarshal(pair[1], &a) == nil {
			s.Pending[key] = a
		}
	}
	if s.Dedup == nil {
		s.Dedup = map[string]int64{}
	}
	for k, v := range s.Dedup {
		nk := normalizeLotteryKey(k)
		if nk != k {
			delete(s.Dedup, k)
			s.Dedup[nk] = max(v, s.Dedup[nk])
		}
	}
	if s.Suppress == nil {
		s.Suppress = map[string]int64{}
	}
	m.cleanup(time.Now().UnixMilli())
	return m, nil
}
func (m *Monitor) save() error {
	m.state.PendingPairs = nil
	for k, a := range m.state.Pending {
		b, e := json.Marshal([]any{k, a})
		if e != nil {
			return e
		}
		m.state.PendingPairs = append(m.state.PendingPairs, b)
	}
	b, e := json.MarshalIndent(m.state, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(m.path), 0700); e != nil {
		return e
	}
	tmp := m.path + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, m.path)
}
func (m *Monitor) Handle(ctx context.Context, r api.Request) api.Response {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.Response{Version: api.Version}
	var e api.Event
	if len(r.Event) > 0 {
		if err := json.Unmarshal(r.Event, &e); err != nil {
			out.Error = err.Error()
			return out
		}
	}
	if e.SelfID != "" {
		m.owner = e.SelfID
	}
	var err error
	switch r.Type {
	case "command":
		args := r.Args
		if len(args) > 0 && args[0] == "monitor" {
			args = args[1:]
		}
		if len(args) == 0 && r.Text != "" {
			args = strings.Fields(r.Text)
			if len(args) > 0 && (args[0] == "monitor" || strings.HasSuffix(args[0], "monitor")) {
				args = args[1:]
			}
		}
		if m.owner == "" || !e.SenderIsUser || (e.SenderPeerID != "" && e.SenderPeerID != m.owner) || strconv.FormatInt(e.SenderID, 10) != m.owner {
			err = errors.New("仅机主可修改监控设置")
			break
		}
		var text string
		text, err = m.command(ctx, e, args)
		if err == nil && text != "" {
			panel := len(args) == 0 || arg(args, 0) == "help" || arg(args, 0) == "list" || arg(args, 0) == "list_groups"
			if panel {
				prefix := "."
				if fields := strings.Fields(r.Text); len(fields) > 0 && strings.HasSuffix(fields[0], "monitor") {
					prefix = strings.TrimSuffix(fields[0], "monitor")
				}
				text = strings.ReplaceAll(text, "<code>.monitor ", "<code>"+html.EscapeString(prefix)+"monitor ")
				for i, page := range api.PanelPages(text) {
					if i > 0 {
						e.Out = false
						e.MessageID = 0
					}
					if err = m.reply(ctx, e, page, 100); err != nil {
						break
					}
				}
			} else {
				err = m.reply(ctx, e, text, 100)
			}
		}
	case "event":
		err = m.event(ctx, e)
	case "tick":
		err = m.tick(ctx)
	case "shutdown":
		err = m.save()
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}
func (m *Monitor) call(ctx context.Context, c api.Call) (api.Result, error) {
	r, e := m.host.Call(ctx, c)
	if e == nil && r.Error != "" {
		e = errors.New(r.Error)
	}
	return r, e
}
func (m *Monitor) reply(ctx context.Context, e api.Event, text string, seconds int) error {
	id := e.MessageID
	var err error
	if e.Out {
		_, err = m.call(ctx, api.Call{Method: "edit", Target: e.ChatID, MessageID: id, Text: text, HTML: true})
	} else {
		var r api.Result
		r, err = m.call(ctx, api.Call{Method: "send", Target: e.ChatID, ReplyTo: id, Text: text, HTML: true})
		id = r.MessageID
		if err == nil && e.MessageID > 0 {
			_, _ = m.call(ctx, api.Call{Method: "delete", Target: e.ChatID, IDs: []int{e.MessageID}})
		}
	}
	if err != nil {
		return err
	}
	if seconds > 0 && id > 0 {
		m.state.Jobs = append(m.state.Jobs, Job{Kind: "delete", Target: e.ChatID, IDs: []int{id}, Due: time.Now().Add(time.Duration(seconds) * time.Second).UnixMilli()})
	}
	return m.save()
}
func (m *Monitor) resolve(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	if numeric.MatchString(input) {
		return input, nil
	}
	if input == "" {
		return "", errors.New("群组不能为空")
	}
	r, e := m.call(ctx, api.Call{Method: "resolve", Target: input})
	if e != nil {
		return "", e
	}
	if r.Entity == nil {
		return "", errors.New("无法解析群组")
	}
	return r.Entity.ID, nil
}
func add(list []string, s string) []string {
	if !slices.Contains(list, s) {
		return append(list, s)
	}
	return list
}
func remove(list []string, s string) []string {
	i, e := strconv.Atoi(s)
	if e == nil && i > 0 && i <= len(list) {
		return slices.Delete(list, i-1, i)
	}
	if i = slices.Index(list, s); i >= 0 {
		return slices.Delete(list, i, i+1)
	}
	return list
}
func arg(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}
func rest(a []string, i int) string {
	if i < len(a) {
		return strings.TrimSpace(strings.Join(a[i:], " "))
	}
	return ""
}
func off(s string) bool { return s == "del" || s == "delete" || s == "off" }
func (m *Monitor) command(ctx context.Context, e api.Event, a []string) (text string, err error) {
	before, _ := json.Marshal(m.state)
	pending := m.state.Pending
	updateID, nextPoll, conflicts := m.state.UpdateID, m.nextPoll, m.conflicts
	defer func() {
		if err != nil {
			var restored State
			_ = json.Unmarshal(before, &restored)
			restored.Pending = pending
			if restored.Suppress == nil {
				restored.Suppress = map[string]int64{}
			}
			m.state = restored
			m.state.UpdateID, m.nextPoll, m.conflicts = updateID, nextPoll, conflicts
		}
	}()
	if err := validateCommand(a); err != nil {
		return "", err
	}
	s := &m.state.Settings
	switch arg(a, 0) {
	case "", "help":
		return m.help(e.ChatID), nil
	case "list", "list_groups":
		return m.details(ctx), nil
	case "clean":
		n := len(m.state.Dedup)
		m.state.Dedup = map[string]int64{}
		return fmt.Sprintf("🧹 清理了 %d 条去重缓存。", n), m.save()
	case "on":
		s.EnabledGroups = add(s.EnabledGroups, e.ChatID)
	case "off":
		if i := slices.Index(s.EnabledGroups, e.ChatID); i >= 0 {
			s.EnabledGroups = slices.Delete(s.EnabledGroups, i, i+1)
		}
	case "global":
		s.IsGlobalEnabled = arg(a, 1) == "on"
	case "sync":
		e.Text = ".monitor " + strings.Join(a, " ")
		return "", m.syncCommand(ctx, e)
	case "send":
		if arg(a, 1) == "" || rest(a, 2) == "" {
			return "", errors.New("用法: .monitor send <群ID> <文本>")
		}
		_, err := m.call(ctx, api.Call{Method: "send", Target: a[1], Text: rest(a, 2)})
		if err != nil {
			return "", err
		}
		_, _ = m.call(ctx, api.Call{Method: "delete", Target: e.ChatID, IDs: []int{e.MessageID}})
		r, err := m.call(ctx, api.Call{Method: "send", Target: e.ChatID, Text: "✅ 安全代发成功"})
		if err == nil {
			m.state.Jobs = append(m.state.Jobs, Job{Kind: "delete", Target: e.ChatID, IDs: []int{r.MessageID}, Due: time.Now().Add(3 * time.Second).UnixMilli()})
		}
		return "", errors.Join(err, m.save())
	case "set":
		key, v := arg(a, 1), arg(a, 2)
		switch key {
		case "leader":
			if off(v) {
				v = ""
			}
			if v != "" && !numeric.MatchString(v) {
				return "", errors.New("Leader 必须是数字 ID")
			}
			s.TrustedLeaderID = v
		case "bot_token":
			v = rest(a, 2)
			if off(v) {
				v = ""
			}
			if s.BotToken != v {
				m.state.UpdateID = 0
			}
			s.BotToken = v
			m.nextPoll = 0
			m.conflicts = 0
		case "bot_id":
			if off(v) {
				v = ""
			}
			s.BotID = v
		case "monitor_all_groups":
			s.MonitorAllGroups = v == "on"
		case "dedup":
			s.EnableDedup = v == "on"
		case "ignore_bot_messages":
			s.IgnoreBotMessages = v != "on"
		case "monitor_admins_messages":
			s.MonitorAdminsMessages = v == "on"
		case "monitor_users_messages":
			s.MonitorUsersMessages = v == "on"
		case "keyword":
			if v == "add" {
				k := rest(a, 3)
				if _, err := compileKeyword(k); err != nil {
					return "", err
				}
				s.Keywords = add(s.Keywords, k)
			} else if v == "del" {
				if !canRemove(s.Keywords, rest(a, 3)) {
					return "", errors.New("关键词不存在")
				}
				s.Keywords = remove(s.Keywords, rest(a, 3))
			}
		case "monitor_group", "exclude_group":
			list := &s.EnabledGroups
			if key == "exclude_group" {
				list = &s.ExcludedGroups
			}
			if v == "add" {
				id, err := m.resolve(ctx, rest(a, 3))
				if err != nil {
					return "", err
				}
				*list = add(*list, id)
			} else if v == "del" {
				if !canRemove(*list, arg(a, 3)) {
					return "", errors.New("群组不存在")
				}
				*list = remove(*list, arg(a, 3))
			}
		case "target":
			if v == "add" {
				id := arg(a, 3)
				if id == "" {
					return "", errors.New("目标不能为空")
				}
				found := false
				for _, t := range s.TargetGroups {
					found = found || t.ID == id
				}
				if !found {
					name := rest(a, 4)
					if name == "" {
						name = id
					}
					s.TargetGroups = append(s.TargetGroups, Target{ID: id, URL: id, Name: name})
				}
			} else if v == "del" {
				i, _ := strconv.Atoi(arg(a, 3))
				if i <= 0 || i > len(s.TargetGroups) {
					return "", errors.New("目标序号不存在")
				}
				if i > 0 && i <= len(s.TargetGroups) {
					s.TargetGroups = slices.Delete(s.TargetGroups, i-1, i)
				}
			}
		case "group_keyword", "group_user":
			id, err := m.resolve(ctx, arg(a, 3))
			value := rest(a, 4)
			if key == "group_user" && v == "add" {
				if match := messageLink.FindStringSubmatch(arg(a, 3)); match != nil {
					target := match[2]
					if match[1] != "" {
						target = "-100" + target
					}
					id, err = m.resolve(ctx, target)
					if err == nil {
						mid, _ := strconv.Atoi(match[3])
						r, er := m.call(ctx, api.Call{Method: "messages", Target: id, IDs: []int{mid}})
						err = er
						if len(r.Messages) > 0 {
							value = r.Messages[0].SenderID
						} else {
							err = errors.New("链接读不到原消息")
						}
					}
				}
			}
			if err != nil {
				return "", err
			}
			table := s.GroupKeywords
			if key == "group_user" {
				table = s.GroupUsers
			}
			switch v {
			case "add":
				if key == "group_keyword" {
					if _, err = compileKeyword(value); err != nil {
						return "", err
					}
				} else if !numeric.MatchString(id) || !numeric.MatchString(value) {
					return "", errors.New("群和用户必须为数字 ID")
				}
				table[id] = add(table[id], value)
			case "del":
				if !canRemove(table[id], value) {
					return "", errors.New("指定条目不存在")
				}
				table[id] = remove(table[id], value)
				if len(table[id]) == 0 {
					delete(table, id)
				}
			case "clear":
				delete(table, id)
			}
		default:
			return "", errors.New("未知设置")
		}
	default:
		return fmt.Sprintf("总开关: %t\n监控全部: %t\n本群监听: %t\n本群屏蔽: %t", s.IsGlobalEnabled, s.MonitorAllGroups, slices.Contains(s.EnabledGroups, e.ChatID), slices.Contains(s.ExcludedGroups, e.ChatID)), nil
	}
	return "✅ 监控配置已更新", m.save()
}

func taskKey(t, f, text string) string {
	return strings.TrimSpace(t) + "|" + strings.TrimSpace(f) + "|" + strings.TrimSpace(text)
}
func (m *Monitor) ensureOwner(ctx context.Context) error {
	if m.owner != "" {
		return nil
	}
	r, e := m.call(ctx, api.Call{Method: "self"})
	if e != nil {
		return e
	}
	if r.Entity == nil || r.Entity.ID == "" {
		return errors.New("无法确认机主身份")
	}
	m.owner = r.Entity.ID
	return nil
}
func (m *Monitor) syncCommand(ctx context.Context, e api.Event) error {
	if err := m.ensureOwner(ctx); err != nil {
		return err
	}
	sender := e.SenderPeerID
	if sender == "" {
		sender = strconv.FormatInt(e.SenderID, 10)
	}
	if !e.SenderIsUser {
		return nil
	}
	owner := sender == m.owner
	if !owner && (m.state.Settings.TrustedLeaderID == "" || sender != m.state.Settings.TrustedLeaderID) {
		return nil
	}
	a := strings.Fields(e.Text)
	if len(a) < 5 {
		return nil
	}
	t, f, text := a[2], a[3], strings.Join(a[4:], " ")
	if t == "" || f == "" || text == "" {
		return nil
	}
	if owner && m.state.Suppress[taskKey(t, f, text)] > time.Now().Add(-2*time.Minute).UnixMilli() {
		return nil
	}
	if e.MessageID > 0 && e.ChatID != "" {
		key := "message:" + e.ChatID + ":" + strconv.Itoa(e.MessageID)
		if m.state.Suppress[key] > time.Now().Add(-2*time.Minute).UnixMilli() {
			return nil
		}
		m.state.Suppress[key] = time.Now().UnixMilli()
	}
	m.queueSync(t, f, text, e.ChatID, owner, e.MessageID)
	return m.save()
}
func (m *Monitor) queueSync(t, f, text, report string, owner bool, id int) {
	m.state.Jobs = append(m.state.Jobs, Job{Kind: "sync", Target: t, Fallback: f, Text: text, Report: report, Owner: owner, IDs: []int{id}, Due: time.Now().Add(time.Duration(1500+rand.IntN(4500)) * time.Millisecond).UnixMilli()})
}
func (m *Monitor) cleanup(now int64) {
	for k, v := range m.state.Dedup {
		if now-v > 86400000 {
			delete(m.state.Dedup, k)
		}
	}
	for k, v := range m.state.Pending {
		if now-v.Time > 86400000 {
			delete(m.state.Pending, k)
		}
	}
	for k, v := range m.state.Suppress {
		if now-v > 120000 {
			delete(m.state.Suppress, k)
		}
	}
	m.lastCleanup = now
}
func (m *Monitor) tick(ctx context.Context) error {
	now := time.Now().UnixMilli()
	var errs []error
	if now-m.lastCleanup >= 600000 {
		m.cleanup(now)
	}
	jobs := m.state.Jobs
	m.state.Jobs = nil
	for _, j := range jobs {
		if j.Due > now {
			m.state.Jobs = append(m.state.Jobs, j)
			continue
		}
		if j.Kind == "bot_retry" {
			_, err := m.bot(ctx, "sendMessage", j.Payload)
			if err != nil && strings.Contains(err.Error(), "network failure") && j.Attempt < 3 {
				j.Attempt++
				j.Due = now + 500
				m.state.Jobs = append(m.state.Jobs, j)
			}
			if err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if j.Kind == "delete" {
			_, err := m.call(ctx, api.Call{Method: "delete", Target: j.Target, IDs: j.IDs})
			if err != nil && j.Attempt < 3 {
				j.Attempt++
				j.Due = now + 5000
				m.state.Jobs = append(m.state.Jobs, j)
			}
			if err != nil {
				errs = append(errs, err)
			}
			continue
		}
		target := j.Target
		if !strings.HasPrefix(target, "@") && !numeric.MatchString(target) {
			target = "@" + target
		}
		_, err := m.call(ctx, api.Call{Method: "send", Target: target, Text: j.Text})
		if err != nil && (strings.Contains(err.Error(), "CHAT_WRITE_FORBIDDEN") || strings.Contains(err.Error(), "CHAT_ADMIN_REQUIRED") || strings.Contains(err.Error(), "CHANNEL_PRIVATE")) && target != j.Fallback {
			_, err = m.call(ctx, api.Call{Method: "send", Target: j.Fallback, Text: j.Text})
		}
		if j.Owner && j.Report != "" {
			text := "✅ 集群同步执行成功"
			delay := int64(3000)
			if err != nil {
				text = "❌ 同步失败: " + html.EscapeString(shortText(err.Error(), 512))
				delay = 5000
			}
			r, er := m.call(ctx, api.Call{Method: "send", Target: j.Report, Text: text, HTML: true})
			if er != nil {
				errs = append(errs, er)
			}
			if er == nil {
				ids := []int{r.MessageID}
				if err == nil {
					for _, id := range j.IDs {
						if id > 0 {
							ids = append(ids, id)
						}
					}
				}
				m.state.Jobs = append(m.state.Jobs, Job{Kind: "delete", Target: j.Report, IDs: ids, Due: now + delay})
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if m.state.Settings.BotToken != "" && now >= m.nextPoll && m.awaitingCallback(now) {
		if err := m.poll(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, m.save())...)
}
