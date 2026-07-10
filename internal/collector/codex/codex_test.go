package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func TestCollect(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-24T13:45:00Z")
	got := Collect(Options{Root: "testdata/codexroot", Now: now})

	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v, want available with no error", got.Available, got.Error)
	}
	if got.Tool != schema.ToolCodex {
		t.Errorf("Tool = %q", got.Tool)
	}
	if got.Backend != schema.BackendSubscription {
		t.Errorf("Backend = %q, want subscription (plan_type present)", got.Backend)
	}
	if got.Plan == nil || *got.Plan != "prolite" {
		t.Errorf("Plan = %v, want prolite", got.Plan)
	}
	if got.Model == nil || got.Model.ID != "gpt-5.4-codex" {
		t.Errorf("Model = %+v, want gpt-5.4-codex", got.Model)
	}
	if got.Stale {
		t.Error("Stale = true, want false (event ~5min before now)")
	}

	s := got.Session
	if s == nil {
		t.Fatal("Session is nil")
	}
	if s.ID == nil || *s.ID != "019e5933-2289-7e72-88fd-c494201693fa" {
		t.Errorf("Session.ID = %v", s.ID)
	}
	if s.CWD == nil || *s.CWD != "/Users/example/dev/project" {
		t.Errorf("Session.CWD = %v", s.CWD)
	}
	if s.ContextWindow == nil || *s.ContextWindow != 258400 {
		t.Errorf("ContextWindow = %v", s.ContextWindow)
	}
	if s.Tokens == nil || s.Tokens.Total != 989120 || s.Tokens.CachedInput != 803712 {
		t.Errorf("Tokens = %+v", s.Tokens)
	}
	// last_token_usage.total / context_window = 68190/258400 ≈ 26.39%
	if s.ContextUsedPct == nil || *s.ContextUsedPct < 26.3 || *s.ContextUsedPct > 26.5 {
		t.Errorf("ContextUsedPct = %v, want ≈26.39", s.ContextUsedPct)
	}

	if len(got.Limits) != 2 {
		t.Fatalf("Limits = %+v, want 2 entries", got.Limits)
	}
	five, weekly := got.Limits[0], got.Limits[1]
	if five.Window != schema.WindowFiveHour || *five.UsedPct != 5.0 || *five.WindowMinutes != 300 {
		t.Errorf("5h limit = %+v", five)
	}
	if weekly.Window != schema.WindowWeekly || *weekly.UsedPct != 4.0 || *weekly.WindowMinutes != 10080 {
		t.Errorf("weekly limit = %+v", weekly)
	}
	wantReset := time.Unix(1779646858, 0).Local().Format(time.RFC3339)
	if five.ResetsAt == nil || *five.ResetsAt != wantReset {
		t.Errorf("5h ResetsAt = %v, want %s", five.ResetsAt, wantReset)
	}

	if got.Fallback == nil || got.Fallback.SessionTokens == nil || *got.Fallback.SessionTokens != 989120 {
		t.Errorf("Fallback = %+v", got.Fallback)
	}
}

func TestCollectStale(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-24T19:00:00Z") // ~5h20m after the event, past Codex's 5h threshold
	got := Collect(Options{Root: "testdata/codexroot", Now: now})
	if !got.Stale {
		t.Error("Stale = false, want true past the 5h threshold")
	}
}

// Codex uses a longer (5h) stale threshold than the shared 60-minute default,
// because its rate-limit windows stay valid for hours after the last turn.
// Data 2h20m old is past the 60-min default but still fresh for Codex.
func TestCollectFreshWithinFiveHours(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-24T16:00:00Z") // ~2h20m after the event
	got := Collect(Options{Root: "testdata/codexroot", Now: now})
	if got.Stale {
		t.Error("Stale = true, want false within Codex's 5h threshold")
	}
}

// Exactly 5h after the last event is still fresh (the threshold is "older than
// 5h", a strict >), so the boundary doesn't flip a turn early.
func TestCollectStaleBoundaryExactlyFiveHours(t *testing.T) {
	// last token_count event is 2026-05-24T13:40:28.570Z; +5h = 18:40:28.570Z.
	now, _ := time.Parse(time.RFC3339Nano, "2026-05-24T18:40:28.570Z")
	got := Collect(Options{Root: "testdata/codexroot", Now: now})
	if got.Stale {
		t.Error("Stale = true at exactly 5h, want false (threshold is strictly >5h)")
	}
}

func TestCollectNoSessions(t *testing.T) {
	got := Collect(Options{Root: "testdata/emptyroot"})
	if got.Available {
		t.Errorf("Available = true, want false: %+v", got)
	}
	if got.Error != nil {
		t.Errorf("Error = %+v, want nil (missing data source is not an error)", got.Error)
	}
}

func TestCollectUsesCodexHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)

	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-c494201693fa.jsonl"
	writeRollout(t, day, name,
		ctxLine("2026-05-24T10:00:00.000Z", "gpt-env", "/x")+"\n"+tcLine("2026-05-24T10:05:00.000Z", 150), time.Unix(1000, 0))

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:30:00Z")
	got := Collect(Options{Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-env" {
		t.Fatalf("Model = %+v, want CODEX_HOME session", got.Model)
	}
}

// TestCollectRealHome runs against the real ~/.codex. Opt-in:
//
//	TACHO_E2E=1 go test ./internal/collector/codex -run RealHome -v
func TestCollectRealHome(t *testing.T) {
	if os.Getenv("TACHO_E2E") == "" {
		t.Skip("set TACHO_E2E=1 to run against the real ~/.codex")
	}
	got := Collect(Options{})
	b, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("real ~/.codex result:\n%s", b)
	if got.Error != nil {
		t.Errorf("Error = %+v", got.Error)
	}
}

// pickSession must backtrack past an empty newest day/month to the most recent
// day that actually holds a usable token_count (e.g. around date rollover).
func TestPickSessionBacktracksEmptyBranches(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	dataDay := filepath.Join(sessions, "2026", "05", "24")
	if err := os.MkdirAll(dataDay, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-c494201693fa.jsonl"
	writeRollout(t, dataDay, name,
		ctxLine("2026-05-24T10:00:00.000Z", "gpt-x", "/x")+"\n"+tcLine("2026-05-24T10:05:00.000Z", 150), time.Unix(1000, 0))
	// Newer-but-empty branches that must be skipped: a later day, a later
	// month, and a later year, all without any .jsonl.
	for _, empty := range []string{
		filepath.Join(sessions, "2026", "05", "25"),
		filepath.Join(sessions, "2026", "06"),
		filepath.Join(sessions, "2027"),
	} {
		if err := os.MkdirAll(empty, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if res := pickSession(sessions); res.tc == nil || res.path != filepath.Join(dataDay, name) {
		t.Errorf("pickSession path = %q (tc set=%v), want %q", res.path, res.tc != nil, filepath.Join(dataDay, name))
	}
}

// At a day boundary, a brand-new day directory may hold only a fresh
// turn_context (no token_count yet) while the prior day holds the latest usage.
// Collect must backtrack ACROSS the day to the prior day's token_count, not
// return no_token_count for the new day.
func TestCollectBacktracksDayWithoutTokenCount(t *testing.T) {
	root := t.TempDir()
	prevDay := filepath.Join(root, "sessions", "2026", "05", "24")
	newDay := filepath.Join(root, "sessions", "2026", "05", "25")
	for _, d := range []string{prevDay, newDay} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Prior day: a session with a token_count (model gpt-prev).
	writeRollout(t, prevDay, "rollout-2026-05-24T23-50-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine("2026-05-24T23:50:00.000Z", "gpt-prev", "/a")+"\n"+tcLine("2026-05-24T23:59:00.000Z", 150), time.Unix(1000, 0))
	// New day: only a fresh turn_context, no token_count yet, newer mtime.
	writeRollout(t, newDay, "rollout-2026-05-25T00-01-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine("2026-05-25T00:01:00.000Z", "gpt-new", "/b"), time.Unix(2000, 0))

	now, _ := time.Parse(time.RFC3339, "2026-05-25T00:30:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v, want the prior day's session (new day has no token_count)", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-prev" {
		t.Errorf("Model = %+v, want gpt-prev (must backtrack across the day boundary)", got.Model)
	}
}

// writeRollout writes a rollout file and stamps its mtime so selection tests
// can control file recency independently of the events inside.
func writeRollout(t *testing.T, day, name, content string, mod time.Time) {
	t.Helper()
	p := filepath.Join(day, name)
	if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// ctxLine and tcLine build rollout events for selection tests.
func ctxLine(ts, model, cwd string) string {
	return `{"timestamp":"` + ts + `","type":"turn_context","payload":{"cwd":"` + cwd + `","model":"` + model + `"}}`
}
func tcLine(ts string, total int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d},"model_context_window":200000}}}`,
		ts, total, total)
}

// Collect must pick the session whose last token_count is the most recent, not
// the newest file by mtime — a later turn in an older-mtime file still wins.
// (mtimes stay realistic — a file is at least as new as its last event — since
// that invariant is what lets the walk prune older files.)
func TestCollectPicksLatestTokenCountNotMtime(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	// Session A: last token_count at 10:05 (model gpt-a), file written then.
	writeRollout(t, day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine("2026-05-24T10:00:00.000Z", "gpt-a", "/a")+"\n"+tcLine("2026-05-24T10:05:00.000Z", 150),
		time.Date(2026, 5, 24, 10, 5, 30, 0, time.UTC))
	// Session B: last token_count at 10:02 (older), but NEWER file mtime
	// (e.g. still streaming events that carry no token_count).
	writeRollout(t, day, "rollout-2026-05-24T10-01-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine("2026-05-24T10:01:00.000Z", "gpt-b", "/b")+"\n"+tcLine("2026-05-24T10:02:00.000Z", 99),
		time.Date(2026, 5, 24, 10, 7, 0, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-a" {
		t.Errorf("Model = %+v, want gpt-a (freshest token_count wins over newer mtime)", got.Model)
	}
}

// Regression for #188: a session that has been running for days lives in a
// day directory far behind several newer rollout-holding days. A day-count
// cutoff (the old extraDaysCompared = 2) stopped before reaching it; mtime
// ordering must find it regardless of how many days sit in between.
func TestCollectFindsLongRunningSessionPastNewerDays(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")

	// Long-running session: started on 05-19, freshest token_count today
	// (05-24 10:05) — its file mtime moved forward with the appends.
	oldDay := filepath.Join(sessions, "2026", "05", "19")
	if err := os.MkdirAll(oldDay, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, oldDay, "rollout-2026-05-19T09-00-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine("2026-05-19T09:00:00.000Z", "gpt-long", "/long")+"\n"+tcLine("2026-05-24T10:05:00.000Z", 999),
		time.Date(2026, 5, 24, 10, 5, 0, 0, time.UTC))

	// Three newer days each hold a finished session, so a day-count cutoff
	// exhausts its budget before reaching 05-19.
	for i, d := range []string{"21", "22", "23"} {
		day := filepath.Join(sessions, "2026", "05", d)
		if err := os.MkdirAll(day, 0o755); err != nil {
			t.Fatal(err)
		}
		ts := fmt.Sprintf("2026-05-%sT12:00:00.000Z", d)
		writeRollout(t, day, fmt.Sprintf("rollout-2026-05-%sT12-00-00-019e5933-2289-7e72-88fd-%012d.jsonl", d, i),
			ctxLine(ts, "gpt-"+d, "/x")+"\n"+tcLine(ts, 100),
			time.Date(2026, 5, 21+i, 12, 0, 0, 0, time.UTC))
	}

	// Today: a one-off run whose token_count is older than the long-running
	// session's latest.
	today := filepath.Join(sessions, "2026", "05", "24")
	if err := os.MkdirAll(today, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, today, "rollout-2026-05-24T09-00-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine("2026-05-24T09:00:00.000Z", "gpt-today", "/t")+"\n"+tcLine("2026-05-24T09:01:00.000Z", 50),
		time.Date(2026, 5, 24, 9, 1, 0, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-long" {
		t.Errorf("Model = %+v, want gpt-long (freshest token_count sits 5 day-dirs back)", got.Model)
	}
	if got.Session == nil || got.Session.Tokens == nil || got.Session.Tokens.Total != 999 {
		t.Errorf("Session.Tokens = %+v, want the long-running session's 999", got.Session)
	}
}

// Equal mtimes must not prune each other: the tie-broken visit order (path
// descending) reads gpt-b first, and gpt-a — same mtime, fresher
// token_count — must still be read and win, not be skipped. This pins the
// strict Before() cutoff: only files strictly older than the freshest
// token_count are skipped.
func TestCollectEqualMtimeReadsBoth(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := time.Date(2026, 5, 24, 10, 10, 0, 0, time.UTC)
	writeRollout(t, day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine("2026-05-24T10:00:00.000Z", "gpt-a", "/a")+"\n"+tcLine("2026-05-24T10:05:00.000Z", 150), mod)
	writeRollout(t, day, "rollout-2026-05-24T10-01-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine("2026-05-24T10:01:00.000Z", "gpt-b", "/b")+"\n"+tcLine("2026-05-24T10:02:00.000Z", 99), mod)

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:11:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-a" {
		t.Errorf("Model = %+v, want gpt-a (equal mtime must not be pruned)", got.Model)
	}
}

// With identical mtimes AND identical token_count timestamps the pick is
// deterministic: the path tie-break (descending) visits gpt-b first, and a
// merely-equal timestamp does not displace it. (A strictly fresher
// token_count at the exact mtime boundary is impossible by the invariant —
// events are never newer than their file — so equality is the boundary's
// only case.)
func TestCollectEqualMtimeAndTimestampTieBreak(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := time.Date(2026, 5, 24, 10, 5, 0, 0, time.UTC)
	ts := "2026-05-24T10:05:00.000Z"
	writeRollout(t, day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine(ts, "gpt-a", "/a")+"\n"+tcLine(ts, 150), mod)
	writeRollout(t, day, "rollout-2026-05-24T10-01-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine(ts, "gpt-b", "/b")+"\n"+tcLine(ts, 99), mod)

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-b" {
		t.Errorf("Model = %+v, want gpt-b (deterministic path tie-break)", got.Model)
	}
}

// A rollout that is listed but cannot be read must surface as read_error,
// not read as an absent tool: "exists but unreadable" is unknown, not
// missing. A dangling symlink reproduces the failure portably.
func TestCollectReadErrorWhenOnlyRolloutUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone"),
		filepath.Join(day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-cccccccccccc.jsonl")); err != nil {
		t.Fatal(err)
	}

	got := Collect(Options{Root: root})
	if got.Error == nil || got.Error.Code != "read_error" {
		t.Fatalf("Error = %+v, want read_error for a listed-but-unreadable rollout", got.Error)
	}
}

// A just-started session (newest mtime, only turn_context, no token_count) must
// not be selected and hide an older session's still-valid data.
func TestCollectSkipsSessionWithoutTokenCount(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	// Session A: complete with token_count, older mtime.
	writeRollout(t, day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine("2026-05-24T10:00:00.000Z", "gpt-a", "/a")+"\n"+tcLine("2026-05-24T10:05:00.000Z", 150), time.Unix(1000, 0))
	// Session B: just started, only turn_context, NEWEST mtime.
	writeRollout(t, day, "rollout-2026-05-24T10-10-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine("2026-05-24T10:10:00.000Z", "gpt-b", "/b"), time.Unix(2000, 0))

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v, want session A (B has no token_count)", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-a" {
		t.Errorf("Model = %+v, want gpt-a (B has no token_count, must be skipped)", got.Model)
	}
}

// When a single turn is longer than the tail window, turn_context (emitted at
// turn start) falls outside the tail while token_count (turn end) stays in it.
// Collect must full-scan so model/cwd don't silently go null.
func TestCollectTurnContextBeyondTail(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	b = append(b, []byte(`{"timestamp":"2026-05-24T10:00:00.000Z","type":"turn_context","payload":{"cwd":"/tmp/work","model":"gpt-5.4-codex"}}`+"\n")...)
	// Filler well beyond tailBytes (512KB) between turn_context and token_count.
	filler := `{"timestamp":"2026-05-24T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + string(bytes.Repeat([]byte("x"), 1000)) + `"}]}}` + "\n"
	for total := 0; total < tailBytes+50*1024; total += len(filler) {
		b = append(b, filler...)
	}
	b = append(b, []byte(`{"timestamp":"2026-05-24T10:05:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":50,"total_tokens":150},"model_context_window":200000}}}`+"\n")...)
	file := filepath.Join(day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-c494201693fa.jsonl")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-5.4-codex" {
		t.Errorf("Model = %+v, want gpt-5.4-codex (turn_context past the tail must be recovered by full scan)", got.Model)
	}
	if got.Session == nil || got.Session.CWD == nil || *got.Session.CWD != "/tmp/work" {
		t.Errorf("CWD = %v, want /tmp/work", got.Session.CWD)
	}
}

// toLimit must keep an absent/zero resets_at as null, not 1970-01-01.
func TestToLimitNullResetsAt(t *testing.T) {
	l := toLimit(&rlWindow{WindowMinutes: 300, UsedPercent: 5, ResetsAt: 0})
	if l.ResetsAt != nil {
		t.Errorf("ResetsAt = %v, want nil for zero epoch", *l.ResetsAt)
	}
	l = toLimit(&rlWindow{WindowMinutes: 300, UsedPercent: 5, ResetsAt: 1779646858})
	if l.ResetsAt == nil {
		t.Error("ResetsAt = nil, want set for a real epoch")
	}
}

func TestWindowName(t *testing.T) {
	cases := map[int]string{300: "5h", 10080: "weekly", 60: "1h", 90: "90m"}
	for mins, want := range cases {
		if got := windowName(mins); got != want {
			t.Errorf("windowName(%d) = %q, want %q", mins, got, want)
		}
	}
}
