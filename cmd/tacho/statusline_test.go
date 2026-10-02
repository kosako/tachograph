package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/schema"
)

func TestRunStatuslineUsesLiveInputAndPreservesDaily(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	dailyCost := 1.2
	cached := &schema.Status{
		SchemaVersion: schema.Version,
		GeneratedAt:   now.Format(time.RFC3339),
		Tools: []schema.Tool{
			{
				Tool:      schema.ToolClaudeCode,
				Available: true,
				Backend:   schema.BackendSubscription,
				Daily:     &schema.Daily{Tokens: 12_700_000, CostUSD: &dailyCost},
			},
			schema.Unavailable(schema.ToolCodex),
		},
	}
	if err := cache.WriteStatus(cached, core.Roots(core.Options{})); err != nil {
		t.Fatal(err)
	}

	input, err := os.ReadFile(filepath.Join("..", "..", "internal", "collector", "claude", "testdata", "statusline_input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := runStatuslineWithIO(
		[]string{"--template", "{claude.model} 5h {claude.5h.pct} all {claude.tokens.all}", "--no-color"},
		bytes.NewReader(input),
		&out,
		now,
	)
	if code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if got, want := strings.TrimSpace(out.String()), "Fable 5 5h 76% all 12.7M/d"; got != want { // 23.5% used → 76% left
		t.Fatalf("statusline output = %q, want %q", got, want)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if len(snap.Limits) != 2 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 23.5 {
		t.Fatalf("snapshot limits = %+v", snap.Limits)
	}
}

// The statusline's session tokens cover the whole session tree — the main
// transcript plus the subagent transcripts nested under it — like
// session_today and the cost Claude Code reports (#262).
func TestRunStatuslineCountsSubagentsInSessionTokens(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:05:00+09:00")

	fixture := filepath.Join("..", "..", "internal", "collector", "claude", "testdata")
	b, err := os.ReadFile(filepath.Join(fixture, "clauderoot", "projects", "-Users-example-dev-project", "abc12345-1234-5678-9abc-def012345678.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(mainPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "s", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := `{"type":"assistant","timestamp":"2026-06-12T12:02:00.000Z","message":{"id":"msg_sub","model":"claude-fable-5","role":"assistant","usage":{"input_tokens":100000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}},"requestId":"req_sub"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s", "subagents", "agent-a.jsonl"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(fixture, "statusline_input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload["transcript_path"] = mainPath
	input, _ := json.Marshal(payload)

	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.tokens} today {claude.tokens.session.today}", "--no-color"},
		bytes.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	// main 76,107 + subagent 100,000 = 176,107 → "176k"; main alone would read "76k".
	if got, want := strings.TrimSpace(out.String()), "176k today 176k"; got != want {
		t.Errorf("statusline output = %q, want %q", got, want)
	}
}

func TestRunStatuslineDoesNotOverwriteSnapshotWithEmptyInput(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)
	t.Setenv("HOME", t.TempDir())

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimit(t, now, 42)

	var out bytes.Buffer
	code := runStatuslineWithIO(
		[]string{"--template", "{claude.5h.pct}", "--no-color"},
		strings.NewReader(""),
		&out,
		now,
	)
	if code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok || len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot after empty stdin = %+v, %v", snap, ok)
	}
}

func TestRunStatuslineDoesNotOverwriteSnapshotWithEmptyJSON(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimit(t, now, 42)

	var out bytes.Buffer
	code := runStatuslineWithIO(
		[]string{"--template", "{claude.5h.pct}", "--no-color"},
		strings.NewReader("{}"),
		&out,
		now,
	)
	if code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok || len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot after empty JSON = %+v, %v", snap, ok)
	}
}

func TestRunStatuslinePreservesSnapshotLimitsWhenLivePayloadOmitsThem(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimit(t, now, 42)

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	code := runStatuslineWithIO(
		[]string{"--template", "{claude.model} {claude.5h.pct}", "--no-color"},
		strings.NewReader(input),
		&out,
		now,
	)
	if code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if snap.Model == nil || snap.Model.ID != "claude-test" {
		t.Fatalf("snapshot model = %+v", snap.Model)
	}
	if len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot limits = %+v", snap.Limits)
	}
	// The statusline row is projected too, not only the core.Status one (#295).
	if snap.Limits[0].Projection.Method != schema.ProjectionWindowAverage {
		t.Errorf("preserved limit projection = %+v, want window_average", snap.Limits[0].Projection)
	}
}

func writeClaudeSnapshotWithLimit(t *testing.T, now time.Time, usedPct float64) {
	t.Helper()
	writeClaudeSnapshotWithLimitObserved(t, now, usedPct, now.Add(-2*time.Minute))
}

// writeClaudeSnapshotWithLimitObserved seeds a fresh snapshot (CollectedAt 2
// minutes ago) whose 5h window is still running (it resets in 2h) and whose
// limits were originally observed at observed, so tests can separate
// snapshot freshness from limits freshness.
func writeClaudeSnapshotWithLimitObserved(t *testing.T, now time.Time, usedPct float64, observed time.Time) {
	t.Helper()
	writeClaudeSnapshotWithLimits(t, now, observed,
		window(schema.WindowFiveHour, 300, usedPct, now.Add(2*time.Hour)))
}

// writeClaudeSnapshotWithLimits seeds a fresh subscription snapshot
// (CollectedAt 2 minutes ago) carrying limits observed at observed.
func writeClaudeSnapshotWithLimits(t *testing.T, now, observed time.Time, limits ...schema.Limit) {
	t.Helper()
	collected := now.Add(-2 * time.Minute).Format(time.RFC3339)
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      limits,
	}
	root := core.Roots(core.Options{})[schema.ToolClaudeCode] // the root runStatuslineWithIO resolves (#321)
	if err := cache.WriteSnapshot(tool, observed, root); err != nil {
		t.Fatal(err)
	}
}

// isolateClaudeRoot points CLAUDE_CONFIG_DIR at an empty directory and
// returns it: the snapshot records the root it was observed from (#321), so a
// test's writes and reads must agree on one, and no real transcripts are read.
func isolateClaudeRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}

// window builds a limit; a zero resetsAt leaves the reset time unset.
func window(name string, minutes int, usedPct float64, resetsAt time.Time) schema.Limit {
	l := schema.Limit{Window: name, WindowMinutes: &minutes, UsedPct: &usedPct}
	if !resetsAt.IsZero() {
		r := resetsAt.Format(time.RFC3339)
		l.ResetsAt = &r
	}
	return l
}

// A preserved window must still be running: one whose reset time has passed
// is dropped rather than re-saved under a fresh collected_at, where it would
// read (and notify) as the current window (#318).
func TestRunStatuslineDoesNotPreserveResetLimits(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimits(t, now, now.Add(-2*time.Minute),
		window(schema.WindowFiveHour, 300, 95, now.Add(-time.Hour)))

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "--" {
		t.Errorf("statusline output = %q, want -- (the window already reset)", got)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if snap.Limits != nil {
		t.Fatalf("a window that reset an hour ago was preserved: %+v", snap.Limits)
	}
}

// Mixed windows: the one that reset is dropped and the one still running is
// kept, with the limits' original observation time (#186) intact.
func TestRunStatuslinePreservesOnlyRunningLimits(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	observed := now.Add(-3 * time.Hour)
	writeClaudeSnapshotWithLimits(t, now, observed,
		window(schema.WindowFiveHour, 300, 95, now.Add(-time.Hour)),
		window(schema.WindowWeekly, 10080, 17, now.Add(72*time.Hour)))

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.wk.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if len(snap.Limits) != 1 || snap.Limits[0].Window != schema.WindowWeekly {
		t.Fatalf("snapshot limits = %+v, want only the weekly window", snap.Limits)
	}
	if _, _, got, ok := cache.ReadSnapshotLimits(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root); !ok || !got.Equal(observed) {
		t.Errorf("limits observation = %v, %v, want original %v", got, ok, observed)
	}
}

// A window without a reset time can't be shown to be running and isn't
// preserved either, as on the transcript route (#263).
func TestRunStatuslineDoesNotPreserveLimitsWithoutReset(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimits(t, now, now.Add(-2*time.Minute),
		window(schema.WindowFiveHour, 300, 42, time.Time{}))

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if snap.Limits != nil {
		t.Fatalf("a window without a reset time was preserved: %+v", snap.Limits)
	}
}

// A Bedrock payload must not inherit subscription limits from an older
// snapshot: bedrock/api/vertex keep limits null by the collector contract,
// and the rewritten snapshot must not carry them either (#186).
func TestRunStatuslineDoesNotPreserveLimitsAcrossBackends(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimit(t, now, 42)

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.model}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if snap.Backend != schema.BackendBedrock {
		t.Fatalf("snapshot backend = %q, want bedrock", snap.Backend)
	}
	if snap.Limits != nil {
		t.Fatalf("subscription limits leaked into a bedrock snapshot: %+v", snap.Limits)
	}
}

// Preserved limits keep their original observation time in the rewritten
// snapshot, so a stream of limit-less payloads can't re-stamp them fresh
// forever (#186).
func TestRunStatuslinePreservedLimitsKeepOriginalObservation(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	observed := now.Add(-29 * 24 * time.Hour)
	writeClaudeSnapshotWithLimitObserved(t, now, 42, observed)

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok || len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot after preserve = %+v, %v", snap, ok)
	}
	_, _, got, ok := cache.ReadSnapshotLimits(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("rewritten snapshot lost its limits observation time")
	}
	if !got.Equal(observed) {
		t.Errorf("limits observation = %v, want original %v (not re-stamped)", got, observed)
	}
}

// Limits whose original observation is past SnapshotMaxAge are not preserved,
// even when the snapshot file itself was rewritten recently (#186: the
// laundering case this change closes).
func TestRunStatuslineDropsPreservedLimitsPastMaxAge(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	observed := now.Add(-cache.SnapshotMaxAge - time.Hour)
	writeClaudeSnapshotWithLimitObserved(t, now, 42, observed)

	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if snap.Limits != nil {
		t.Fatalf("limits observed %v ago were still preserved: %+v", now.Sub(observed), snap.Limits)
	}
}

// The written snapshot records the config root it was observed from, so a
// tacho run against another root does not pick it up (#321).
func TestRunStatuslineSnapshotRecordsRoot(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	root := isolateClaudeRoot(t)

	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	input := `{"session_id":"session-1","model":{"id":"claude-test","display_name":"Claude Test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.model}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if _, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root); !ok {
		t.Fatal("snapshot not served to the root it was observed from")
	}
	if _, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, t.TempDir()); ok {
		t.Error("snapshot served to a run against another config root")
	}
}
