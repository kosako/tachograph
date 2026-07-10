package core

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/schema"
)

const (
	claudeRoot = "../collector/claude/testdata/clauderoot"
	codexRoot  = "../collector/codex/testdata/codexroot"
)

func TestStatusAssemblesBothTools(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	s := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now})

	if s.SchemaVersion != schema.Version {
		t.Errorf("SchemaVersion = %q", s.SchemaVersion)
	}
	if len(s.Tools) != 2 || s.Tools[0].Tool != schema.ToolClaudeCode || s.Tools[1].Tool != schema.ToolCodex {
		t.Fatalf("Tools = %+v", s.Tools)
	}
	if !s.Tools[0].Available || !s.Tools[1].Available {
		t.Errorf("both tools should be available from fixtures: %+v", s.Tools)
	}
	if s.Tools[1].Fallback == nil || s.Tools[1].Fallback.EstimatedCostUSD == nil {
		t.Errorf("Codex session cost was not attached: %+v", s.Tools[1].Fallback)
	}
}

func TestStatusServesFromCache(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	first := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now})

	// Same call with unusable roots must still succeed via the cache.
	cached := Status(Options{ClaudeRoot: t.TempDir(), CodexRoot: t.TempDir(), Now: now.Add(5 * time.Second)})
	if cached.GeneratedAt != first.GeneratedAt {
		t.Errorf("expected cache hit: %q vs %q", cached.GeneratedAt, first.GeneratedAt)
	}

	// NoCache bypasses it.
	live := Status(Options{ClaudeRoot: t.TempDir(), CodexRoot: t.TempDir(), Now: now.Add(5 * time.Second), NoCache: true})
	if live.Tools[0].Available {
		t.Error("NoCache call should have re-collected from empty roots")
	}
}

func TestStatusPrefersClaudeSnapshot(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")

	collected := now.Add(-time.Minute).Format(time.RFC3339)
	pct := 42.0
	snap := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
	}
	if err := cache.WriteSnapshot(snap, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	s := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true})
	got := s.Tools[0]
	if got.Limits == nil || *got.Limits[0].UsedPct != 42.0 {
		t.Errorf("expected snapshot (with limits) to win over transcript route: %+v", got)
	}
}

// The display path ages limits from their original observation: a snapshot
// re-saved recently but carrying limits observed too long ago serves the
// session data without the limits (#186).
func TestStatusDropsSnapshotLimitsPastObservationCeiling(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")

	collected := now.Add(-time.Minute).Format(time.RFC3339)
	pct := 42.0
	snap := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
	}
	if err := cache.WriteSnapshot(snap, now.Add(-cache.SnapshotMaxAge-time.Hour)); err != nil {
		t.Fatal(err)
	}

	s := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true})
	got := s.Tools[0]
	if !got.Available {
		t.Fatalf("Tool = %+v, want the snapshot served", got)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want nil (observation past SnapshotMaxAge)", got.Limits)
	}
}

func TestAddCodexSessionCost(t *testing.T) {
	tool := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Model:     &schema.Model{ID: "gpt-x"},
		Session: &schema.Session{Tokens: &schema.Tokens{
			Input:       120,
			CachedInput: 20,
			Output:      30,
			Total:       150,
		}},
		Fallback: &schema.Fallback{},
	}
	addCodexSessionCost(&tool, pricing.Table{
		"gpt-x": {In: 2, CacheRead: 0.5, Out: 10},
	})

	if tool.Fallback.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil")
	}
	want := (100*2.0 + 20*0.5 + 30*10.0) / 1_000_000
	if got := *tool.Fallback.EstimatedCostUSD; got != want {
		t.Errorf("EstimatedCostUSD = %v, want %v", got, want)
	}
}

func TestAddCodexSessionCostPreservesExistingEstimate(t *testing.T) {
	existing := 9.0
	tool := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Model:     &schema.Model{ID: "gpt-x"},
		Session:   &schema.Session{Tokens: &schema.Tokens{Input: 100, Total: 100}},
		Fallback:  &schema.Fallback{EstimatedCostUSD: &existing},
	}
	addCodexSessionCost(&tool, pricing.Table{
		"gpt-x": {In: 2},
	})

	if got := *tool.Fallback.EstimatedCostUSD; got != existing {
		t.Errorf("EstimatedCostUSD = %v, want existing %v", got, existing)
	}
}
