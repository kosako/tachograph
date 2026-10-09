package notify

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

func limitsTool(name string, stale bool, used5, usedW float64, resets5 string) schema.Tool {
	t := schema.Tool{Tool: name, Available: true, Stale: stale, Limits: []schema.Limit{
		{Window: schema.WindowFiveHour, UsedPct: &used5},
		{Window: schema.WindowWeekly, UsedPct: &usedW},
	}}
	if resets5 != "" {
		t.Limits[0].ResetsAt = &resets5
	}
	return t
}

func status(tools ...schema.Tool) schema.Status { return schema.Status{Tools: tools} }

// testNow is the evaluation time; windows without observed_at have no age,
// so it only matters to the tests that set one.
var testNow = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

// testRoots are the config roots the tests read each tool from.
var testRoots = map[string]string{
	schema.ToolClaudeCode: filepath.Join(string(filepath.Separator), "profiles", "claude"),
	schema.ToolCodex:      filepath.Join(string(filepath.Separator), "profiles", "codex"),
}

// keyOf is the record key of tool's window read from its testRoots root.
func keyOf(tool, window string) string {
	root, _ := cache.NormalizeRoot(testRoots[tool])
	return Key(tool, window, root)
}

// observedAgo stamps every window of tool as observed ago before testNow.
func observedAgo(tool schema.Tool, ago time.Duration) schema.Tool {
	observed := testNow.Add(-ago).Format(time.RFC3339)
	for i := range tool.Limits {
		tool.Limits[i].ObservedAt = &observed
	}
	return tool
}

// A window fires once per threshold per cycle: crossing 50% announces once,
// staying below it stays quiet, crossing 30% announces again, rising back
// above 30% re-arms it, and a new reset time re-arms everything.
func TestEvaluateFiresOncePerThresholdPerCycle(t *testing.T) {
	th := []int{50, 30, 10}
	r1 := "2026-09-20T03:00:00+09:00"

	ev, st := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 55, 10, r1)), th, State{}, testRoots, testNow)
	if len(ev) != 1 || ev[0].Key != keyOf(schema.ToolClaudeCode, schema.WindowFiveHour) || ev[0].Threshold != 50 || ev[0].Remaining != 45 {
		t.Fatalf("first crossing: events = %+v", ev)
	}
	if ev, _ = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 58, 10, r1)), th, st, testRoots, testNow); len(ev) != 0 {
		t.Errorf("still below 50%%: events = %+v, want none", ev)
	}
	ev, st = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 72, 10, r1)), th, st, testRoots, testNow)
	if len(ev) != 1 || ev[0].Threshold != 30 {
		t.Fatalf("crossing 30%%: events = %+v", ev)
	}
	// Back above 30% (but below 50%): 30 re-arms, 50 stays announced.
	_, st = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 60, 10, r1)), th, st, testRoots, testNow)
	ev, st = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 75, 10, r1)), th, st, testRoots, testNow)
	if len(ev) != 1 || ev[0].Threshold != 30 {
		t.Fatalf("re-crossing 30%% after re-arm: events = %+v", ev)
	}
	// New cycle: the reset time changed, so 50 fires again.
	ev, _ = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 55, 10, "2026-09-20T08:00:00+09:00")), th, st, testRoots, testNow)
	if len(ev) != 1 || ev[0].Threshold != 50 {
		t.Errorf("new cycle: events = %+v, want 50 again", ev)
	}
}

// Dropping past several thresholds at once announces the deepest one only,
// and marks all of them so none fires later in the same cycle.
func TestEvaluateCollapsesMultipleCrossings(t *testing.T) {
	th := []int{50, 30, 10}
	ev, st := Evaluate(status(limitsTool(schema.ToolCodex, false, 95, 0, "")), th, State{}, testRoots, testNow)
	if len(ev) != 1 || ev[0].Threshold != 10 || ev[0].Remaining != 5 {
		t.Fatalf("events = %+v, want one event at 10", ev)
	}
	if got := st[keyOf(schema.ToolCodex, schema.WindowFiveHour)].Notified; len(got) != 3 {
		t.Errorf("Notified = %v, want all three thresholds", got)
	}
	if ev, _ = Evaluate(status(limitsTool(schema.ToolCodex, false, 96, 0, "")), th, st, testRoots, testNow); len(ev) != 0 {
		t.Errorf("events = %+v after collapse, want none", ev)
	}
}

// Stale, unavailable, and errored tools are skipped and keep their record;
// no thresholds means nothing fires; the input state is never mutated.
func TestEvaluateSkipsAndPreservesState(t *testing.T) {
	th := []int{50}
	_, st := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 60, 0, "")), th, State{}, testRoots, testNow)
	before := append([]int(nil), st[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)].Notified...)

	ev, next := Evaluate(status(limitsTool(schema.ToolClaudeCode, true, 99, 99, "")), th, st, testRoots, testNow)
	if len(ev) != 0 || len(next[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)].Notified) != 1 {
		t.Errorf("stale tool: events = %+v, state = %+v, want none and the record kept", ev, next)
	}
	errTool := schema.Unavailable(schema.ToolCodex)
	errTool.Available = true
	errTool.Error = &schema.Error{Code: "x"}
	if ev, _ := Evaluate(status(schema.Unavailable(schema.ToolClaudeCode), errTool), th, State{}, testRoots, testNow); len(ev) != 0 {
		t.Errorf("unavailable / errored tools: events = %+v, want none", ev)
	}
	if ev, _ := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 99, 99, "")), nil, State{}, testRoots, testNow); len(ev) != 0 {
		t.Errorf("no thresholds: events = %+v, want none", ev)
	}
	// Only the 5h and weekly windows are watched: a collector-reported odd
	// window size (e.g. Codex with an unexpected window_minutes) is ignored.
	used := 99.0
	odd := schema.Tool{Tool: schema.ToolCodex, Available: true, Limits: []schema.Limit{{Window: "6h", UsedPct: &used}}}
	if ev, st := Evaluate(status(odd), th, State{}, testRoots, testNow); len(ev) != 0 || len(st) != 0 {
		t.Errorf("odd window: events = %+v, state = %+v, want none", ev, st)
	}
	// Re-arm through the same state must not have touched the caller's copy.
	Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 10, 0, "")), th, st, testRoots, testNow)
	if got := st[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)].Notified; len(got) != len(before) || got[0] != before[0] {
		t.Errorf("input State mutated: %v, want %v", got, before)
	}
}

// A window observed longer ago than its tool's stale threshold — the reading
// SwiftBar marks ⚠ (#331) — neither fires nor touches its record, even on a
// tool that is itself fresh; the same reading observed recently fires
// (#338). The threshold is the tool's: 60 minutes for Claude, 5 hours for
// Codex.
func TestEvaluateSkipsOldObservations(t *testing.T) {
	th := []int{50}
	for _, c := range []struct {
		name  string
		tool  string
		ago   time.Duration
		fires bool
	}{
		{"Claude, 10 minutes old", schema.ToolClaudeCode, 10 * time.Minute, true},
		{"Claude, 2 hours old", schema.ToolClaudeCode, 2 * time.Hour, false},
		{"Codex, 2 hours old", schema.ToolCodex, 2 * time.Hour, true},
		{"Codex, 6 hours old", schema.ToolCodex, 6 * time.Hour, false},
	} {
		ev, st := Evaluate(status(observedAgo(limitsTool(c.tool, false, 80, 80, ""), c.ago)), th, State{}, testRoots, testNow)
		if c.fires && len(ev) != 2 {
			t.Errorf("%s: events = %+v, want both windows", c.name, ev)
		}
		if !c.fires && (len(ev) != 0 || len(st) != 0) {
			t.Errorf("%s: events = %+v, state = %+v, want none and no record", c.name, ev, st)
		}
	}

	// The windows of one tool are judged one by one: next to a fresh window,
	// an old one stays silent and keeps its record (which would otherwise
	// start a new cycle, its reset time differing), and the fresh one fires.
	mixed := func(ago5h, agoWk time.Duration) schema.Tool {
		tool := limitsTool(schema.ToolClaudeCode, false, 80, 80, "")
		o5, ow := testNow.Add(-ago5h).Format(time.RFC3339), testNow.Add(-agoWk).Format(time.RFC3339)
		tool.Limits[0].ObservedAt, tool.Limits[1].ObservedAt = &o5, &ow
		return tool
	}
	kept := Window{ResetsAt: "2026-09-19T12:00:00Z"}
	for _, c := range []struct {
		name       string
		tool       schema.Tool
		fires, old string
	}{
		{"old 5h, fresh weekly", mixed(2*time.Hour, 10*time.Minute), keyOf(schema.ToolClaudeCode, schema.WindowWeekly), keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)},
		{"fresh 5h, old weekly", mixed(10*time.Minute, 2*time.Hour), keyOf(schema.ToolClaudeCode, schema.WindowFiveHour), keyOf(schema.ToolClaudeCode, schema.WindowWeekly)},
	} {
		ev, next := Evaluate(status(c.tool), th, State{c.old: kept}, testRoots, testNow)
		if len(ev) != 1 || ev[0].Key != c.fires {
			t.Errorf("%s: events = %+v, want %s only", c.name, ev, c.fires)
		}
		if got := next[c.old]; got.ResetsAt != kept.ResetsAt || len(got.Notified) != 0 {
			t.Errorf("%s: record of the old window = %+v, want %+v kept", c.name, got, kept)
		}
	}

	// An old reading keeps the record as it was: its headroom back above
	// 50% doesn't re-arm the threshold, nor does its reset time start a new
	// cycle.
	_, st := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 60, 0, "2026-09-19T12:00:00Z")), th, State{}, testRoots, testNow)
	old := observedAgo(limitsTool(schema.ToolClaudeCode, false, 10, 0, "2026-09-19T17:00:00Z"), 2*time.Hour)
	_, next := Evaluate(status(old), th, st, testRoots, testNow)
	if got := next[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)]; got.ResetsAt != "2026-09-19T12:00:00Z" || len(got.Notified) != 1 || got.Notified[0] != 50 {
		t.Errorf("record after an old reading = %+v, want the 12:00 cycle with 50 still announced", got)
	}
}

// A window whose reset time is not after now belongs to a cycle that is
// over: its headroom says nothing about the current one, so it neither fires
// nor touches its record, even when the reading is fresh; the same reading
// with its reset ahead fires (#363). A reset time that can't be read, or none,
// leaves the window judged as before.
func TestEvaluateSkipsPassedResets(t *testing.T) {
	th := []int{50}
	at := func(d time.Duration) string { return testNow.Add(d).Format(time.RFC3339) }
	for _, c := range []struct {
		name, resets string
		fires        bool
	}{
		{"reset a minute ago", at(-time.Minute), false},
		{"reset now", at(0), false},
		{"reset a second ahead", at(time.Second), true},
		{"reset unreadable", "soon", true},
		{"no reset time", "", true},
	} {
		for _, name := range []string{schema.ToolClaudeCode, schema.ToolCodex} {
			key := keyOf(name, schema.WindowFiveHour)
			tool := observedAgo(limitsTool(name, false, 95, 0, c.resets), 10*time.Minute)
			ev, st := Evaluate(status(tool), th, State{}, testRoots, testNow)
			_, recorded := st[key]
			if c.fires && (len(ev) != 1 || ev[0].Key != key || !recorded) {
				t.Errorf("%s, %s: events = %+v, state = %+v, want the 5h window to fire", name, c.name, ev, st)
			}
			if !c.fires && (len(ev) != 0 || recorded) {
				t.Errorf("%s, %s: events = %+v, state = %+v, want none and no 5h record", name, c.name, ev, st)
			}
		}
	}

	// The windows of one tool are judged one by one: next to a running
	// window, a passed one stays silent and keeps its record (which would
	// otherwise start a new cycle, its reset time differing), and the running
	// one fires.
	mixed := func(resets5h, resetsWk string) schema.Tool {
		tool := observedAgo(limitsTool(schema.ToolClaudeCode, false, 80, 80, resets5h), 10*time.Minute)
		tool.Limits[1].ResetsAt = &resetsWk
		return tool
	}
	kept := Window{ResetsAt: at(-6 * time.Hour)}
	for _, c := range []struct {
		name         string
		tool         schema.Tool
		fires, ended string
	}{
		{"passed 5h, running weekly", mixed(at(-time.Minute), at(24*time.Hour)), keyOf(schema.ToolClaudeCode, schema.WindowWeekly), keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)},
		{"running 5h, passed weekly", mixed(at(time.Hour), at(-time.Minute)), keyOf(schema.ToolClaudeCode, schema.WindowFiveHour), keyOf(schema.ToolClaudeCode, schema.WindowWeekly)},
	} {
		ev, next := Evaluate(status(c.tool), th, State{c.ended: kept}, testRoots, testNow)
		if len(ev) != 1 || ev[0].Key != c.fires {
			t.Errorf("%s: events = %+v, want %s only", c.name, ev, c.fires)
		}
		if got := next[c.ended]; got.ResetsAt != kept.ResetsAt || len(got.Notified) != 0 {
			t.Errorf("%s: record of the passed window = %+v, want %+v kept", c.name, got, kept)
		}
	}

	// A passed window keeps the record as it was: its headroom back above
	// 50% doesn't re-arm the threshold announced in that cycle.
	r := at(-time.Minute)
	st := State{keyOf(schema.ToolClaudeCode, schema.WindowFiveHour): {ResetsAt: r, Notified: []int{50}}}
	passed := observedAgo(limitsTool(schema.ToolClaudeCode, false, 10, 0, r), 10*time.Minute)
	if _, next := Evaluate(status(passed), th, st, testRoots, testNow); len(next[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)].Notified) != 1 {
		t.Errorf("record after a passed window = %+v, want 50 still announced", next[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)])
	}
}

func TestBodyAndURL(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-09-19T10:00:00+09:00")
	resets := "2026-09-19T14:30:00+09:00"
	ev := Event{Tool: schema.ToolClaudeCode, Window: schema.WindowWeekly, Remaining: 28.4, Threshold: 30, ResetsAt: &resets}
	// The reset time renders in the runner's local zone (render.ResetShort);
	// build the expectation the same way so this holds on any CI timezone.
	if got, want := Body(ev, now), "Claude weekly: 28% left · resets "+render.ResetShort(resets, now); got != want {
		t.Errorf("Body = %q, want %q", got, want)
	}
	ev.ResetsAt = nil
	ev.Tool = schema.ToolCodex
	if got := Body(ev, now); got != "Codex weekly: 28% left" {
		t.Errorf("Body without reset = %q", got)
	}
	u := URL(ev, "/Users/me/.config/swiftbar/plugins/tacho.30s.sh", now)
	if !strings.HasPrefix(u, "swiftbar://notify?") || !strings.Contains(u, "plugin=%2FUsers%2Fme%2F.config%2Fswiftbar%2Fplugins%2Ftacho.30s.sh") ||
		!strings.Contains(u, "body=Codex%20weekly%3A%2028%25%20left") || strings.Contains(u, "+") {
		t.Errorf("URL = %q", u)
	}
}

// Run delivers each event and keeps a failed delivery's window un-announced
// so it is retried next tick.
func TestRunRetriesFailedDelivery(t *testing.T) {
	th := []int{50}
	s := status(limitsTool(schema.ToolClaudeCode, false, 60, 0, ""), limitsTool(schema.ToolCodex, false, 70, 0, ""))
	var sent []string
	fail := func(u string) error {
		sent = append(sent, u)
		if strings.Contains(u, "Codex") {
			return errors.New("open failed")
		}
		return nil
	}
	st := Run(s, th, State{}, testRoots, "tacho.30s.sh", time.Now(), fail)
	if len(sent) != 2 {
		t.Fatalf("sent %d notifications, want 2", len(sent))
	}
	if _, ok := st[keyOf(schema.ToolCodex, schema.WindowFiveHour)]; ok {
		t.Errorf("failed delivery recorded: %+v", st[keyOf(schema.ToolCodex, schema.WindowFiveHour)])
	}
	if len(st[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)].Notified) != 1 {
		t.Errorf("successful delivery not recorded: %+v", st)
	}
	sent = nil
	Run(s, th, st, testRoots, "tacho.30s.sh", time.Now(), fail)
	if len(sent) != 1 || !strings.Contains(sent[0], "Codex") {
		t.Errorf("retry sent %v, want only the Codex window again", sent)
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	if got := LoadState(); len(got) != 0 {
		t.Fatalf("LoadState on empty cache = %v", got)
	}
	st := State{keyOf(schema.ToolClaudeCode, schema.WindowFiveHour): {ResetsAt: "r", Notified: []int{50, 30}}}
	if err := SaveState(st); err != nil {
		t.Fatal(err)
	}
	got := LoadState()
	if w := got[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)]; w.ResetsAt != "r" || len(w.Notified) != 2 {
		t.Errorf("LoadState = %+v", got)
	}
}

// Each config root keeps its own record (#364): plugins for two profiles
// sharing the cache dir, evaluated in turn, announce each profile's crossing
// once — before, a different reset time restarted the other's cycle on every
// tick, and an equal one let the first profile's record silence the second's
// first notification. A tool's record goes by its own root, so a profile
// that differs only in Claude's root leaves Codex's record shared.
func TestEvaluateKeepsRecordsPerRoot(t *testing.T) {
	th := []int{50}
	profile := func(claudeRoot string) map[string]string {
		return map[string]string{schema.ToolClaudeCode: claudeRoot, schema.ToolCodex: testRoots[schema.ToolCodex]}
	}
	a := profile(filepath.Join(string(filepath.Separator), "profiles", "a"))
	b := profile(filepath.Join(string(filepath.Separator), "profiles", "b"))
	for _, c := range []struct {
		name           string
		resetA, resetB string
	}{
		{"different reset times", "2026-09-19T12:00:00Z", "2026-09-19T14:00:00Z"},
		{"the same reset time", "2026-09-19T12:00:00Z", "2026-09-19T12:00:00Z"},
	} {
		st := State{}
		fired := map[string]int{}
		for i := 0; i < 4; i++ {
			roots, resets := a, c.resetA
			if i%2 == 1 {
				roots, resets = b, c.resetB
			}
			s := status(limitsTool(schema.ToolClaudeCode, false, 80, 0, resets), limitsTool(schema.ToolCodex, false, 80, 0, ""))
			var ev []Event
			ev, st = Evaluate(s, th, st, roots, testNow)
			for _, e := range ev {
				fired[e.Key]++
			}
		}
		rootA, _ := cache.NormalizeRoot(a[schema.ToolClaudeCode])
		rootB, _ := cache.NormalizeRoot(b[schema.ToolClaudeCode])
		want := map[string]int{
			Key(schema.ToolClaudeCode, schema.WindowFiveHour, rootA): 1,
			Key(schema.ToolClaudeCode, schema.WindowFiveHour, rootB): 1,
			keyOf(schema.ToolCodex, schema.WindowFiveHour):           1,
		}
		if len(fired) != len(want) {
			t.Errorf("%s: fired %v, want %v", c.name, fired, want)
			continue
		}
		for k, n := range want {
			if fired[k] != n {
				t.Errorf("%s: fired %v, want %v", c.name, fired, want)
				break
			}
		}
	}
}

// A tool whose config root doesn't normalize — none given, empty, or a
// relative one with the current directory gone — neither fires nor gets a
// record: it couldn't be told from another profile's (#364, as #321).
func TestEvaluateSkipsUnresolvableRoot(t *testing.T) {
	th := []int{50}
	s := status(limitsTool(schema.ToolClaudeCode, false, 80, 0, ""), limitsTool(schema.ToolCodex, false, 80, 0, ""))
	only := func(claudeRoot string, set bool) map[string]string {
		roots := map[string]string{schema.ToolCodex: testRoots[schema.ToolCodex]}
		if set {
			roots[schema.ToolClaudeCode] = claudeRoot
		}
		return roots
	}
	check := func(name string, roots map[string]string) {
		t.Helper()
		ev, st := Evaluate(s, th, State{}, roots, testNow)
		if len(ev) != 1 || ev[0].Tool != schema.ToolCodex || len(st) != 2 {
			t.Errorf("%s: events = %+v, state = %+v, want Codex alone to fire and be recorded", name, ev, st)
		}
		for k := range st {
			if strings.HasPrefix(k, schema.ToolClaudeCode+"/") {
				t.Errorf("%s: recorded %q for Claude", name, k)
			}
		}
	}
	check("no root", only("", false))
	check("empty root", only("", true))

	t.Run("relative root, current directory gone", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the current directory can't be removed on Windows")
		}
		gone := filepath.Join(t.TempDir(), "gone")
		if err := os.Mkdir(gone, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(gone)
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		if _, err := filepath.Abs("profiles/rel"); err == nil {
			t.Skip("filepath.Abs still resolves a relative path from a removed directory here")
		}
		ev, st := Evaluate(s, th, State{}, only("profiles/rel", true), testNow)
		if len(ev) != 1 || ev[0].Tool != schema.ToolCodex || len(st) != 2 {
			t.Errorf("events = %+v, state = %+v, want Codex alone to fire and be recorded", ev, st)
		}
	})
}

// The record is versioned: one in another format — the root-less one before
// #364 included — is discarded, as if the file had been deleted (at most one
// repeated notification), rather than read as some profile's.
func TestLoadStateDiscardsOtherFormats(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", dir)
	for _, c := range []struct{ name, body string }{
		{"root-less record", `{"claude-code/5h":{"resets_at":"r","notified":[50]}}`},
		{"other version", `{"version":1,"windows":{"claude-code/5h@/p":{"resets_at":"r","notified":[50]}}}`},
		{"no windows", `{"version":2}`},
	} {
		if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadState(); len(got) != 0 {
			t.Errorf("%s: LoadState = %+v, want empty", c.name, got)
		}
	}
}

// Notify holds the record's lock from the read to the write (#364). When
// another pass still holds it, the tick neither sends nor saves; without file
// locks on the system it goes on unlocked; with the lock it sends, saves,
// and only then unlocks.
func TestNotifyHoldsTheLock(t *testing.T) {
	th := []int{50}
	s := status(limitsTool(schema.ToolClaudeCode, false, 80, 0, ""))
	for _, c := range []struct {
		name       string
		lockErr    error
		wantSent   bool
		wantSaved  bool
		wantUnlock bool
	}{
		{"held by another pass", errors.New("resource temporarily unavailable"), false, false, false},
		{"no file locks here", errors.ErrUnsupported, true, true, false},
		{"lock taken", nil, true, true, true},
	} {
		dir := t.TempDir()
		t.Setenv("TACHO_CACHE_DIR", dir)
		var order []string
		lock := func() (func(), error) {
			if c.lockErr != nil {
				return nil, c.lockErr
			}
			return func() { order = append(order, "unlock") }, nil
		}
		send := func(string) error { order = append(order, "send"); return nil }
		Notify(s, th, testRoots, "tacho.30s.sh", testNow, lock, send)

		_, err := os.Stat(filepath.Join(dir, stateFile))
		saved := err == nil
		sent := len(order) > 0 && order[0] == "send"
		unlocked := len(order) > 0 && order[len(order)-1] == "unlock"
		if sent != c.wantSent || saved != c.wantSaved || unlocked != c.wantUnlock {
			t.Errorf("%s: sent %v, saved %v, unlocked last %v (calls %v), want %v, %v, %v",
				c.name, sent, saved, unlocked, order, c.wantSent, c.wantSaved, c.wantUnlock)
		}
		if saved {
			if got := LoadState(); len(got[keyOf(schema.ToolClaudeCode, schema.WindowFiveHour)].Notified) != 1 {
				t.Errorf("%s: saved record = %+v, want 50 announced", c.name, got)
			}
		}
	}
}
