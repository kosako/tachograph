// Package schema defines the unified JSON schema emitted by `tacho status --json`.
// docs/schema.md is the authoritative specification.
package schema

const Version = "3.1"

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
	Tool         string    `json:"tool"`
	Available    bool      `json:"available"`
	Error        *Error    `json:"error"`
	Stale        bool      `json:"stale"`
	CollectedAt  *string   `json:"collected_at"` // RFC 3339
	Backend      string    `json:"backend"`
	Plan         *string   `json:"plan"`
	Model        *Model    `json:"model"`
	Session      *Session  `json:"session"`
	Limits       []Limit   `json:"limits"` // nil marshals to null
	Credits      *float64  `json:"credits"`
	Fallback     *Fallback `json:"fallback"`
	Daily        *Daily    `json:"daily"`         // today's totals across all sessions
	SessionToday *Daily    `json:"session_today"` // current session's totals, today only (Claude only)
}

// Daily holds today's aggregate usage across every session of a tool. Tokens
// is the billing volume — Input (cache writes and cache reads included) plus
// Output, the same measure as Tokens.Total on a session — so it shares a
// denominator with CostUSD (#234; schema 2.0).
type Daily struct {
	Tokens      int64    `json:"tokens"`       // Input + Output as the provider reports it
	Input       int64    `json:"input"`        // incl. cache writes and cache reads
	CachedInput int64    `json:"cached_input"` // cache reads within Input
	Output      int64    `json:"output"`
	CostUSD     *float64 `json:"cost_usd"` // nil until pricing is known
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Model struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"display_name"`
	Effort      *string `json:"effort"` // reasoning effort (low|medium|high|xhigh|max); null when unsupported
}

type Session struct {
	ID             *string  `json:"id"`
	CWD            *string  `json:"cwd"`
	ContextWindow  *int64   `json:"context_window"`
	ContextUsedPct *float64 `json:"context_used_pct"`
	Tokens         *Tokens  `json:"tokens"`
	TranscriptPath *string  `json:"transcript_path,omitempty"` // local jsonl; the session tree totals are read from it
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
	// ObservedAt is when this window's used_pct and resets_at were observed
	// (RFC 3339): a limit carried over from an older snapshot keeps its own
	// observation time, which can be older than the tool's collected_at
	// (#295).
	ObservedAt *string `json:"observed_at"`
	// Projection extrapolates this observation to the reset (#295). Always
	// present with fixed keys; see Projection.
	Projection Projection `json:"projection"`
}

// Projection is the window-average extrapolation of a limit from its latest
// observation alone: the window is taken to have begun at resets_at minus
// window_minutes with nothing used, so the pace is the use observed divided
// by the time elapsed, and the use at reset that pace held for the whole
// window. No history is kept or read (#295). It is a reference for "if the
// average so far holds", not a guarantee of what can still be spent. When the
// figures can't be computed, the three of them are null together and
// UnavailableReason says why; the elapsed share stays whenever the window
// itself is sound.
type Projection struct {
	Method                  string   `json:"method"` // always ProjectionWindowAverage
	ElapsedPctAtObservation *float64 `json:"elapsed_pct_at_observation"`
	PacePctPerHour          *float64 `json:"pace_pct_per_hour"` // percentage points per hour
	UsedPctAtReset          *float64 `json:"used_pct_at_reset"` // not capped: above 100 means the pace would overrun the window
	HeadroomPctAtReset      *float64 `json:"headroom_pct_at_reset"`
	UnavailableReason       *string  `json:"unavailable_reason"`
}

// ProjectionWindowAverage is the only projection method so far.
const ProjectionWindowAverage = "window_average"

// Reasons a projection is unavailable, in the order they are checked.
const (
	ProjectionMissingInput      = "missing_input"      // used_pct, resets_at, window_minutes, or observed_at is missing
	ProjectionInvalidWindow     = "invalid_window"     // the window length is not positive, or the observation falls outside the window
	ProjectionUnsupportedWindow = "unsupported_window" // the window has saved_resets, so it doesn't begin empty at resets_at − window_minutes
	ProjectionResetPassed       = "reset_passed"       // resets_at is not after now
	ProjectionStale             = "stale"              // the observation is older than the tool's stale threshold
)

// Fallback is the primary display when limits is null (e.g. Bedrock).
type Fallback struct {
	SessionTokens    *int64   `json:"session_tokens"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`
}

// Unavailable returns a Tool entry for an agent whose data source was not found.
func Unavailable(tool string) Tool {
	return Tool{Tool: tool, Backend: BackendUnknown}
}
