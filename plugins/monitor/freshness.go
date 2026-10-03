package monitor

import (
	"time"

	api "github.com/OrionG-hub/laowangbot/pkg/pluginapi"
)

const (
	// alertMaxAge bounds how late Telegram may hand us a post and still get it
	// alerted. After updates.channel_too_long the library replays a channel via
	// getDifference, and those posts arrive minutes after they were written;
	// a short freshness window discards them without a trace.
	alertMaxAge = 15 * time.Minute
	// callbackFreshMS is how long a 一键参加 press is still honoured, and with it
	// how long polling the Bot API is worth doing at all.
	callbackFreshMS = 10 * 60 * 1000
)

// stale compares arrival with posting time on purpose: time the event spent
// behind our own serial lane must not count against it, but time lost between
// Telegram and us is exactly what used to be discarded.
func stale(e api.Event) bool {
	received := e.ReceivedAt
	if received == 0 {
		received = time.Now().Unix()
	}
	return received-int64(e.Date) > int64(alertMaxAge/time.Second)
}

// awaitingCallback reports whether a press could still be honoured, which is
// the only reason a tick has to talk to api.telegram.org. That request is plain
// HTTPS with a 5s client timeout and it runs inside the plugin's single lane.
func (m *Monitor) awaitingCallback(now int64) bool {
	for _, a := range m.state.Pending {
		if now-a.Time <= callbackFreshMS {
			return true
		}
	}
	return false
}
