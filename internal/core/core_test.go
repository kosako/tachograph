package core

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/schema"
)

const (
	claudeRoot = "../collector/claude/testdata/clauderoot"
	codexRoot  = "../collector/codex/testdata/codexroot"
)

func TestStatusAssemblesBothTools(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
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
	if err := cache.WriteSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	s := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true})
	got := s.Tools[0]
	if got.Limits == nil || *got.Limits[0].UsedPct != 42.0 {
		t.Errorf("expected snapshot (with limits) to win over transcript route: %+v", got)
	}
}
