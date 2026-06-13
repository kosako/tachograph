// Package schema defines the unified JSON schema emitted by `tacho status --json`.
// docs/schema.md is the authoritative specification.
package schema

const Version = "0.1"

// Tool name values.
const (
	ToolClaudeCode = "claude-code"
	ToolCodex      = "codex"
)

// Backend values.
const (
	BackendSubscription = "subscription"
	BackendAPI          = "api"
	BackendBedrock      = "bedrock"
	BackendVertex       = "vertex"
	BackendUnknown      = "unknown"
)

// Limit window values.
const (
	WindowFiveHour = "5h"
	WindowWeekly   = "weekly"
)

// StaleAfterMinutes is the age of collected_at beyond which stale is set.
// Rate-limit windows span hours, so a reading stays trustworthy for a while;
// this is deliberately generous so brief idle gaps don't gray everything out.
const StaleAfterMinutes = 60

// Status is the top-level document.
type Status struct {
	SchemaVersion string `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"` // RFC 3339
	Tools         []Tool `json:"tools"`
}

// Tool is one entry per supported agent CLI. Fields that cannot be
// determined are explicit nulls, never omitted (see docs/schema.md).
type Tool struct {
	Tool        string    `json:"tool"`
	Available   bool      `json:"available"`
	Error       *Error    `json:"error"`
	Stale       bool      `json:"stale"`
	CollectedAt *string   `json:"collected_at"` // RFC 3339
	Backend     string    `json:"backend"`
	Plan        *string   `json:"plan"`
	Model       *Model    `json:"model"`
	Session     *Session  `json:"session"`
	Limits      []Limit   `json:"limits"` // nil marshals to null
	Credits     *float64  `json:"credits"`
	Fallback    *Fallback `json:"fallback"`
	Daily       *Daily    `json:"daily"` // today's totals across all sessions
}

// Daily holds today's aggregate usage across every session of a tool.
type Daily struct {
	Tokens  int64    `json:"tokens"`
	CostUSD *float64 `json:"cost_usd"` // nil until pricing is known
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Model struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"display_name"`
}

type Session struct {
	ID             *string  `json:"id"`
	CWD            *string  `json:"cwd"`
	ContextWindow  *int64   `json:"context_window"`
	ContextUsedPct *float64 `json:"context_used_pct"`
	Tokens         *Tokens  `json:"tokens"`
}

type Tokens struct {
	Input       int64 `json:"input"`
	CachedInput int64 `json:"cached_input"`
	Output      int64 `json:"output"`
	Total       int64 `json:"total"`
}

type Limit struct {
	Window        string   `json:"window"`
	WindowMinutes *int     `json:"window_minutes"`
	UsedPct       *float64 `json:"used_pct"`
	ResetsAt      *string  `json:"resets_at"` // RFC 3339
	SavedResets   any      `json:"saved_resets"`
}

// Fallback is the primary display when limits is null (e.g. Bedrock).
type Fallback struct {
	SessionTokens    *int64   `json:"session_tokens"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`
}

// Unavailable returns a Tool entry for an agent whose data source was not found.
func Unavailable(tool string) Tool {
	return Tool{Tool: tool, Backend: BackendUnknown}
}
