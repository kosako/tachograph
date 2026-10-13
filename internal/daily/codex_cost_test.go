package daily

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/collector/codex"
	"github.com/kosako/tachograph/internal/pricing"
)

// Both ways a day's Codex cost is priced — from a rollout's endpoints (a
// rollout without timestamped events) and event by event — bill the
// growth's cached input at the cache-read rate and only the rest at the
// input rate, through pricing's one Codex rule (#383).
func TestCodexDayCostBillsCachedInputOnce(t *testing.T) {
	prices := pricing.Table{"gpt-a": {In: 2, Out: 10, CacheRead: 0.5}}
	before := &codex.TokenUsage{InputTokens: 100_000, CachedInputTokens: 50_000, OutputTokens: 10_000, TotalTokens: 110_000}
	last := codex.TokenUsage{InputTokens: 1_100_000, CachedInputTokens: 450_000, OutputTokens: 110_000, TotalTokens: 1_210_000}
	// Growth: input 1M, of it cached 400k; output 100k.
	// (600_000*2 + 400_000*0.5 + 100_000*10) / 1e6 = 1.2 + 0.2 + 1.0
	const want = 2.4

	if got := codexDeltaTotals("gpt-a", before, &last, prices).Cost; got != want {
		t.Errorf("codexDeltaTotals cost = %v, want %v", got, want)
	}
	events := []codexTC{{ts: time.Unix(0, 0), usage: last, model: "gpt-a"}}
	if got := codexEventCost(events, before, "gpt-a", prices); got != want {
		t.Errorf("codexEventCost = %v, want %v", got, want)
	}
}
