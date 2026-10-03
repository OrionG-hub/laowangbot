// Package extensions connects statically linked Go plugins to Telegram.
package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OrionG-hub/laowangbot/internal/app"
	"github.com/OrionG-hub/laowangbot/internal/bot"
	"github.com/OrionG-hub/laowangbot/internal/command"
	"github.com/OrionG-hub/laowangbot/internal/compiled"
	"github.com/OrionG-hub/laowangbot/internal/hostbridge"
	"github.com/OrionG-hub/laowangbot/internal/plugin"
	"github.com/OrionG-hub/laowangbot/pkg/pluginapi"
	"github.com/gotd/td/tg"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
)

var commandName = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// Register validates the complete namespace before registering any plugin.
// Installing or replacing a plugin takes effect after restart.
func Register(a *app.App) error {
	external := compiled.Entries()
	if err := registerEntries(a, bundledEntries(external)); err != nil {
		return err
	}
	registerBundledHelp(a, external)
	return nil
}

// ValidateCompiled checks linked manifests and the command namespace without
// constructing plugins, touching state, or connecting to Telegram.
func ValidateCompiled(a *app.App) error {
	return validateEntries(a, bundledEntries(compiled.Entries()))
}

var pluginName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func validateEntries(a *app.App, entries []compiled.Entry) error {
	names := map[string]bool{"tpm": true}
	plugins := map[string]bool{}
	for _, c := range a.Registry.Commands() {
		names[c.Name] = true
	}
	for _, entry := range entries {
		m := entry.Manifest
		if !pluginName.MatchString(m.Name) || plugins[m.Name] || m.Version == "" || m.ProtocolVersion != 2 || m.Package != "." || m.Executable != "" || m.Persistent || len(m.Args) > 0 || entry.New == nil {
			return fmt.Errorf("invalid compiled plugin %q", m.Name)
		}
		plugins[m.Name] = true
		if m.TimeoutSeconds < 0 || m.TimeoutSeconds > 300 || m.IntervalSeconds < 0 || m.IntervalSeconds > 31536000 {
			return fmt.Errorf("plugin %s: invalid timeout/interval", m.Name)
		}
		if _, err := hostbridge.Direct(nil, m.Capabilities); err != nil {
			return err
		}
		for _, n := range m.Commands {
			if !commandName.MatchString(n) || names[n] {
				return fmt.Errorf("plugin %s has invalid or conflicting command %q", m.Name, n)
			}
			names[n] = true
		}
		for _, e := range m.Events {
			if e != "message" && e != "outgoing" && e != "edited" {
				return fmt.Errorf("plugin %s: unsupported event %s", m.Name, e)
			}
		}
	}
	return nil
}
func registerEntries(a *app.App, entries []compiled.Entry) error {
	if err := validateEntries(a, entries); err != nil {
		return err
	}
	registerManagement(a, plugin.Manager{Root: a.Root})
	for _, entry := range entries {
		m := entry.Manifest
		host, _ := hostbridge.Direct(a.Bot, m.Capabilities)
		life, cancel := context.WithCancel(context.Background())
		r := &runtime{factory: entry.New, host: host, root: a.Root, manifest: m, logger: a.Logger, queue: make(chan event, 64), gate: make(chan struct{}, 1), life: life, cancel: cancel}
		a.OnClose(r.close)
		for _, n := range m.Commands {
			a.Registry.Register(&command.Command{Name: n, Description: "插件 " + m.Name, Handle: func(ctx context.Context, inv *command.Invocation) error {
				event := messageEvent(inv.Message, inv.Client)
				if inv.Client != nil {
					event["self_id"] = strconv.FormatInt(inv.Client.SelfID(), 10)
				}
				raw, _ := json.Marshal(event)
				response, e := r.call(ctx, pluginapi.Request{Version: 1, Type: "command", Command: inv.Command, Args: inv.Args, Text: inv.Text, Event: raw})
				if e != nil {
					return e
				}
				if e = sendFor(ctx, inv.Client, response, m); e != nil {
					return e
				}
				formatted, isHelp := pluginHelpHTML(response.Text, inv.Prefix)
				if response.HTML {
					formatted, isHelp = response.Text, true
				}
				if isHelp {
					for i, page := range pluginapi.PanelPages(formatted) {
						var err error
						if i == 0 {
							err = inv.Edit(ctx, page)
						} else {
							err = inv.Reply(ctx, page)
						}
						if err != nil {
							return err
						}
					}
					return nil
				}
				if response.Text != "" {
					return writePluginText(ctx, response.Text,
						func(text string) error { return inv.EditText(ctx, text) },
						func(text string) error { return inv.Reply(ctx, command.Escape(text)) })
				}
				return nil
			}})
		}
		if len(m.Events) > 0 {
			a.OnIncoming(func(ctx context.Context, c *bot.Client, msg *bot.Message) bool {
				if msg.Edited && !slices.Contains(m.Events, "edited") {
					return false
				}
				if msg.Out && !slices.Contains(m.Events, "outgoing") {
					return false
				}
				if !msg.Out && !slices.Contains(m.Events, "message") {
					return false
				}
				payload := messageEvent(msg, c)
				payload["self_id"] = strconv.FormatInt(c.SelfID(), 10)
				r.observeIngress(messageDate(msg))
				raw, _ := json.Marshal(payload)
				if err := r.enqueue(ctx, event{request: pluginapi.Request{Version: 1, Type: "event", Event: raw}, client: c, message: msg}); err != nil && a.Logger != nil {
					a.Logger.Warn("plugin.event_enqueue_failed", "plugin", m.Name, "error", err)
				}
				return false
			})
		}
		a.Registry.AddJob(r.run)
	}
	return nil
}

type event struct {
	request pluginapi.Request
	client  *bot.Client
	message *bot.Message
}
type runtime struct {
	filter   atomic.Value // immutable func(pluginapi.Event) bool
	factory  pluginapi.Factory
	host     pluginapi.Host
	root     string
	manifest plugin.Manifest
	logger   *slog.Logger
	queue    chan event
	gate     chan struct{}
	instance pluginapi.Plugin
	life     context.Context
	cancel   context.CancelFunc
	stats    counters
}

func stateDirectory(root, name string) (string, error) {
	path, err := filepath.Abs(filepath.Join(root, "state", name))
	if err != nil {
		return "", err
	}
	// Reject state redirects without rejecting normal OS aliases above the deployment.
	for _, p := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path), path} {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
		if e == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return "", fmt.Errorf("state directory symlink or non-directory rejected: %s", p)
		}
	}
	if err = os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	return path, nil
}
func (r *runtime) call(ctx context.Context, q pluginapi.Request) (response pluginapi.Response, err error) {
	seconds := r.manifest.TimeoutSeconds
	if seconds == 0 {
		seconds = 15
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	stop := context.AfterFunc(r.life, cancel)
	defer stop()
	select {
	case r.gate <- struct{}{}:
	case <-ctx.Done():
		return response, ctx.Err()
	case <-r.life.Done():
		return response, r.life.Err()
	}
	defer func() { <-r.gate }()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("plugin %s panicked: %v", r.manifest.Name, p)
		}
	}()
	if err = r.life.Err(); err != nil {
		return response, err
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}
	if r.instance == nil {
		var state string
		state, err = stateDirectory(r.root, r.manifest.Name)
		if err != nil {
			return response, err
		}
		r.instance, err = r.factory(r.life, r.host, state)
		if err != nil {
			return response, err
		}
		if r.instance == nil {
			return response, errors.New("plugin factory returned nil")
		}
	}
	// Invoke synchronously: cooperative timeout never detaches work or permits overlap.
	started := time.Now()
	response = r.instance.Handle(ctx, q)
	if q.Type != "event" || r.filter.Load() == nil {
		if f, ok := r.instance.(pluginapi.EventFilter); ok {
			r.filter.Store(f.EventFilter())
		}
	}
	elapsed := time.Since(started)
	r.observeCall(q.Type, elapsed)
	if elapsed >= time.Second && r.logger != nil {
		r.logger.Warn("plugin.request_slow", "plugin", r.manifest.Name, "type", q.Type, "took", elapsed, "queued", len(r.queue))
	}
	if err = ctx.Err(); err != nil {
		return response, err
	}

	if response.Version != 1 {
		return response, errors.New("invalid response version")
	}
	if len(response.Messages) > 20 {
		return response, errors.New("too many response messages")
	}
	data, e := json.Marshal(response)
	if e != nil {
		return response, e
	}
	if len(data) > plugin.MaxOutput {
		return response, errors.New("plugin response too large")
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}

// Backpressure preserves admitted events rather than discarding them at capacity.
func (r *runtime) enqueue(ctx context.Context, ev event) error {
	if r.manifest.Name != "monitor" {
		select {
		case r.queue <- ev:
			return nil
		default:
			if r.logger != nil {
				r.logger.Warn("plugin.event_dropped", "plugin", r.manifest.Name)
			}
			return nil
		}
	}

	if f := r.filter.Load(); f != nil {
		var input pluginapi.Event
		if json.Unmarshal(ev.request.Event, &input) == nil && !f.(func(pluginapi.Event) bool)(input) {
			r.stats.filtered.Add(1)
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.life.Done():
		return r.life.Err()
	case r.queue <- ev:
		return nil
	}
}

func (r *runtime) close() {
	r.cancel()
	r.gate <- struct{}{}
	defer func() { <-r.gate }()
	defer func() {
		if p := recover(); p != nil && r.logger != nil {
			r.logger.Warn("plugin.close_panicked", "plugin", r.manifest.Name, "panic", p)
		}
	}()
	if r.instance != nil {
		instance := r.instance
		r.instance = nil
		instance.Close()
	}
}
func (r *runtime) run(ctx context.Context, client *bot.Client) {
	defer r.close()
	var ticks <-chan time.Time
	if r.manifest.IntervalSeconds > 0 {
		t := time.NewTicker(time.Duration(r.manifest.IntervalSeconds) * time.Second)
		defer t.Stop()
		ticks = t.C
	}
	r.reportStats(ctx)
	burst := 0
	for {
		var ev event
		select {
		case <-ctx.Done():
			return
		case <-r.life.Done():
			return
		case ev = <-r.queue:
		case <-ticks:
			ev = event{request: pluginapi.Request{Version: 1, Type: "tick"}, client: client}
			// Process a bounded burst before scheduled work; never starve ticks.
			if r.manifest.Name == "monitor" && burst < 32 {
				select {
				case ev = <-r.queue:
				default:
				}
			}
		}
		if ev.request.Type == "event" {
			burst++
		} else {
			burst = 0
		}
		response, e := r.call(ctx, ev.request)
		if e == nil {
			e = sendFor(ctx, ev.client, response, r.manifest)
		}
		if e == nil && response.Text != "" && ev.message != nil {
			peer, err := ev.client.InputPeerFromChatID(ev.message.ChatID)
			if err == nil {
				_, e = ev.client.SendText(ctx, peer, response.Text, bot.SendOptions{ReplyTo: ev.message.ID})
			} else {
				e = err
			}
		}
		if e != nil && r.logger != nil {
			r.logger.Warn("plugin.request_failed", "plugin", r.manifest.Name, "error", e)
		}
	}
}
func messageEvent(m *bot.Message, clients ...*bot.Client) map[string]any {
	channelDM := false
	if m.Out && m.Raw != nil && m.Raw.SavedPeerID != nil && len(clients) > 0 && clients[0] != nil {
		if id, ok := m.Channel(); ok {
			ch, found := clients[0].Peers().Channel(id)
			channelDM = found && ch.Monoforum
		}
	}
	_, user := m.Sender.(*tg.PeerUser)
	payload := map[string]any{"received_at": time.Now().Unix(), "chat_type": string(m.ChatType), "channel_dm": channelDM, "type": "message", "chat_id": m.ChatID, "message_id": m.ID, "sender_id": m.SenderID(), "sender_is_user": user, "sender_peer_id": bot.PeerID(m.Sender), "text": m.Text, "edited": m.Edited, "reply_to_id": m.ReplyToID, "out": m.Out, "date": messageDate(m)}
	if m.Raw != nil {
		payload["message"] = hostbridge.MessageSnapshot(m)
	}
	return payload
}
func send(ctx context.Context, c *bot.Client, r pluginapi.Response) error {
	for _, m := range r.Messages {
		peer, e := c.InputPeerFromChatID(m.ChatID)
		if e != nil {
			return e
		}
		if _, e = c.SendText(ctx, peer, m.Text, bot.SendOptions{}); e != nil {
			return e
		}
	}
	return nil
}

func messageDate(m *bot.Message) int {
	if m.Raw != nil {
		return m.Raw.Date
	}
	return 0
}

func sendFor(ctx context.Context, c *bot.Client, r pluginapi.Response, m plugin.Manifest) error {
	if len(m.Capabilities) > 0 && len(r.Messages) > 0 && !slices.Contains(m.Capabilities, "send") {
		return fmt.Errorf("plugin %s lacks send capability", m.Name)
	}
	return send(ctx, c, r)
}
