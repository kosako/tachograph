package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/schema"
)

func TestRunStatuslineUsesLiveInputAndPreservesDaily(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("CMUX_WORKSPACE_ID", "")

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
	if err := cache.WriteStatus(cached); err != nil {
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
	if got, want := strings.TrimSpace(out.String()), "Fable 5 5h 24% all 12.7M/d"; got != want {
		t.Fatalf("statusline output = %q, want %q", got, want)
	}

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if len(snap.Limits) != 2 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 23.5 {
		t.Fatalf("snapshot limits = %+v", snap.Limits)
	}
}

func TestRunStatuslineDoesNotOverwriteSnapshotWithEmptyInput(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("CMUX_WORKSPACE_ID", "")
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

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now)
	if !ok || len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot after empty stdin = %+v, %v", snap, ok)
	}
}

func TestRunStatuslineDoesNotOverwriteSnapshotWithEmptyJSON(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("CMUX_WORKSPACE_ID", "")

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

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now)
	if !ok || len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot after empty JSON = %+v, %v", snap, ok)
	}
}

func TestRunStatuslinePreservesSnapshotLimitsWhenLivePayloadOmitsThem(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("CMUX_WORKSPACE_ID", "")

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

	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now)
	if !ok {
		t.Fatal("snapshot was not written")
	}
	if snap.Model == nil || snap.Model.ID != "claude-test" {
		t.Fatalf("snapshot model = %+v", snap.Model)
	}
	if len(snap.Limits) != 1 || snap.Limits[0].UsedPct == nil || *snap.Limits[0].UsedPct != 42 {
		t.Fatalf("snapshot limits = %+v", snap.Limits)
	}
}

func writeClaudeSnapshotWithLimit(t *testing.T, now time.Time, usedPct float64) {
	t.Helper()

	collected := now.Add(-2 * time.Minute).Format(time.RFC3339)
	minutes := 300
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits: []schema.Limit{
			{
				Window:        schema.WindowFiveHour,
				WindowMinutes: &minutes,
				UsedPct:       &usedPct,
			},
		},
	}
	if err := cache.WriteSnapshot(tool); err != nil {
		t.Fatal(err)
	}
}
