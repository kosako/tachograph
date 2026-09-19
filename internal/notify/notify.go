// Package notify raises macOS notifications when a rate-limit window's
// headroom drops to a configured threshold (#244). There is no daemon: the
// SwiftBar refresh evaluates the status every tick and hands each crossing
// to SwiftBar's notify URL scheme, so notifications only happen while
// SwiftBar is running the plugin.
package notify

import (
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// State remembers what was already announced, per tool and window, so a
// threshold fires once per reset cycle. Keys are "<tool>/<window>".
type State map[string]Window

// Window is one rate-limit window's announcement record.
type Window struct {
	ResetsAt string `json:"resets_at"` // cycle identity; a new value re-arms everything
	Notified []int  `json:"notified"`  // thresholds announced this cycle
}

// Event is one crossing to announce.
type Event struct {
	Key       string  // "<tool>/<window>", the State entry it belongs to
	Tool      string  // schema tool name
	Window    string  // schema window name
	Remaining float64 // headroom % at evaluation
	Threshold int     // the deepest threshold newly crossed
	ResetsAt  *string // RFC 3339, when the window resets (nil when unknown)
}

// Evaluate finds the windows whose headroom is at or below a threshold that
// has not been announced this reset cycle. Per window it yields at most one
// event — for the deepest newly crossed threshold — and marks every crossed
// threshold as announced, so a jump from 60% to 5% left says "5%" once
// rather than once per threshold. A threshold re-arms when the headroom
// rises back above it or the window's reset time changes. Tools that are
// absent, errored, or stale are skipped and keep their record; thresholds
// must already be normalized (see config.NormalizeThresholds).
func Evaluate(s schema.Status, thresholds []int, st State) ([]Event, State) {
	next := State{}
	for k, w := range st {
		next[k] = w
	}
	if len(thresholds) == 0 {
		return nil, next
	}
	var events []Event
	for _, t := range s.Tools {
		if !t.Available || t.Error != nil || t.Stale {
			continue
		}
		for _, l := range t.Limits {
			if l.UsedPct == nil {
				continue
			}
			key := t.Tool + "/" + l.Window
			resetsAt := ""
			if l.ResetsAt != nil {
				resetsAt = *l.ResetsAt
			}
			w := next[key]
			if w.ResetsAt != resetsAt {
				w = Window{ResetsAt: resetsAt}
			}
			remaining := render.RemainingPct(*l.UsedPct)
			notified := map[int]bool{}
			for _, n := range w.Notified {
				notified[n] = true
			}
			deepest := 0
			for _, th := range thresholds {
				switch {
				case remaining > float64(th):
					delete(notified, th) // re-armed
				case !notified[th]:
					notified[th] = true
					if deepest == 0 || th < deepest {
						deepest = th
					}
				}
			}
			w.Notified = nil // a fresh slice: the old one may back the caller's State
			for th := range notified {
				w.Notified = append(w.Notified, th)
			}
			sort.Sort(sort.Reverse(sort.IntSlice(w.Notified)))
			next[key] = w
			if deepest != 0 {
				events = append(events, Event{Key: key, Tool: t.Tool, Window: l.Window, Remaining: remaining, Threshold: deepest, ResetsAt: l.ResetsAt})
			}
		}
	}
	return events, next
}

// Body is the notification text for an event, e.g.
// "Claude weekly: 28% left · resets ↻09/20".
func Body(ev Event, now time.Time) string {
	tool := "Codex"
	if ev.Tool == schema.ToolClaudeCode {
		tool = "Claude"
	}
	body := fmt.Sprintf("%s %s: %.0f%% left", tool, ev.Window, ev.Remaining)
	if ev.ResetsAt != nil {
		body += " · resets " + render.ResetShort(*ev.ResetsAt, now)
	}
	return body
}

// URL builds the swiftbar://notify URL for an event. plugin is the running
// plugin's file name, which SwiftBar uses to attribute the notification.
func URL(ev Event, plugin string, now time.Time) string {
	q := url.Values{}
	q.Set("plugin", plugin)
	q.Set("title", "tachograph")
	q.Set("body", Body(ev, now))
	// Query encoding turns spaces into "+", which URL scheme handlers read
	// literally; percent-encode them instead.
	return "swiftbar://notify?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}

// Open hands a URL to macOS (`open`), which routes swiftbar:// to SwiftBar.
func Open(u string) error {
	return exec.Command("open", u).Run()
}

// Run evaluates s and delivers every crossing through send, returning the
// state to persist. A delivery that fails keeps the window's previous record
// so it is retried on the next tick instead of being lost.
func Run(s schema.Status, thresholds []int, st State, plugin string, now time.Time, send func(string) error) State {
	events, next := Evaluate(s, thresholds, st)
	for _, ev := range events {
		if err := send(URL(ev, plugin, now)); err != nil {
			if prev, ok := st[ev.Key]; ok {
				next[ev.Key] = prev
			} else {
				delete(next, ev.Key)
			}
		}
	}
	return next
}

// stateFile is the announcement record in the cache dir.
const stateFile = "notify-state.json"

// LoadState reads the persisted State; empty when there is none.
func LoadState() State {
	st := State{}
	if !cache.ReadJSON(stateFile, &st) || st == nil {
		return State{}
	}
	return st
}

// SaveState persists st.
func SaveState(st State) error {
	return cache.WriteJSON(stateFile, st)
}
