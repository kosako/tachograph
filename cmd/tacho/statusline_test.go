package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

func TestRunStatuslineUsesLiveInputAndPreservesDaily(t *testing.T) {
	root := subscriptionStatuslineEnv(t)

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
	subscriptionStatuslineEnv(t)
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
	root := subscriptionStatuslineEnv(t)
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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)
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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)

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
	root := subscriptionStatuslineEnv(t)

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

// A live payload's windows keep their observation time through later
// limit-less payloads: the carried limits still say when they were seen, the
// projection is recomputed from that time (not from the rewrite), and once
// the observation is older than the stale threshold the figures go null with
// reason stale (#295).
func TestRunStatuslinePreservedLimitsKeepObservedAtAndReproject(t *testing.T) {
	root := subscriptionStatuslineEnv(t)

	t0, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	resets := t0.Add(4 * time.Hour) // observed 60 min into the 5h window
	live := fmt.Sprintf(`{"session_id":"s","model":{"id":"claude-test"},"rate_limits":{"five_hour":{"used_percentage":20,"resets_at":%d}}}`, resets.Unix())
	later := `{"session_id":"s","model":{"id":"claude-test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(live), &out, t0); code != 0 {
		t.Fatalf("live exit = %d", code)
	}
	observed := t0.Local().Format(time.RFC3339) // as the collector stamps it, in the runner's zone

	// 10 minutes later, no limits in the payload: the window is carried with
	// its original observation and projected from it (still 60 min in).
	t1 := t0.Add(10 * time.Minute)
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(later), &out, t1); code != 0 {
		t.Fatalf("carry exit = %d", code)
	}
	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, t1, root)
	if !ok || len(snap.Limits) != 1 {
		t.Fatalf("snapshot after carry = %+v, %v", snap, ok)
	}
	l := snap.Limits[0]
	if l.ObservedAt == nil || *l.ObservedAt != observed {
		t.Errorf("carried ObservedAt = %v, want the live observation %s", l.ObservedAt, observed)
	}
	if l.Projection.UnavailableReason != nil || l.Projection.UsedPctAtReset == nil || *l.Projection.UsedPctAtReset != 100 {
		t.Errorf("carried projection = %+v, want 100%% at reset from the original observation", l.Projection)
	}

	// 61 minutes after the observation it is older than Claude's stale
	// threshold: the window is still carried, the figures are withheld.
	t2 := t0.Add(61 * time.Minute)
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(later), &out, t2); code != 0 {
		t.Fatalf("stale exit = %d", code)
	}
	snap, ok = cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, t2, root)
	if !ok || len(snap.Limits) != 1 {
		t.Fatalf("snapshot after stale carry = %+v, %v", snap, ok)
	}
	l = snap.Limits[0]
	if l.ObservedAt == nil || *l.ObservedAt != observed {
		t.Errorf("stale-carried ObservedAt = %v, want %s", l.ObservedAt, observed)
	}
	p := l.Projection
	if p.UnavailableReason == nil || *p.UnavailableReason != schema.ProjectionStale || p.PacePctPerHour != nil || p.UsedPctAtReset != nil || p.HeadroomPctAtReset != nil {
		t.Errorf("stale-carried projection = %+v, want reason stale with the figures null", p)
	}
	if p.ElapsedPctAtObservation == nil || *p.ElapsedPctAtObservation != 20 {
		t.Errorf("stale-carried elapsed = %v, want 20 (the window itself is sound)", p.ElapsedPctAtObservation)
	}
}

// subscriptionStatuslineEnv isolates a statusline run from the runner's
// environment: the cache, the config, both agents' roots (core.Status scans
// no real ~/.claude or ~/.codex), and the backend variables, cleared so a
// payload reads as subscription unless the test sets one after this. Every
// test that runs the statusline uses it (#354): an ANTHROPIC_API_KEY left in
// a developer's shell would turn a limit-less payload into api and stop the
// snapshot carry the tests expect.
func subscriptionStatuslineEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "ANTHROPIC_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	return isolateClaudeRoot(t)
}

// transcriptRespondedAt writes a session transcript whose last API response
// is at, as Claude Code records it, and returns its path.
func transcriptRespondedAt(t *testing.T, at time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"assistant","sessionId":"s","timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `","message":{"id":"m","model":"claude-test","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"r"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// An idle session redraws its statusline (here once its 5h window ended)
// with the weekly reading of its last API response, three hours old. The
// snapshot holds a newer reading from another session: the weekly window
// keeps it, and the 5h window the payload lacks is carried, not erased
// (#330).
func TestRunStatuslineOlderPayloadDoesNotRollBackLimits(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimits(t, now, now.Add(-time.Minute),
		window(schema.WindowFiveHour, 300, 6, now.Add(3*time.Hour)),
		window(schema.WindowWeekly, 10080, 92, now.Add(time.Hour)))

	input := fmt.Sprintf(`{"session_id":"idle","transcript_path":%q,"model":{"id":"claude-test"},"rate_limits":{"seven_day":{"used_percentage":89,"resets_at":%d}}}`,
		transcriptRespondedAt(t, now.Add(-3*time.Hour)), now.Add(time.Hour).Unix())
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.wk.pct} {claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "8% 94%" {
		t.Errorf("statusline output = %q, want the newer weekly and the carried 5h (8%% 94%%)", got)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	got := map[string]float64{}
	for _, l := range snap.Limits {
		got[l.Window] = *l.UsedPct
	}
	if len(got) != 2 || got[schema.WindowWeekly] != 92 || got[schema.WindowFiveHour] != 6 {
		t.Errorf("snapshot limits = %v, want weekly 92 kept and 5h 6 carried", got)
	}
}

// A payload observed later wins even with a lower reading: use only falls
// when the window is reset or the limit raised, and that must show (#330).
func TestRunStatuslineNewerPayloadReplacesLimits(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimits(t, now, now.Add(-10*time.Minute),
		window(schema.WindowWeekly, 10080, 92, now.Add(time.Hour)))

	input := fmt.Sprintf(`{"session_id":"active","transcript_path":%q,"model":{"id":"claude-test"},"rate_limits":{"seven_day":{"used_percentage":3,"resets_at":%d}}}`,
		transcriptRespondedAt(t, now.Add(-5*time.Second)), now.Add(time.Hour).Unix())
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.wk.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "97%" {
		t.Errorf("statusline output = %q, want the newer reading (97%%)", got)
	}
	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok || len(snap.Limits) != 1 || *snap.Limits[0].UsedPct != 3 {
		t.Fatalf("snapshot after a newer, lower reading = %+v, %v", snap, ok)
	}
	if got, ok := core.ObservationTime(snap.Limits[0]); !ok || !got.Equal(now.Add(-5*time.Second)) {
		t.Errorf("observed = %v, %v, want the last response %v", got, ok, now.Add(-5*time.Second))
	}
}

// A payload with one window keeps the snapshot's other running window, with
// its own observation; the limits as a whole date from the older of the two,
// so the carried window still ages out from when it was seen (#186, #330).
func TestRunStatuslineCarriesWindowsThePayloadLacks(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	carriedAt := now.Add(-5 * time.Minute)
	writeClaudeSnapshotWithLimits(t, now, carriedAt,
		window(schema.WindowFiveHour, 300, 30, now.Add(2*time.Hour)),
		window(schema.WindowWeekly, 10080, 50, now.Add(72*time.Hour)))

	input := fmt.Sprintf(`{"session_id":"s","model":{"id":"claude-test"},"rate_limits":{"seven_day":{"used_percentage":51,"resets_at":%d}}}`, now.Add(72*time.Hour).Unix())
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.wk.pct} {claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "49% 70%" {
		t.Errorf("statusline output = %q, want the live weekly and the carried 5h (49%% 70%%)", got)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok || len(snap.Limits) != 2 || snap.Limits[0].Window != schema.WindowFiveHour || snap.Limits[1].Window != schema.WindowWeekly {
		t.Fatalf("snapshot = %+v, %v, want the 5h then the weekly window (ascending window_minutes)", snap, ok)
	}
	// The carried 5h keeps its observation; the live weekly is dated at receipt.
	for i, want := range []time.Time{carriedAt, now} {
		if got, ok := core.ObservationTime(snap.Limits[i]); !ok || !got.Equal(want) {
			t.Errorf("%s observed = %v, %v, want %v", snap.Limits[i].Window, got, ok, want)
		}
	}
	if _, _, got, ok := cache.ReadSnapshotLimits(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root); !ok || !got.Equal(carriedAt) {
		t.Errorf("limits observation = %v, %v, want the older window's %v", got, ok, carriedAt)
	}
}

// A carried window whose observed_at can't be read takes the snapshot's own
// observation time instead of losing it, so it still ages out from when it
// was seen, not from the rewrite (#186, #330).
func TestRunStatuslineDatesCarriedWindowWithoutReadableObservation(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	observed := now.Add(-29 * 24 * time.Hour)
	bad := "not-a-time"
	l := window(schema.WindowFiveHour, 300, 42, now.Add(2*time.Hour))
	l.ObservedAt = &bad
	writeClaudeSnapshotWithLimits(t, now, observed, l)

	input := `{"session_id":"s","model":{"id":"claude-test"}}`
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.5h.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	_, _, got, ok := cache.ReadSnapshotLimits(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	if !ok || !got.Equal(observed) {
		t.Errorf("limits observation = %v, %v, want the snapshot's %v (not re-stamped)", got, ok, observed)
	}
}

// liveClaudeTool is a collected statusline payload whose windows were
// observed at observed.
func liveClaudeTool(now, observed time.Time, windows ...schema.Limit) schema.Tool {
	collected := now.Format(time.RFC3339)
	obs := observed.Format(time.RFC3339)
	for i := range windows {
		windows[i].ObservedAt = &obs
	}
	return schema.Tool{Tool: schema.ToolClaudeCode, Available: true, Backend: schema.BackendSubscription, CollectedAt: &collected, Limits: windows}
}

// snapshotUsed is the snapshot's used_pct by window.
func snapshotUsed(t *testing.T, now time.Time, root string) map[string]float64 {
	t.Helper()
	used := map[string]float64{}
	if snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root); ok {
		for _, l := range snap.Limits {
			used[l.Window] = *l.UsedPct
		}
	}
	return used
}

// The snapshot is read only once its lock is held, and saved before it is
// let go: a session that saved a newer reading while this one waited is
// merged with, not overwritten (#330).
func TestWriteStatuslineSnapshotMergesUnderLock(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	resets := now.Add(time.Hour)
	writeClaudeSnapshotWithLimits(t, now, now.Add(-time.Hour), window(schema.WindowWeekly, 10080, 90, resets))
	tool := liveClaudeTool(now, now.Add(-5*time.Minute),
		window(schema.WindowFiveHour, 300, 4, now.Add(3*time.Hour)),
		window(schema.WindowWeekly, 10080, 91, resets))

	var atUnlock map[string]float64
	lock := func(string) (func(), error) {
		// Another session saves a newer weekly reading while this one waits.
		writeClaudeSnapshotWithLimits(t, now, now.Add(-time.Minute), window(schema.WindowWeekly, 10080, 92, resets))
		return func() { atUnlock = snapshotUsed(t, now, root) }, nil
	}
	writeStatuslineSnapshot(&tool, now, root, lock)

	want := map[string]float64{schema.WindowFiveHour: 4, schema.WindowWeekly: 92}
	if fmt.Sprint(atUnlock) != fmt.Sprint(want) {
		t.Errorf("snapshot at unlock = %v, want %v: this session's 5h merged with the other's newer weekly, saved before the unlock", atUnlock, want)
	}
}

// While another session keeps the snapshot locked past the wait, this
// statusline still merges for its own line but doesn't save, since saving
// could roll back what the holder saves (#330).
func TestWriteStatuslineSnapshotSkipsSaveWithoutLock(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimits(t, now, now.Add(-time.Minute),
		window(schema.WindowFiveHour, 300, 6, now.Add(3*time.Hour)),
		window(schema.WindowWeekly, 10080, 92, now.Add(time.Hour)))
	tool := liveClaudeTool(now, now, window(schema.WindowWeekly, 10080, 95, now.Add(time.Hour)))

	writeStatuslineSnapshot(&tool, now, root, func(string) (func(), error) { return nil, errors.New("still held") })

	if got := snapshotUsed(t, now, root); got[schema.WindowWeekly] != 92 {
		t.Errorf("snapshot weekly = %v, want 92 left as the holder saves it", got[schema.WindowWeekly])
	}
	if len(tool.Limits) != 2 {
		t.Errorf("line limits = %+v, want the payload's weekly with the snapshot's 5h", tool.Limits)
	}
}

// A system without a file lock saves unlocked: not saving would leave the
// snapshot never updated there (#330).
func TestWriteStatuslineSnapshotSavesWhereLockUnsupported(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	writeClaudeSnapshotWithLimits(t, now, now.Add(-time.Minute), window(schema.WindowWeekly, 10080, 92, now.Add(time.Hour)))
	tool := liveClaudeTool(now, now, window(schema.WindowWeekly, 10080, 95, now.Add(time.Hour)))

	writeStatuslineSnapshot(&tool, now, root, func(string) (func(), error) { return nil, errors.ErrUnsupported })

	if got := snapshotUsed(t, now, root); got[schema.WindowWeekly] != 95 {
		t.Errorf("snapshot weekly = %v, want the newer 95 saved", got[schema.WindowWeekly])
	}
}

// End to end with the real lock held by another session the whole time: the
// statusline waits, gives up, still prints its line, and leaves the snapshot
// to the holder (#330).
func TestRunStatuslineLeavesLockedSnapshot(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("no snapshot lock on " + runtime.GOOS)
	}
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	resets := now.Add(time.Hour)
	writeClaudeSnapshotWithLimits(t, now, now.Add(-time.Hour), window(schema.WindowWeekly, 10080, 90, resets))
	unlock, err := cache.LockSnapshot(schema.ToolClaudeCode)
	if err != nil {
		t.Fatalf("LockSnapshot: %v", err)
	}
	defer unlock()

	input := fmt.Sprintf(`{"session_id":"a","transcript_path":%q,"model":{"id":"claude-test"},"rate_limits":{"seven_day":{"used_percentage":91,"resets_at":%d}}}`,
		transcriptRespondedAt(t, now.Add(-5*time.Minute)), resets.Unix())
	var out bytes.Buffer
	if code := runStatuslineWithIO([]string{"--template", "{claude.wk.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "9%" {
		t.Errorf("statusline output = %q, want its own reading (9%%)", got)
	}
	if got := snapshotUsed(t, now, root); got[schema.WindowWeekly] != 90 {
		t.Errorf("snapshot weekly = %v, want 90 left to the holder", got[schema.WindowWeekly])
	}
}

// Two readings in the same second keep their order: the snapshot holds the
// one observed later in that second, not the one that happened to arrive
// last (#330).
func TestRunStatuslineKeepsOrderWithinSecond(t *testing.T) {
	root := subscriptionStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	resets := now.Add(time.Hour)
	second := now.Add(-time.Minute) // both readings fall in this second
	run := func(used int, at time.Time) {
		t.Helper()
		input := fmt.Sprintf(`{"session_id":"s","transcript_path":%q,"model":{"id":"claude-test"},"rate_limits":{"seven_day":{"used_percentage":%d,"resets_at":%d}}}`,
			transcriptRespondedAt(t, at), used, resets.Unix())
		var out bytes.Buffer
		if code := runStatuslineWithIO([]string{"--template", "{claude.wk.pct}", "--no-color"}, strings.NewReader(input), &out, now); code != 0 {
			t.Fatalf("runStatuslineWithIO exit = %d", code)
		}
	}
	run(92, second.Add(900*time.Millisecond))
	run(89, second.Add(100*time.Millisecond))
	if got := snapshotUsed(t, now, root); got[schema.WindowWeekly] != 92 {
		t.Errorf("snapshot weekly = %v, want the reading from later in the second (92)", got[schema.WindowWeekly])
	}
}

// templateStatuslineEnv isolates a statusline run that takes its template
// from the config directory: subscriptionStatuslineEnv with a known
// TACHO_CONFIG_DIR, returned, and the home and XDG directories moved away so
// no real statusline.tmpl can be picked up instead.
func templateStatuslineEnv(t *testing.T) string {
	t.Helper()
	subscriptionStatuslineEnv(t)
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	return dir
}

// fixtureStatusline runs the statusline on the fixture payload with args
// (plus --no-color) and returns the line it prints.
func fixtureStatusline(t *testing.T, now time.Time, args ...string) string {
	t.Helper()
	input, err := os.ReadFile(filepath.Join("..", "..", "internal", "collector", "claude", "testdata", "statusline_input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runStatuslineWithIO(append(args, "--no-color"), bytes.NewReader(input), &out, now); code != 0 {
		t.Fatalf("runStatuslineWithIO exit = %d", code)
	}
	return strings.TrimSpace(out.String())
}

// Without --template or a statusline.tmpl the statusline renders the
// built-in default template.
func TestRunStatuslineDefaultsWithoutTemplateFile(t *testing.T) {
	templateStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")

	want := fixtureStatusline(t, now, "--template", render.DefaultTemplate)
	if got := fixtureStatusline(t, now); got != want {
		t.Errorf("statusline output = %q, want the default template's %q", got, want)
	}
}

// A statusline.tmpl in the config directory supplies the template: its first
// line that is neither blank nor a # comment, and nothing after it.
func TestRunStatuslineUsesTemplateFile(t *testing.T) {
	dir := templateStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	tmpl := "# my statusline\n\n  # {claude.wk.pct} commented out\n{claude.model} 5h {claude.5h.pct}\n{claude.wk.pct} never reached\n"
	if err := os.WriteFile(filepath.Join(dir, "statusline.tmpl"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := fixtureStatusline(t, now), "Fable 5 5h 76%"; got != want { // 23.5% used → 76% left
		t.Errorf("statusline output = %q, want %q (the file's first template line)", got, want)
	}
}

// `tacho config statusline-preset NAME` writes the preset where the next
// statusline run without --template picks it up.
func TestRunStatuslineUsesPresetFromConfig(t *testing.T) {
	templateStatuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	preset, ok := render.PresetTemplate("minimal")
	if !ok {
		t.Fatal("no minimal preset")
	}
	want := fixtureStatusline(t, now, "--template", preset)
	if def := fixtureStatusline(t, now, "--template", render.DefaultTemplate); want == def {
		t.Fatalf("the minimal preset renders like the default (%q), so the run can't tell them apart", want)
	}

	var code int
	capture(t, &os.Stdout, func() { code = run([]string{"config", "statusline-preset", "minimal"}) })
	if code != 0 {
		t.Fatalf("config statusline-preset minimal = %d, want 0", code)
	}
	if got := fixtureStatusline(t, now); got != want {
		t.Errorf("statusline output = %q, want the minimal preset's %q", got, want)
	}
}
