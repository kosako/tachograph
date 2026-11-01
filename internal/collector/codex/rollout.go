// Rollout discovery and event parsing shared by this collector and
// internal/daily. The on-disk rollout layout and format are an external
// contract owned by Codex CLI, so where rollouts live, their event envelope,
// payload shapes, and session-id rule live here in one place.

package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// RolloutFile is one rollout found by ListRollouts: its path, the
// YYYY/MM/DD directory holding it, and its mtime (zero when it couldn't be
// read).
type RolloutFile struct {
	Path string
	Day  string
	Mod  time.Time
}

// ListRollouts lists every rollout under sessions/YYYY/MM/DD — each
// non-directory .jsonl entry of a day directory — in directory-name order.
// Other entries at any level are skipped. A missing sessions directory means
// no rollouts yet, not a failure.
//
// A failure doesn't stop the walk: a directory that can't be listed is
// skipped whole, and an entry whose mtime can't be read stays listed with a
// zero mtime. err is the first such failure — the first one other than an
// entry removed while listing (fs.ErrNotExist) when there is one, so a later
// unreadable directory isn't hidden behind a vanished one (#437) — and what it
// means is the caller's call: the collector still picks from what was listed,
// while daily takes any gap as unknown totals (#187).
func ListRollouts(sessions string) (files []RolloutFile, err error) {
	years, err := readDir(sessions)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var first error
	fail := func(err error) {
		if first == nil || (errors.Is(first, fs.ErrNotExist) && !errors.Is(err, fs.ErrNotExist)) {
			first = err
		}
	}
	for _, y := range years {
		if !y.IsDir() {
			continue
		}
		for _, month := range subdirs(filepath.Join(sessions, y.Name()), fail) {
			for _, day := range subdirs(month, fail) {
				entries, err := readDir(day)
				if err != nil {
					fail(err)
					continue
				}
				for _, e := range entries {
					if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
						continue
					}
					var mod time.Time
					if info, err := e.Info(); err == nil {
						mod = info.ModTime()
					} else {
						fail(err)
					}
					files = append(files, RolloutFile{filepath.Join(day, e.Name()), day, mod})
				}
			}
		}
	}
	return files, first
}

// readDir is os.ReadDir; tests replace it to fail chosen directories in a
// chosen way.
var readDir = os.ReadDir

// subdirs lists dir's immediate subdirectories in name order. A directory
// that can't be listed goes to fail and yields none — not the part ReadDir
// read before failing.
func subdirs(dir string, fail func(error)) []string {
	entries, err := readDir(dir)
	if err != nil {
		fail(err)
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// Event is one rollout line's envelope.
type Event struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// ParseEvent decodes one rollout line. ok is false for blank or non-JSON
// lines.
func ParseEvent(line []byte) (Event, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Event{}, false
	}
	var ev Event
	if json.Unmarshal(line, &ev) != nil {
		return Event{}, false
	}
	return ev, true
}

// TokenCount decodes the payload of an event_msg/token_count event; nil for
// any other event.
func (e Event) TokenCount() *TokenCount {
	if e.Type != "event_msg" {
		return nil
	}
	var p TokenCount
	if json.Unmarshal(e.Payload, &p) != nil || p.Type != "token_count" {
		return nil
	}
	p.timestamp = e.Timestamp
	return &p
}

// TurnContext decodes the payload of a turn_context event; nil for any other
// event.
func (e Event) TurnContext() *TurnContext {
	if e.Type != "turn_context" {
		return nil
	}
	var p TurnContext
	if json.Unmarshal(e.Payload, &p) != nil {
		return nil
	}
	return &p
}

// TokenCount is a token_count event's payload: cumulative usage plus the
// account-global rate limits as of that turn.
type TokenCount struct {
	timestamp string // envelope timestamp, stamped by Event.TokenCount
	Type      string `json:"type"`
	Info      *struct {
		TotalTokenUsage    *TokenUsage `json:"total_token_usage"`
		LastTokenUsage     *TokenUsage `json:"last_token_usage"`
		ModelContextWindow *int64      `json:"model_context_window"`
	} `json:"info"`
	RateLimits *struct {
		// LimitID names the limit bucket the token_count reports. Codex
		// writes one token_count per bucket: "codex" is the account's own
		// windows; others ("premium", model-specific ones like
		// "codex_bengalfox") can follow it in the same turn. Empty on
		// token_counts from Codex versions that predate buckets (#258).
		LimitID   string    `json:"limit_id"`
		Primary   *rlWindow `json:"primary"`
		Secondary *rlWindow `json:"secondary"`
		Credits   any       `json:"credits"`
		PlanType  *string   `json:"plan_type"`
	} `json:"rate_limits"`
}

// TokenUsage is one token-usage figure of a token_count event. Whether it is
// cumulative depends on the field holding it: TotalTokenUsage is the running
// per-session total, LastTokenUsage the latest request's.
type TokenUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
}

type rlWindow struct {
	UsedPercent   *float64 `json:"used_percent"` // nullable: absent means unknown, not 0 (#322)
	WindowMinutes int      `json:"window_minutes"`
	ResetsAt      int64    `json:"resets_at"` // epoch seconds
}

// Usable reports whether the token_count carries anything a renderer can
// show: usage info, a rate-limit window, a plan, or a credits balance.
// Codex also writes an empty token_count (info null, no windows)
// when a request is refused for a hit usage limit; selecting it as current
// would mask the last valid session's limits at exactly the moment they
// matter most (#205).
func (tc *TokenCount) Usable() bool {
	if i := tc.Info; i != nil &&
		(i.TotalTokenUsage != nil || i.LastTokenUsage != nil || i.ModelContextWindow != nil) {
		return true
	}
	return tc.AccountLimits()
}

// AccountLimits reports whether the token_count carries the account's rate
// limits: a window, a plan, or a credits balance from the "codex" bucket (or
// a pre-bucket token_count without limit_id). Other buckets are not the
// account's windows — taking limits from a "premium" token_count (no
// windows) blanked them, and a model-specific bucket would stand in for the
// account's weekly window (#258).
func (tc *TokenCount) AccountLimits() bool {
	rl := tc.RateLimits
	if rl == nil || (rl.LimitID != "" && rl.LimitID != "codex") {
		return false
	}
	if rl.Primary != nil || rl.Secondary != nil || rl.PlanType != nil {
		return true
	}
	_, ok := creditsBalance(rl.Credits)
	return ok
}

// creditsBalance returns the balance a rate_limits.credits value carries.
// Current Codex writes an object — {"has_credits", "unlimited", "balance"},
// with the balance as a decimal string — while older versions wrote a plain
// number, which is still read. ok is false when there is no finite balance
// to show: a plan without credits, unlimited credits, a missing or
// non-boolean has_credits / unlimited, or a missing or unparsable balance
// (#267). The has_credits:false object on the empty
// token_count of a refused run (#205) therefore keeps it unusable.
func creditsBalance(v any) (balance float64, ok bool) {
	switch c := v.(type) {
	case float64:
		return c, true
	case map[string]any:
		// Both flags must be explicit booleans: a missing or mistyped
		// unlimited must not read as "limited" and surface a balance.
		has, hasOK := c["has_credits"].(bool)
		unlimited, unlimitedOK := c["unlimited"].(bool)
		s, isStr := c["balance"].(string)
		if !hasOK || !has || !unlimitedOK || unlimited || !isStr {
			return 0, false
		}
		// ParseFloat accepts "NaN" / "Inf", which encoding/json can't emit.
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// TurnContext is a turn_context event's payload (emitted at turn start).
type TurnContext struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

// sessionIDRe extracts the session UUID from a rollout filename
// (rollout-<ISO-ts>-<uuid>.jsonl).
var sessionIDRe = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// SessionID extracts the session UUID from a rollout path or filename; ok is
// false when the name carries none. Callers dedup resumed sessions with it: a
// session that resumed into a second rollout keeps the same UUID.
func SessionID(path string) (id string, ok bool) {
	m := sessionIDRe.FindStringSubmatch(path)
	if m == nil {
		return "", false
	}
	return m[1], true
}
