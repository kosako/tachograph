package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	// The same roots within the TTL are served from the cache (the cache is
	// keyed by the roots, #321 — another root's miss is covered in
	// freshness_test.go).
	cached := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now.Add(5 * time.Second)})
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
	if err := cache.WriteSnapshot(snap, now.Add(-time.Minute), claudeRoot); err != nil {
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
	if err := cache.WriteSnapshot(snap, now.Add(-cache.SnapshotMaxAge-time.Hour), claudeRoot); err != nil {
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

// A snapshot older than the stale threshold still serves the account-level
// values (limits, model), but its session-scoped values are unknown by then:
// outside the statusline they would otherwise sit next to a daily total
// recomputed right now (#235). A fresh snapshot keeps them as the most
// recently observed session.
func TestStatusDropsSessionValuesFromStaleSnapshot(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")

	snapshotAt := func(age time.Duration) schema.Tool {
		collected := now.Add(-age).Format(time.RFC3339)
		pct, ctx, cost, tokens := 42.0, 24.0, 1.25, int64(4321)
		id := "session-b"
		return schema.Tool{
			Tool:         schema.ToolClaudeCode,
			Available:    true,
			Backend:      schema.BackendSubscription,
			CollectedAt:  &collected,
			Model:        &schema.Model{ID: "claude-opus-5"},
			Session:      &schema.Session{ID: &id, ContextUsedPct: &ctx, Tokens: &schema.Tokens{Total: tokens}},
			Limits:       []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
			Fallback:     &schema.Fallback{SessionTokens: &tokens, EstimatedCostUSD: &cost},
			SessionToday: &schema.Daily{Tokens: tokens, CostUSD: &cost},
		}
	}

	// No transcript fresher than the snapshot (an empty Claude root): a
	// fresher one would take over the session values instead (#263). The
	// snapshot is written for that same root (#321).
	emptyRoot := t.TempDir()
	stale := schema.StaleAfterMinutes*time.Minute + time.Minute
	if err := cache.WriteSnapshot(snapshotAt(stale), now.Add(-stale), emptyRoot); err != nil {
		t.Fatal(err)
	}
	got := Status(Options{ClaudeRoot: emptyRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if !got.Stale {
		t.Fatalf("Stale = false, want true for a %v-old snapshot", stale)
	}
	if got.Session != nil || got.Fallback != nil || got.SessionToday != nil {
		t.Errorf("stale snapshot kept session-scoped values: session=%+v fallback=%+v session_today=%+v", got.Session, got.Fallback, got.SessionToday)
	}
	if got.Limits == nil || got.Model == nil {
		t.Errorf("stale snapshot lost account-level values: limits=%+v model=%+v", got.Limits, got.Model)
	}

	if err := cache.WriteSnapshot(snapshotAt(time.Minute), now.Add(-time.Minute), claudeRoot); err != nil {
		t.Fatal(err)
	}
	got = Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if got.Stale || got.Session == nil || *got.Session.ID != "session-b" || got.Fallback == nil || got.Fallback.EstimatedCostUSD == nil {
		t.Errorf("fresh snapshot must keep the last observed session: %+v", got)
	}
}

// An available Codex whose day scan fails keeps daily null — unknown, not
// zero usage (#180, #187). A regular file where today's day directory goes is
// skipped by the collector's walk, so the session still shows, but the day
// scan can't list it.
func TestStatusKeepsCodexDailyNullWhenDayScanFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a plain file where the directory goes reads as missing on Windows, so it can't stand in for an unreadable directory there")
	}
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	now := time.Date(2026, 7, 4, 2, 0, 0, 0, time.Local)
	root := t.TempDir()
	dayDir := func(d time.Time) string {
		return filepath.Join(root, "sessions", d.Format("2006"), d.Format("01"), d.Format("02"))
	}
	// A session started yesterday and still running.
	rollout := `{"type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n" +
		`{"timestamp":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":0,"output_tokens":0,"total_tokens":1000}}}}` + "\n"
	path := filepath.Join(dayDir(now.AddDate(0, 0, -1)), "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-cdcdcdcdcdcd.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{ClaudeRoot: t.TempDir(), CodexRoot: root, Now: now, NoCache: true}

	// Readable, the same root has a daily.
	if got := Status(opts).Tools[1]; got.Daily == nil {
		t.Fatalf("Codex daily = nil with readable logs, want a figure: %+v", got)
	}

	if err := os.WriteFile(dayDir(now), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Status(opts).Tools[1]
	if !got.Available || got.Error != nil {
		t.Fatalf("Codex = %+v, want available without an error (the session is still readable)", got)
	}
	if got.Daily != nil {
		t.Errorf("Codex daily = %+v, want nil when the day scan fails", got.Daily)
	}
}

// The same contract on the Claude side: a transcript nested under a project
// that can't be read (a dangling symlink, so no chmod — a no-op as root) fails
// the day scan, while the collector, which looks only at the project's own
// transcripts, still shows the session.
func TestStatusKeepsClaudeDailyNullWhenDayScanFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	// The real clock: the day scan judges the dangling link by its own mtime,
	// which is when it is created.
	now := time.Now()
	root := t.TempDir()
	writeClaudeMessage(t, root, now.Add(-time.Minute), 100)
	opts := Options{ClaudeRoot: root, CodexRoot: t.TempDir(), Now: now, NoCache: true}

	// Readable, the same root has a daily.
	if got := Status(opts).Tools[0]; got.Daily == nil {
		t.Fatalf("Claude daily = nil with readable logs, want a figure: %+v", got)
	}

	nested := filepath.Join(root, "projects", "p", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(nested, "broken.jsonl")); err != nil {
		t.Fatal(err)
	}
	got := Status(opts).Tools[0]
	if !got.Available || got.Error != nil {
		t.Fatalf("Claude = %+v, want available without an error (the session is still readable)", got)
	}
	if got.Daily != nil {
		t.Errorf("Claude daily = %+v, want nil when the day scan fails", got.Daily)
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

// History lays each tool's per-day totals along the window, oldest first,
// with zero days present and an unreadable tool left nil for every day.
func TestHistoryAlignsDaysAndMarksUnknownTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a plain file where the directory goes reads as missing on Windows, so it can't stand in for an unreadable directory there")
	}
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	from, to := today.AddDate(0, 0, -2), today.AddDate(0, 0, 1)

	// Codex fixture root has sessions; Claude root is a file, so its
	// projects listing fails and the column is unknown.
	claudeRoot := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudeRoot, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h := History(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: today.Add(2 * time.Hour)}, from, to)

	want := []string{"2026-07-02", "2026-07-03", "2026-07-04"}
	if strings.Join(h.Days, ",") != strings.Join(want, ",") {
		t.Fatalf("Days = %v, want %v", h.Days, want)
	}
	for _, tool := range []string{schema.ToolClaudeCode, schema.ToolCodex} {
		if len(h.Tools[tool]) != len(want) {
			t.Fatalf("Tools[%s] has %d entries, want %d", tool, len(h.Tools[tool]), len(want))
		}
	}
	for i, d := range h.Tools[schema.ToolClaudeCode] {
		if d != nil {
			t.Errorf("Claude day %d = %+v, want nil (projects unreadable)", i, d)
		}
	}
	for i, d := range h.Tools[schema.ToolCodex] {
		if d == nil || d.Tokens != 0 || d.CostUSD != nil {
			t.Errorf("Codex day %d = %+v, want a zero Daily (fixture has no usage in the window)", i, d)
		}
	}
}
