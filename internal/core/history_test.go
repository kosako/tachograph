package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/daily"
	"github.com/kosako/tachograph/internal/schema"
)

// claudeRootWithDays writes one Claude transcript per given day (each with a
// single priced message of tokens input tokens) under a fresh root.
func claudeRootWithDays(t *testing.T, days map[time.Time]int64) string {
	t.Helper()
	root := t.TempDir()
	for day, tokens := range days {
		writeClaudeMessage(t, root, day.Add(10*time.Hour), tokens)
	}
	return root
}

// writeClaudeMessage writes a transcript under root holding a single priced
// message of tokens input tokens stamped ts, last modified at ts.
func writeClaudeMessage(t *testing.T, root string, ts time.Time, tokens int64) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`+"\n",
		ts.Format(time.RFC3339), tokens)
	path := filepath.Join(root, "projects", "p", ts.Format("2006-01-02")+".jsonl")
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

func historyStatus(todayTokens int64) schema.Status {
	return schema.Status{Tools: []schema.Tool{
		{Tool: schema.ToolClaudeCode, Available: true, Daily: &schema.Daily{Tokens: todayTokens}},
		{Tool: schema.ToolCodex, Available: true},
	}}
}

// hideRoot makes root's logs unreadable in place — the directory moved aside,
// a plain file left at its path — and returns a func that puts it back. The
// root keeps its name, so the history cache (tied to the roots, #337) still
// applies: whatever comes back for a closed day comes from the cache.
func hideRoot(t *testing.T, root string) (restore func()) {
	t.Helper()
	aside := root + ".hidden"
	if err := os.Rename(root, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.Remove(root); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(aside, root); err != nil {
			t.Fatal(err)
		}
	}
}

// cachedDay is the history cache's entry for the local calendar day date,
// looked up under the day's span as RecentHistory keys it.
func cachedDay(t *testing.T, cached map[string]map[string]cache.DailyHistoryEntry, date string) map[string]cache.DailyHistoryEntry {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return cached[daySpan(d)]
}

// setLocal switches the local timezone for the rest of the test.
func setLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
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

	opts := Options{ClaudeRoot: root, CodexRoot: t.TempDir(), Now: now}
	h := RecentHistory(opts, historyStatus(50), 7, "v1")
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

	// Served from the cache: the same roots, now unreadable, would otherwise
	// yield unknown.
	hideRoot(t, opts.ClaudeRoot)
	hideRoot(t, opts.CodexRoot)
	opts.Now = now.Add(time.Hour)
	again := RecentHistory(opts, historyStatus(75), 7, "v1")
	for i, w := range []int64{0, 0, 0, 400, 0, 200, 75} {
		if got := claudeTokens(again, i); got != w {
			t.Errorf("cached day %s claude tokens = %d, want %d", again.Days[i], got, w)
		}
	}
	cached, ok := cache.ReadDailyHistory("v1|", Roots(opts))
	if !ok || len(cached) != 6 || cachedDay(t, cached, "2026-06-25") != nil {
		t.Errorf("cache holds %d days (%v), want the 6 closed days of the window only", len(cached), keys(cached))
	}
}

// A different key (release or pricing change) discards the cache and
// recomputes; an unknown tool is shown as nil, not cached, and filled in on
// the next call once its logs are readable again.
func TestRecentHistoryRekeysAndRetriesUnknown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a plain file where the directory goes reads as missing on Windows, so it can't stand in for an unreadable directory there")
	}
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	now := today.Add(9 * time.Hour)
	opts := Options{
		ClaudeRoot: claudeRootWithDays(t, map[time.Time]int64{today.AddDate(0, 0, -1): 200}),
		CodexRoot:  t.TempDir(),
		Now:        now,
	}
	roots := Roots(opts)

	RecentHistory(opts, historyStatus(0), 3, "v1")

	// New key with Claude unreadable: Claude unknown (nil) and not cached,
	// Codex recomputed and cached under the new key.
	showClaude := hideRoot(t, opts.ClaudeRoot)
	h := RecentHistory(opts, historyStatus(0), 3, "v2")
	if h.Tools[schema.ToolClaudeCode][1] != nil {
		t.Errorf("claude yesterday = %+v under a new key with unreadable logs, want nil", h.Tools[schema.ToolClaudeCode][1])
	}
	if _, ok := cache.ReadDailyHistory("v1|", roots); ok {
		t.Error("old key still readable, want it discarded")
	}
	cached, ok := cache.ReadDailyHistory("v2|", roots)
	y := cachedDay(t, cached, "2026-07-03")
	if !ok || y[schema.ToolClaudeCode].Daily != nil || y[schema.ToolClaudeCode].CheckedAt == "" || y[schema.ToolCodex].Daily == nil {
		t.Errorf("v2 cache = %v, want codex known and claude unknown (with its check time) for yesterday", cached)
	}

	// Logs readable again but the unknown was checked moments ago: served
	// as unknown without a rescan (one scan per unknownRetry, not per tick).
	showClaude()
	hideRoot(t, opts.CodexRoot)
	opts.Now = now.Add(time.Minute)
	h = RecentHistory(opts, historyStatus(0), 3, "v2")
	if h.Tools[schema.ToolClaudeCode][1] != nil {
		t.Errorf("claude yesterday = %+v within the retry interval, want nil (not rescanned)", h.Tools[schema.ToolClaudeCode][1])
	}
	if got := h.Tools[schema.ToolCodex][1]; got == nil || got.Tokens != 0 {
		t.Errorf("codex yesterday = %+v, want its cached zero (an unreadable root must not be rescanned)", got)
	}

	// Past the retry interval the missing tool is computed and cached.
	opts.Now = now.Add(unknownRetry)
	h = RecentHistory(opts, historyStatus(0), 3, "v2")
	if got := claudeTokens(h, 1); got != 200 {
		t.Errorf("claude yesterday after retry = %d, want 200", got)
	}
	cached, _ = cache.ReadDailyHistory("v2|", roots)
	if cachedDay(t, cached, "2026-07-03")[schema.ToolClaudeCode].Daily == nil {
		t.Error("claude yesterday not cached after the retry")
	}
}

// Editing pricing.json changes the cache key as a new build does, so the
// closed days are repriced instead of served at the old rates (#243).
func TestRecentHistoryRecomputesAfterPricingChange(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	writePricing := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "pricing.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claudeCost := func(h DailyHistory, i int) float64 {
		if d := h.Tools[schema.ToolClaudeCode][i]; d != nil && d.CostUSD != nil {
			return *d.CostUSD
		}
		return -1
	}
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	opts := Options{
		ClaudeRoot: claudeRootWithDays(t, map[time.Time]int64{today.AddDate(0, 0, -1): 200}),
		CodexRoot:  t.TempDir(),
		Now:        today.Add(9 * time.Hour),
	}

	writePricing(`{"claude-fable":{"input":1}}`)
	if got, want := claudeCost(RecentHistory(opts, historyStatus(0), 3, "v1"), 1), 200*1.0/1e6; got != want {
		t.Fatalf("claude yesterday cost = %v, want %v (pricing.json's rate)", got, want)
	}

	// Another length, so the override's stamp changes even where two writes
	// share an mtime.
	writePricing(`{"claude-fable":{"input":2.5}}`)
	opts.Now = opts.Now.Add(time.Minute)
	if got, want := claudeCost(RecentHistory(opts, historyStatus(0), 3, "v1"), 1), 200*2.5/1e6; got != want {
		t.Errorf("claude yesterday cost after editing pricing.json = %v, want %v (recomputed, not the cached %v)", got, want, 200*1.0/1e6)
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

	roots := Roots(Options{ClaudeRoot: root, CodexRoot: codex})

	early := RecentHistory(Options{ClaudeRoot: root, CodexRoot: codex, Now: today.Add(5 * time.Minute)}, historyStatus(0), 3, "v1")
	if got := claudeTokens(early, 1); got != 200 {
		t.Errorf("claude yesterday inside grace = %d, want 200 (computed live)", got)
	}
	cached, ok := cache.ReadDailyHistory("v1|", roots)
	if !ok || cachedDay(t, cached, "2026-07-03") != nil || cachedDay(t, cached, "2026-07-02") == nil {
		t.Errorf("cache inside grace = %v, want the day before yesterday only", keys(cached))
	}

	RecentHistory(Options{ClaudeRoot: root, CodexRoot: codex, Now: today.Add(closedDayGrace)}, historyStatus(0), 3, "v1")
	cached, _ = cache.ReadDailyHistory("v1|", roots)
	if cachedDay(t, cached, "2026-07-03") == nil {
		t.Errorf("cache after grace = %v, want yesterday stored", keys(cached))
	}
}

// Across a skipped midnight (America/Santiago, 2026-09-06) both History and
// RecentHistory list each date once, today's row is today's, and the evening
// before the skip keeps its last hour (#346).
func TestHistoryAcrossSkippedMidnight(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tz database entry for America/Santiago here: %v", err)
	}
	setLocal(t, santiago)
	root := t.TempDir()
	writeClaudeMessage(t, root, time.Date(2026, 9, 5, 23, 30, 0, 0, santiago), 100) // the last hour before the skip
	writeClaudeMessage(t, root, time.Date(2026, 9, 6, 1, 30, 0, 0, santiago), 200)  // just after it
	opts := Options{ClaudeRoot: root, CodexRoot: t.TempDir(), Now: time.Date(2026, 9, 7, 12, 0, 0, 0, santiago)}
	wantDays := "[2026-09-05 2026-09-06 2026-09-07]"

	h := History(opts, daily.DayStartFrom(opts.Now, -2), daily.DayStartFrom(opts.Now, 1))
	if fmt.Sprint(h.Days) != wantDays || claudeTokens(h, 0) != 100 || claudeTokens(h, 1) != 200 {
		t.Errorf("History: days %v, claude %d / %d, want %s with 100 / 200", h.Days, claudeTokens(h, 0), claudeTokens(h, 1), wantDays)
	}

	r := RecentHistory(opts, historyStatus(50), 3, "v1")
	if fmt.Sprint(r.Days) != wantDays || claudeTokens(r, 0) != 100 || claudeTokens(r, 1) != 200 || claudeTokens(r, 2) != 50 {
		t.Errorf("RecentHistory: days %v, claude %d / %d / %d, want %s with 100 / 200 / today's 50", r.Days, claudeTokens(r, 0), claudeTokens(r, 1), claudeTokens(r, 2), wantDays)
	}
}

// The cache is tied to the config roots its days were read from: after a
// switch to another profile's root the closed days are recomputed from that
// profile's logs instead of served from the previous one's cache (#337).
func TestRecentHistoryRecomputesForAnotherRoot(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	today := time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	now := today.Add(9 * time.Hour)
	a := claudeRootWithDays(t, map[time.Time]int64{today.AddDate(0, 0, -1): 200})
	b := claudeRootWithDays(t, map[time.Time]int64{today.AddDate(0, 0, -1): 300})
	codex := t.TempDir()

	for _, c := range []struct {
		name, root string
		want       int64
	}{
		{"profile a", a, 200},
		{"profile b after a", b, 300},
		{"back on profile a", a, 200},
	} {
		h := RecentHistory(Options{ClaudeRoot: c.root, CodexRoot: codex, Now: now}, historyStatus(0), 3, "v1")
		if got := claudeTokens(h, 1); got != c.want {
			t.Errorf("%s: claude yesterday = %d, want %d (from its own logs)", c.name, got, c.want)
		}
	}
}

// Days are cut at local midnights, so each is cached under the instants it
// spans: after a timezone change the closed days are recomputed under the
// new midnights instead of served under the same dates (#337).
func TestRecentHistoryRecomputesAfterTimezoneChange(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	setLocal(t, time.UTC)
	root := t.TempDir()
	// 2026-07-02 20:00 in UTC is 2026-07-03 05:00 at UTC+9.
	writeClaudeMessage(t, root, time.Date(2026, 7, 2, 20, 0, 0, 0, time.UTC), 500)
	opts := Options{ClaudeRoot: root, CodexRoot: t.TempDir(), Now: time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)}

	h := RecentHistory(opts, historyStatus(0), 3, "v1")
	if h.Days[0] != "2026-07-02" || claudeTokens(h, 0) != 500 || claudeTokens(h, 1) != 0 {
		t.Fatalf("in UTC: days %v, claude %d / %d, want 2026-07-02 = 500 and 2026-07-03 = 0", h.Days, claudeTokens(h, 0), claudeTokens(h, 1))
	}

	setLocal(t, time.FixedZone("UTC+9", 9*60*60))
	h = RecentHistory(opts, historyStatus(0), 3, "v1")
	if h.Days[0] != "2026-07-02" || claudeTokens(h, 0) != 0 || claudeTokens(h, 1) != 500 {
		t.Errorf("at UTC+9: days %v, claude %d / %d, want 2026-07-02 = 0 and 2026-07-03 = 500 (recomputed under the new midnights)", h.Days, claudeTokens(h, 0), claudeTokens(h, 1))
	}
}

// daySpan names the instants a local day covers, offsets included, so the
// same date in two timezones is two keys; any instant of the day gives the
// same span.
func TestDaySpan(t *testing.T) {
	setLocal(t, time.FixedZone("UTC+9", 9*60*60))
	want := "2026-07-03T00:00:00+09:00/2026-07-04T00:00:00+09:00"
	for _, d := range []time.Time{
		time.Date(2026, 7, 3, 0, 0, 0, 0, time.Local),
		time.Date(2026, 7, 3, 23, 59, 59, 0, time.Local),
		time.Date(2026, 7, 3, 5, 0, 0, 0, time.UTC), // 14:00 at UTC+9
	} {
		if got := daySpan(d); got != want {
			t.Errorf("daySpan(%v) = %q, want %q", d, got, want)
		}
	}
	setLocal(t, time.UTC)
	if got := daySpan(time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)); got == want {
		t.Errorf("daySpan in UTC = %q, the same key as at UTC+9", got)
	}

	// A daylight-saving switch makes a 23-hour day; its span still runs from
	// its midnight to the next one.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database here: %v", err)
	}
	setLocal(t, ny)
	if got, want := daySpan(time.Date(2026, 3, 8, 12, 0, 0, 0, ny)), "2026-03-08T00:00:00-05:00/2026-03-09T00:00:00-04:00"; got != want {
		t.Errorf("daySpan on the DST switch = %q, want %q", got, want)
	}

	// Where the switch skips midnight (America/Santiago, 2026-09-06), the day
	// before runs to the switch and the day itself starts there (#346).
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tz database entry for America/Santiago here: %v", err)
	}
	setLocal(t, santiago)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 9, 5, 12, 0, 0, 0, santiago), "2026-09-05T00:00:00-04:00/2026-09-06T01:00:00-03:00"},
		{time.Date(2026, 9, 6, 12, 0, 0, 0, santiago), "2026-09-06T01:00:00-03:00/2026-09-07T00:00:00-03:00"},
	} {
		if got := daySpan(c.at); got != c.want {
			t.Errorf("daySpan(%s) = %q, want %q", c.at.Format(time.RFC3339), got, c.want)
		}
	}
}

func keys(m map[string]map[string]cache.DailyHistoryEntry) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
