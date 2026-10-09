// Package notify raises macOS notifications when a rate-limit window's
// headroom drops to a configured threshold (#244). There is no daemon: the
// SwiftBar refresh evaluates the status every tick and hands each crossing
// to SwiftBar's notify URL scheme, so notifications only happen while
// SwiftBar is running the plugin.
package notify

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// State remembers what was already announced, per tool, config root, and
// window, so a threshold fires once per reset cycle. Keys are Key's.
type State map[string]Window

// Key is the State entry of tool's window read from the config root root
// (CLAUDE_CONFIG_DIR / CODEX_HOME, normalized by cache.NormalizeRoot):
// "<tool>/<window>@<root>". Each profile keeps its own record, so plugins for
// different profiles sharing the cache dir neither restart nor silence each
// other's cycles (#364). Neither the tool nor the window holds an "@".
func Key(tool, window, root string) string {
	return tool + "/" + window + "@" + root
}

// Window is one rate-limit window's announcement record.
type Window struct {
	ResetsAt string `json:"resets_at"` // cycle identity; a new value re-arms everything
	Notified []int  `json:"notified"`  // thresholds announced this cycle
}

// Event is one crossing to announce.
type Event struct {
	Key       string  // the State entry it belongs to (see Key)
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
// rises back above it or the window's reset time changes. Only the 5h and
// weekly windows are watched (a collector can report other window sizes).
// Tools that are absent, errored, or stale are skipped and keep their
// record, and so is a window observed longer ago than its tool's stale
// threshold at now — the reading SwiftBar marks ⚠ (core.ObservedStale) — so
// an old reading neither fires nor re-arms a threshold (#338). So is a window
// whose reset time has passed by now (resetPassed), however fresh the
// reading: its cycle is over (#363). Records are kept per config root (Key),
// roots giving each tool's; a tool whose root doesn't normalize is skipped,
// since its record couldn't be told from another profile's — as the caches
// neither store nor match such a root (#321). thresholds must already be
// normalized (see config.NormalizeThresholds).
func Evaluate(s schema.Status, thresholds []int, st State, roots map[string]string, now time.Time) ([]Event, State) {
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
		root, ok := cache.NormalizeRoot(roots[t.Tool])
		if !ok {
			continue
		}
		for _, l := range t.Limits {
			if l.UsedPct == nil || (l.Window != schema.WindowFiveHour && l.Window != schema.WindowWeekly) {
				continue
			}
			if core.ObservedStale(t.Tool, l, now) || resetPassed(l, now) {
				continue
			}
			key := Key(t.Tool, l.Window, root)
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

// resetPassed reports whether l's reset time, when readable, is not after
// now: the window it describes has ended, and its use says nothing about the
// current one. The snapshot still serves such a window while its observation
// is fresh (#363). A limit without a readable reset time isn't reported
// passed — when it ends is unknown.
func resetPassed(l schema.Limit, now time.Time) bool {
	if l.ResetsAt == nil {
		return false
	}
	resets, err := time.Parse(time.RFC3339, *l.ResetsAt)
	return err == nil && !resets.After(now)
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

// URL builds the swiftbar://notify URL for an event. plugin identifies the
// running plugin to SwiftBar (its full path, see cmd's notifyLimits).
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

// Run evaluates s (roots as for Evaluate) and delivers every crossing through
// send, returning the state to persist. A delivery that fails keeps the
// window's previous record so it is retried on the next tick instead of
// being lost.
func Run(s schema.Status, thresholds []int, st State, roots map[string]string, plugin string, now time.Time, send func(string) error) State {
	events, next := Evaluate(s, thresholds, st, roots, now)
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

// Notify is one tick's pass: it loads the record, evaluates s and delivers
// every crossing (Run), and saves the record, holding the record's lock
// (taken with lock, cache.LockNotifyState) throughout, so plugins for
// different profiles sharing the cache dir don't save over each other's
// announcements (#364). When the lock isn't had — another plugin's pass still
// holds it after the wait — the tick neither sends nor saves, and the next
// one evaluates again. A system without a file lock goes on unlocked, or it
// would never notify.
func Notify(s schema.Status, thresholds []int, roots map[string]string, plugin string, now time.Time, lock func() (func(), error), send func(string) error) {
	unlock, err := lock()
	if err == nil {
		defer unlock()
	} else if !errors.Is(err, errors.ErrUnsupported) {
		return
	}
	// The notifications were already sent; a lost record only risks a repeat.
	_ = SaveState(Run(s, thresholds, LoadState(), roots, plugin, now, send))
}

// stateFile is the announcement record in the cache dir.
const stateFile = "notify-state.json"

// stateVersion is the record's format: 2 keys the windows by config root
// (#364). A record in any other format, the root-less one before included,
// is discarded, which at most repeats a notification once, as deleting the
// file would; which profile a root-less entry came from can't be told.
const stateVersion = 2

// stateRecord is the record file's content.
type stateRecord struct {
	Version int   `json:"version"`
	Windows State `json:"windows"`
}

// LoadState reads the persisted State; empty when there is none, or when
// it's in another format.
func LoadState() State {
	var rec stateRecord
	if !cache.ReadJSON(stateFile, &rec) || rec.Version != stateVersion || rec.Windows == nil {
		return State{}
	}
	return rec.Windows
}

// SaveState persists st.
func SaveState(st State) error {
	return cache.WriteJSON(stateFile, stateRecord{Version: stateVersion, Windows: st})
}
