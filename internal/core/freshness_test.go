package core

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/schema"
)

// The claudeRoot fixture's newest transcript entry is at 2026-06-12T12:01:30Z
// (model claude-fable-5); these tests place snapshots around it (#263).
const fixtureTranscriptAt = "2026-06-12T12:01:30Z"

func clearClaudeBackendEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "ANTHROPIC_API_KEY"} {
		t.Setenv(k, "")
	}
}

// writeClaudeSnapshot saves a subscription snapshot collected `age` before
// now, carrying a 5h and a weekly limit that reset at the given offsets from
// now, plus session values that must not leak once the snapshot is stale.
func writeClaudeSnapshot(t *testing.T, now time.Time, age, reset5h, resetWeekly time.Duration) {
	t.Helper()
	collected := now.Add(-age).Format(time.RFC3339)
	r5 := now.Add(reset5h).Format(time.RFC3339)
	rw := now.Add(resetWeekly).Format(time.RFC3339)
	p5, pw, tokens := 42.0, 17.0, int64(4321)
	id := "snapshot-session"
	snap := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Model:       &schema.Model{ID: "claude-opus-5"},
		Session:     &schema.Session{ID: &id, Tokens: &schema.Tokens{Total: tokens}},
		Fallback:    &schema.Fallback{SessionTokens: &tokens},
		Limits: []schema.Limit{
			{Window: schema.WindowFiveHour, UsedPct: &p5, ResetsAt: &r5},
			{Window: schema.WindowWeekly, UsedPct: &pw, ResetsAt: &rw},
		},
	}
	if err := cache.WriteSnapshot(snap, now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

// A stale snapshot loses to a fresher transcript — Claude used outside the
// statusline (IDE, desktop, `claude -p`) keeps writing transcripts — so the
// session, model, and collected_at come from the transcript and the row is
// not stale. The account's limits are carried over from the snapshot.
func TestStaleSnapshotYieldsToFresherTranscript(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	writeClaudeSnapshot(t, now, 2*time.Hour, time.Hour, 72*time.Hour)

	got := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if got.Stale {
		t.Error("Stale = true, want false (the transcript is fresh)")
	}
	if got.Model == nil || got.Model.ID != "claude-fable-5" {
		t.Errorf("Model = %+v, want the transcript's claude-fable-5", got.Model)
	}
	if got.Session == nil || got.Session.Tokens == nil || (got.Session.ID != nil && *got.Session.ID == "snapshot-session") {
		t.Errorf("Session = %+v, want the transcript's session", got.Session)
	}
	want, _ := time.Parse(time.RFC3339, fixtureTranscriptAt)
	if got.CollectedAt == nil {
		t.Fatal("CollectedAt is nil")
	}
	if at, err := time.Parse(time.RFC3339, *got.CollectedAt); err != nil || !at.Equal(want) {
		t.Errorf("CollectedAt = %s, want the transcript's %s", *got.CollectedAt, fixtureTranscriptAt)
	}
	if len(got.Limits) != 2 {
		t.Errorf("Limits = %+v, want both snapshot windows carried over", got.Limits)
	}
}

// A carried-over window whose reset time has already passed says nothing
// about the current window, so it is dropped; windows still running stay.
func TestCarriedSnapshotLimitsDropResetWindows(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	writeClaudeSnapshot(t, now, 6*time.Hour, -time.Hour, 72*time.Hour)

	got := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if len(got.Limits) != 1 || got.Limits[0].Window != schema.WindowWeekly {
		t.Errorf("Limits = %+v, want only the weekly window (the 5h window already reset)", got.Limits)
	}
}

// A window without a reset time can't be shown to still be running, so it
// isn't revived on a fresh row either.
func TestCarriedSnapshotLimitsDropWindowsWithoutReset(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	collected := now.Add(-2 * time.Hour).Format(time.RFC3339)
	rw := now.Add(72 * time.Hour).Format(time.RFC3339)
	p5, pw := 42.0, 17.0
	snap := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits: []schema.Limit{
			{Window: schema.WindowFiveHour, UsedPct: &p5}, // no resets_at
			{Window: schema.WindowWeekly, UsedPct: &pw, ResetsAt: &rw},
		},
	}
	if err := cache.WriteSnapshot(snap, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if got.Stale {
		t.Fatal("Stale = true, want the fresher transcript to serve the row")
	}
	if len(got.Limits) != 1 || got.Limits[0].Window != schema.WindowWeekly {
		t.Errorf("Limits = %+v, want only the weekly window (the 5h window has no reset time)", got.Limits)
	}
}

// Limits are carried only between subscription sources, like
// preserveSnapshotLimits: an API-key transcript has no rate limits.
func TestNoLimitsCarriedToNonSubscriptionTranscript(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	writeClaudeSnapshot(t, now, 2*time.Hour, time.Hour, 72*time.Hour)

	got := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if got.Backend != schema.BackendAPI {
		t.Fatalf("Backend = %q, want api from the transcript route", got.Backend)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want none carried into an API-key transcript", got.Limits)
	}
}

// Without a snapshot, a stale transcript follows the same rule as a stale
// snapshot (#235): session-scoped values are unknown, the model stays.
func TestStaleTranscriptDropsSessionScope(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T14:05:00Z")

	got := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	if !got.Stale {
		t.Fatal("Stale = false, want true (the transcript is 2h old)")
	}
	if got.Session != nil || got.Fallback != nil || got.SessionToday != nil {
		t.Errorf("stale transcript kept session-scoped values: session=%+v fallback=%+v session_today=%+v", got.Session, got.Fallback, got.SessionToday)
	}
	if got.Model == nil || got.Model.ID != "claude-fable-5" {
		t.Errorf("Model = %+v, want claude-fable-5 kept", got.Model)
	}
}
