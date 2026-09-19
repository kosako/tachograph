package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/schema"
)

// claudeRootWithDays writes one Claude transcript per given day (each with a
// single priced message of tokens input tokens) under a fresh root.
func claudeRootWithDays(t *testing.T, days map[time.Time]int64) string {
	t.Helper()
	root := t.TempDir()
	for day, tokens := range days {
		ts := day.Add(10 * time.Hour)
		line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`+"\n",
			ts.Format(time.RFC3339), tokens)
		path := filepath.Join(root, "projects", "p", day.Format("2006-01-02")+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func historyStatus(todayTokens int64) schema.Status {
	return schema.Status{Tools: []schema.Tool{
		{Tool: schema.ToolClaudeCode, Available: true, Daily: &schema.Daily{Tokens: todayTokens}},
		{Tool: schema.ToolCodex, Available: true},
	}}
}

func unreadableRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.WriteFile(root, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func claudeTokens(h DailyHistory, i int) int64 {
	if d := h.Tools[schema.ToolClaudeCode][i]; d != nil {
		return d.Tokens
	}
	return -1
}

// The first call computes the closed days from the logs and caches them; a
// later call serves them from the cache even when the logs are unreadable,
// while today always comes from the status.
func TestRecentHistoryCachesClosedDays(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	now := today.Add(9 * time.Hour)
	root := claudeRootWithDays(t, map[time.Time]int64{
		today.AddDate(0, 0, -1): 200,
		today.AddDate(0, 0, -3): 400,
		today.AddDate(0, 0, -9): 900, // outside a 7-day window
	})

	h := RecentHistory(Options{ClaudeRoot: root, CodexRoot: t.TempDir(), Now: now}, historyStatus(50), 7, "v1")
	if len(h.Days) != 7 || h.Days[0] != "2026-06-28" || h.Days[6] != "2026-07-04" {
		t.Fatalf("Days = %v, want 2026-06-28 … 2026-07-04", h.Days)
	}
	want := []int64{0, 0, 0, 400, 0, 200, 50}
	for i, w := range want {
		if got := claudeTokens(h, i); got != w {
			t.Errorf("day %s claude tokens = %d, want %d", h.Days[i], got, w)
		}
	}
	for i, d := range h.Tools[schema.ToolCodex][:6] {
		if d == nil || d.Tokens != 0 {
			t.Errorf("codex day %s = %+v, want a zero Daily (empty root)", h.Days[i], d)
		}
	}
	if h.Tools[schema.ToolCodex][6] != nil {
		t.Errorf("codex today = %+v, want nil (status carries no daily)", h.Tools[schema.ToolCodex][6])
	}

	// Served from the cache: unreadable roots would otherwise yield unknown.
	again := RecentHistory(Options{ClaudeRoot: unreadableRoot(t), CodexRoot: unreadableRoot(t), Now: now.Add(time.Hour)}, historyStatus(75), 7, "v1")
	for i, w := range []int64{0, 0, 0, 400, 0, 200, 75} {
		if got := claudeTokens(again, i); got != w {
			t.Errorf("cached day %s claude tokens = %d, want %d", again.Days[i], got, w)
		}
	}
	cached, ok := cache.ReadDailyHistory("v1|")
	if !ok || len(cached) != 6 || cached["2026-06-25"] != nil {
		t.Errorf("cache holds %d days (%v), want the 6 closed days of the window only", len(cached), keys(cached))
	}
}

// A different key (release or pricing change) discards the cache and
// recomputes; an unknown tool is shown as nil, not cached, and filled in on
// the next call once its logs are readable again.
func TestRecentHistoryRekeysAndRetriesUnknown(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	now := today.Add(9 * time.Hour)
	root := claudeRootWithDays(t, map[time.Time]int64{today.AddDate(0, 0, -1): 200})
	codex := t.TempDir()

	RecentHistory(Options{ClaudeRoot: root, CodexRoot: codex, Now: now}, historyStatus(0), 3, "v1")

	// New key with Claude unreadable: Claude unknown (nil) and not cached,
	// Codex recomputed and cached under the new key.
	h := RecentHistory(Options{ClaudeRoot: unreadableRoot(t), CodexRoot: codex, Now: now}, historyStatus(0), 3, "v2")
	if h.Tools[schema.ToolClaudeCode][1] != nil {
		t.Errorf("claude yesterday = %+v under a new key with unreadable logs, want nil", h.Tools[schema.ToolClaudeCode][1])
	}
	if _, ok := cache.ReadDailyHistory("v1|"); ok {
		t.Error("old key still readable, want it discarded")
	}
	cached, ok := cache.ReadDailyHistory("v2|")
	y := cached["2026-07-03"]
	if !ok || y[schema.ToolClaudeCode].Daily != nil || y[schema.ToolClaudeCode].CheckedAt == "" || y[schema.ToolCodex].Daily == nil {
		t.Errorf("v2 cache = %v, want codex known and claude unknown (with its check time) for yesterday", cached)
	}

	// Logs readable again but the unknown was checked moments ago: served
	// as unknown without a rescan (one scan per unknownRetry, not per tick).
	h = RecentHistory(Options{ClaudeRoot: root, CodexRoot: unreadableRoot(t), Now: now.Add(time.Minute)}, historyStatus(0), 3, "v2")
	if h.Tools[schema.ToolClaudeCode][1] != nil {
		t.Errorf("claude yesterday = %+v within the retry interval, want nil (not rescanned)", h.Tools[schema.ToolClaudeCode][1])
	}
	if got := h.Tools[schema.ToolCodex][1]; got == nil || got.Tokens != 0 {
		t.Errorf("codex yesterday = %+v, want its cached zero (an unreadable root must not be rescanned)", got)
	}

	// Past the retry interval the missing tool is computed and cached.
	h = RecentHistory(Options{ClaudeRoot: root, CodexRoot: unreadableRoot(t), Now: now.Add(unknownRetry)}, historyStatus(0), 3, "v2")
	if got := claudeTokens(h, 1); got != 200 {
		t.Errorf("claude yesterday after retry = %d, want 200", got)
	}
	cached, _ = cache.ReadDailyHistory("v2|")
	if cached["2026-07-03"][schema.ToolClaudeCode].Daily == nil {
		t.Error("claude yesterday not cached after the retry")
	}
}

// Inside the grace window after midnight yesterday is computed but not
// stored, since late lines may still land on it; once the grace has passed
// it is cached like any other closed day.
func TestRecentHistoryGraceWindow(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	root := claudeRootWithDays(t, map[time.Time]int64{today.AddDate(0, 0, -1): 200, today.AddDate(0, 0, -2): 100})
	codex := t.TempDir()

	early := RecentHistory(Options{ClaudeRoot: root, CodexRoot: codex, Now: today.Add(5 * time.Minute)}, historyStatus(0), 3, "v1")
	if got := claudeTokens(early, 1); got != 200 {
		t.Errorf("claude yesterday inside grace = %d, want 200 (computed live)", got)
	}
	cached, ok := cache.ReadDailyHistory("v1|")
	if !ok || cached["2026-07-03"] != nil || cached["2026-07-02"] == nil {
		t.Errorf("cache inside grace = %v, want the day before yesterday only", keys(cached))
	}

	RecentHistory(Options{ClaudeRoot: root, CodexRoot: codex, Now: today.Add(closedDayGrace)}, historyStatus(0), 3, "v1")
	cached, _ = cache.ReadDailyHistory("v1|")
	if cached["2026-07-03"] == nil {
		t.Errorf("cache after grace = %v, want yesterday stored", keys(cached))
	}
}

func keys(m map[string]map[string]cache.DailyHistoryEntry) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
