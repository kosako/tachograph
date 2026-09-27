// Rollout-event parsing shared by this collector and internal/daily. The
// on-disk rollout format is an external contract owned by Codex CLI, so its
// event envelope, payload shapes, and session-id rule live here in one place.

package codex

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
)

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

// TokenUsage is one cumulative token-usage snapshot. Codex reports usage as a
// running per-session total, not per turn.
type TokenUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
}

type rlWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"` // epoch seconds
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
// to show: a plan without credits, unlimited credits, or a missing or
// unparsable balance (#267). The has_credits:false object on the empty
// token_count of a refused run (#205) therefore keeps it unusable.
func creditsBalance(v any) (balance float64, ok bool) {
	switch c := v.(type) {
	case float64:
		return c, true
	case map[string]any:
		has, _ := c["has_credits"].(bool)
		unlimited, _ := c["unlimited"].(bool)
		s, isStr := c["balance"].(string)
		if !has || unlimited || !isStr {
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
