package extensions

import (
	"context"
	"sync/atomic"
	"time"
)

// counters accumulate per-plugin activity between reports. They are read on the
// update path and flushed by the reporter, so every field is atomic.
type counters struct {
	seen      atomic.Int64 // messages this plugin was offered
	filtered  atomic.Int64 // rejected by EventFilter before queueing
	late      atomic.Int64 // already older than eventLateAfter when they arrived
	maxAge    atomic.Int64 // oldest arrival age seen, seconds
	tickCalls atomic.Int64 // tick invocations
	tickMax   atomic.Int64 // slowest tick, milliseconds
	callMax   atomic.Int64 // slowest Handle of any kind, milliseconds
}

// Arrival age is measured against delivery time, not against when the plugin
// got to it: a message Telegram handed us minutes late is normal, and the only
// way to tell our own backlog apart from theirs is to look on the update path.
const eventLateAfter = int64(60)

func (r *runtime) observeIngress(date int) {
	r.stats.seen.Add(1)
	if date <= 0 {
		return
	}
	age := time.Now().Unix() - int64(date)
	if age > eventLateAfter {
		r.stats.late.Add(1)
	}
	peak(&r.stats.maxAge, age)
}

func (r *runtime) observeCall(kind string, elapsed time.Duration) {
	ms := elapsed.Milliseconds()
	peak(&r.stats.callMax, ms)
	if kind == "tick" {
		r.stats.tickCalls.Add(1)
		peak(&r.stats.tickMax, ms)
	}
}

func peak(target *atomic.Int64, value int64) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

type statsSnapshot struct{ seen, filtered, late, maxAge, ticks, tickMax, callMax int64 }

// swapStats drains the window so the next minute starts from zero.
func (r *runtime) swapStats() statsSnapshot {
	return statsSnapshot{
		seen:     r.stats.seen.Swap(0),
		filtered: r.stats.filtered.Swap(0),
		late:     r.stats.late.Swap(0),
		maxAge:   r.stats.maxAge.Swap(0),
		ticks:    r.stats.tickCalls.Swap(0),
		tickMax:  r.stats.tickMax.Swap(0),
		callMax:  r.stats.callMax.Swap(0),
	}
}

// reportStats turns the counters into one line per minute. Nothing else in the
// host surfaces a call that stays under the 1 second slow threshold, which is
// exactly how a once-per-second external request looks like an idle plugin.
// The goroutine ends with the runtime context, so it needs no explicit stop.
func (r *runtime) reportStats(ctx context.Context) {
	if r.logger == nil {
		return
	}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.life.Done():
				return
			case <-t.C:
				s := r.swapStats()
				if s.seen == 0 && s.ticks == 0 {
					continue
				}
				r.logger.Info("plugin.stats",
					"plugin", r.manifest.Name,
					"seen", s.seen,
					"filtered", s.filtered,
					"late", s.late,
					"max_age_s", s.maxAge,
					"ticks", s.ticks,
					"tick_max_ms", s.tickMax,
					"max_call_ms", s.callMax,
					"queued", len(r.queue))
			}
		}
	}()
}
