// Rollout-event parsing shared by this collector and internal/daily. The
// on-disk rollout format is an external contract owned by Codex CLI, so its
// event envelope, payload shapes, and session-id rule live here in one place.

package codex

import (
	"bytes"
	"encoding/json"
	"regexp"
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
