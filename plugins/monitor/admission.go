package monitor

import (
	"encoding/json"
	api "github.com/OrionG-hub/laowangbot/pkg/pluginapi"
	"slices"
	"strconv"
	"strings"
)

// EventFilter snapshots settings under the plugin lock. The returned predicate
// never acquires that lock or performs network/regex work on the update thread.
func (m *Monitor) EventFilter() func(api.Event) bool {
	m.mu.Lock()
	raw, _ := json.Marshal(m.state.Settings)
	m.mu.Unlock()
	var s Settings
	_ = json.Unmarshal(raw, &s)
	return func(e api.Event) bool {
		if stale(e) {
			return false
		}
		if strings.HasPrefix(e.Text, ".monitor sync ") {
			return true
		}
		if strings.HasPrefix(e.Text, ".") || !s.IsGlobalEnabled {
			return false
		}
		ids := []string{e.ChatID}
		if !strings.HasPrefix(e.ChatID, "-") {
			ids = append(ids, "-100"+e.ChatID)
		}
		allowed := s.MonitorAllGroups
		for _, id := range ids {
			if slices.Contains(s.ExcludedGroups, id) {
				return false
			}
			allowed = allowed || slices.Contains(s.EnabledGroups, id)
		}
		if !allowed || len(s.TargetGroups) == 0 {
			return false
		}
		// Older hosts without snapshots may have button keywords absent in e.Text.
		if e.Message == nil {
			return true
		}
		sender := e.Message.SenderID
		if sender == "" {
			sender = strconv.FormatInt(e.SenderID, 10)
		}
		if s.BotID != "" && sender == s.BotID {
			return false
		}
		text := e.Message.Text
		for _, b := range e.Message.Buttons {
			text += " " + b.Text
		}
		text = strings.ToLower(text)
		keys := append([]string(nil), s.Keywords...)
		for _, id := range ids {
			if slices.Contains(s.GroupUsers[id], sender) {
				return true
			}
			keys = append(keys, s.GroupKeywords[id]...)
		}
		for _, k := range keys {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(k)), "re:") {
				return true
			} // preserve full regex semantics in worker
			if strings.Contains(text, strings.ToLower(k)) {
				return true
			}
		}
		return false
	}
}
