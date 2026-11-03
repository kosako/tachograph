package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/schema"
)

// The statusline route end to end (payload, template, snapshot files) is
// tested through `tacho statusline` in cmd/tacho; these cover the snapshot
// lock, which needs a lock function the CLI can't pass in.

// statuslineEnv isolates the cache and the Claude root, and clears the
// backend variables so the tools read as subscription. It returns the root.
func statuslineEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	return root
}

// seedSnapshot saves a fresh subscription snapshot (collected 2 minutes ago)
// carrying limits observed at observed.
func seedSnapshot(t *testing.T, now, observed time.Time, root string, limits ...schema.Limit) {
	t.Helper()
	collected := now.Add(-2 * time.Minute).Format(time.RFC3339)
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      limits,
	}
	if err := cache.WriteSnapshot(tool, observed, root); err != nil {
		t.Fatal(err)
	}
}

// liveTool is a collected statusline payload whose windows were observed at
// observed.
func liveTool(now, observed time.Time, windows ...schema.Limit) schema.Tool {
	collected := now.Format(time.RFC3339)
	obs := observed.Format(time.RFC3339)
	for i := range windows {
		windows[i].ObservedAt = &obs
	}
	return schema.Tool{Tool: schema.ToolClaudeCode, Available: true, Backend: schema.BackendSubscription, CollectedAt: &collected, Limits: windows}
}

func limitWindow(name string, minutes int, usedPct float64, resetsAt time.Time) schema.Limit {
	r := resetsAt.Format(time.RFC3339)
	return schema.Limit{Window: name, WindowMinutes: &minutes, UsedPct: &usedPct, ResetsAt: &r}
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
	root := statuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	resets := now.Add(time.Hour)
	seedSnapshot(t, now, now.Add(-time.Hour), root, limitWindow(schema.WindowWeekly, 10080, 90, resets))
	tool := liveTool(now, now.Add(-5*time.Minute),
		limitWindow(schema.WindowFiveHour, 300, 4, now.Add(3*time.Hour)),
		limitWindow(schema.WindowWeekly, 10080, 91, resets))

	var atUnlock map[string]float64
	lock := func(string) (func(), error) {
		// Another session saves a newer weekly reading while this one waits.
		seedSnapshot(t, now, now.Add(-time.Minute), root, limitWindow(schema.WindowWeekly, 10080, 92, resets))
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
	root := statuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	seedSnapshot(t, now, now.Add(-time.Minute), root,
		limitWindow(schema.WindowFiveHour, 300, 6, now.Add(3*time.Hour)),
		limitWindow(schema.WindowWeekly, 10080, 92, now.Add(time.Hour)))
	tool := liveTool(now, now, limitWindow(schema.WindowWeekly, 10080, 95, now.Add(time.Hour)))

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
	root := statuslineEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	seedSnapshot(t, now, now.Add(-time.Minute), root, limitWindow(schema.WindowWeekly, 10080, 92, now.Add(time.Hour)))
	tool := liveTool(now, now, limitWindow(schema.WindowWeekly, 10080, 95, now.Add(time.Hour)))

	writeStatuslineSnapshot(&tool, now, root, func(string) (func(), error) { return nil, errors.ErrUnsupported })

	if got := snapshotUsed(t, now, root); got[schema.WindowWeekly] != 95 {
		t.Errorf("snapshot weekly = %v, want the newer 95 saved", got[schema.WindowWeekly])
	}
}

// statusline takes the lock it is given to save the payload's snapshot: the
// wiring the lock tests above rely on, through the route the CLI calls.
func TestStatuslineSavesUnderGivenLock(t *testing.T) {
	statuslineEnv(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	input := []byte(`{"session_id":"s1","model":{"id":"claude-opus-5-5"}}`)

	var locked []string
	statusline(Options{Now: now}, input, func(tool string) (func(), error) {
		locked = append(locked, tool)
		return func() {}, nil
	})
	if fmt.Sprint(locked) != fmt.Sprint([]string{schema.ToolClaudeCode}) {
		t.Errorf("locked = %v, want the Claude snapshot's lock taken once", locked)
	}
}
