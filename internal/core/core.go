// Package core assembles collector output into the unified status document.
package core

import (
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/collector/codex"
	"github.com/kosako/tachograph/internal/daily"
	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/schema"
)

type Options struct {
	ClaudeRoot string // for tests; default ~/.claude
	CodexRoot  string // for tests; default ~/.codex
	Now        time.Time
	NoCache    bool
}

// Status returns the unified document, served from the TTL cache when fresh.
func Status(opts Options) schema.Status {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if !opts.NoCache {
		if s, ok := cache.ReadStatus(cache.StatusTTL, opts.Now); ok {
			return *s
		}
	}
	s := assemble(opts)
	if !opts.NoCache {
		_ = cache.WriteStatus(&s) // serving the live result matters more than caching it
	}
	return s
}

func assemble(opts Options) schema.Status {
	prices := pricing.Load()
	claudeT := claudeTool(opts)
	codexT := codex.Collect(codex.Options{Root: opts.CodexRoot, Now: opts.Now})
	addCodexSessionCost(&codexT, prices)
	claudeDaily, claudeDailyErr := daily.ClaudeTotals(opts.ClaudeRoot, opts.Now, prices)
	addDaily(&claudeT, claudeDaily, claudeDailyErr)
	codexDaily, codexDailyErr := daily.CodexTotals(opts.CodexRoot, opts.Now, prices)
	addDaily(&codexT, codexDaily, codexDailyErr)
	AddSessionToday(&claudeT, opts.Now, prices)
	return schema.Status{
		SchemaVersion: schema.Version,
		GeneratedAt:   opts.Now.Local().Format(time.RFC3339),
		Tools:         []schema.Tool{claudeT, codexT},
	}
}

// addDaily attaches today's aggregate to an available tool. Cost is set only
// when non-zero (a priced model was seen). A scan error means the total is
// unknown, so Daily stays null instead of reading as zero usage.
func addDaily(t *schema.Tool, tot daily.Totals, err error) {
	if !t.Available || t.Error != nil || err != nil {
		return
	}
	t.Daily = tot.Schema()
}

// addCodexSessionCost estimates the session cost as current model x whole
// cumulative usage. token_count only carries cumulative totals, so a
// mid-session model switch is approximated at the current model's rate —
// daily.cost_usd is the per-event precise figure (#191).
func addCodexSessionCost(t *schema.Tool, prices pricing.Table) {
	if t.Tool != schema.ToolCodex || !t.Available || t.Error != nil ||
		t.Model == nil || t.Session == nil || t.Session.Tokens == nil {
		return
	}
	if t.Fallback != nil && t.Fallback.EstimatedCostUSD != nil {
		return
	}
	r, ok := prices.For(t.Model.ID)
	if !ok {
		return
	}
	u := t.Session.Tokens
	nonCachedInput := u.Input - u.CachedInput
	if nonCachedInput < 0 {
		nonCachedInput = 0
	}
	cost := r.Cost(nonCachedInput, 0, u.CachedInput, u.Output)
	if t.Fallback == nil {
		t.Fallback = &schema.Fallback{}
	}
	t.Fallback.EstimatedCostUSD = &cost
}

// AddSessionToday attaches the current session's today-only totals, computed
// from its transcript. Claude only — Codex's cumulative token_count can't be
// sliced to a single day. No-op when there's no transcript path.
func AddSessionToday(t *schema.Tool, now time.Time, prices pricing.Table) {
	if !t.Available || t.Error != nil || t.Session == nil || t.Session.TranscriptPath == nil {
		return
	}
	t.SessionToday = daily.ClaudeSessionToday(*t.Session.TranscriptPath, now, prices).Schema()
}

// claudeTool prefers a recent statusline snapshot (which carries rate
// limits) over the transcript route (which cannot see them).
//
// Outside the statusline "session" can only mean the most recently observed
// session, and that reading holds only while the snapshot is fresh. Once it
// is stale the session-scoped values (session, fallback, session_today) are
// dropped as unknown instead of being served next to a daily total that is
// recomputed on every call (#235). The account-level rate limits, model,
// plan, and credits keep the snapshot's 30-day retention.
func claudeTool(opts Options) schema.Tool {
	if snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, opts.Now); ok {
		if snap.Stale {
			snap.Session = nil
			snap.Fallback = nil
			snap.SessionToday = nil
		}
		return *snap
	}
	return claude.Collect(claude.Options{Root: opts.ClaudeRoot, Now: opts.Now})
}

// DailyHistory is each tool's usage per local calendar day over a window,
// oldest day first. Tools maps a tool name to one entry per day, aligned
// with Days: a day without usage is a zero Daily, and a nil entry means the
// tool's logs could not be read (unknown, not zero — the same contract as
// Status's daily, #187).
type DailyHistory struct {
	Days  []string // "2006-01-02", oldest first
	Tools map[string][]*schema.Daily
}

// History aggregates the days in [from, to) (local midnights, see
// daily.DayStart) for both tools with the same accounting as Status's daily,
// so today's row equals `tacho status` and yesterday's row is what today's
// was. Nothing is cached: every call re-reads the logs (#242).
func History(opts Options, from, to time.Time) DailyHistory {
	prices := pricing.Load()
	h := DailyHistory{Tools: map[string][]*schema.Daily{}}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		h.Days = append(h.Days, daily.DayKey(d))
	}
	claudeDays, err := daily.ClaudeDays(opts.ClaudeRoot, from, to, prices)
	h.Tools[schema.ToolClaudeCode] = historyColumn(h.Days, claudeDays, err)
	codexDays, err := daily.CodexDays(opts.CodexRoot, from, to, prices)
	h.Tools[schema.ToolCodex] = historyColumn(h.Days, codexDays, err)
	return h
}

// historyColumn lays one tool's per-day totals out along days. A scan error
// leaves every entry nil: the whole column is unknown, since a single
// unreadable file can hold any of the days.
func historyColumn(days []string, totals map[string]daily.Totals, err error) []*schema.Daily {
	col := make([]*schema.Daily, len(days))
	if err != nil {
		return col
	}
	for i, d := range days {
		col[i] = totals[d].Schema()
	}
	return col
}
