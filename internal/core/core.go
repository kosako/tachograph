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
func claudeTool(opts Options) schema.Tool {
	if snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, opts.Now); ok {
		return *snap
	}
	return claude.Collect(claude.Options{Root: opts.ClaudeRoot, Now: opts.Now})
}
