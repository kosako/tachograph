package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// crossMidnightRoot builds the #133 scenario: a main session that started
// yesterday and kept running past midnight (its rollout stays in YESTERDAY's
// directory), plus a one-off `codex exec` after midnight in TODAY's directory.
// mainLastTC / execTC are the sessions' latest token_count timestamps; each
// file's mtime follows its own last event, as a live rollout's would.
func crossMidnightRoot(t *testing.T, mainLastTC, execTC string) string {
	t.Helper()
	root := t.TempDir()
	prevDay := filepath.Join(root, "sessions", "2026", "07", "03")
	newDay := filepath.Join(root, "sessions", "2026", "07", "04")
	for _, d := range []string{prevDay, newDay} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRollout(t, prevDay, "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-aaaaaaaaaaaa.jsonl",
		ctxLine("2026-07-03T22:00:00.000Z", "gpt-main", "/main")+"\n"+
			tcLine("2026-07-03T23:50:00.000Z", 100000)+"\n"+
			tcLine(mainLastTC, 150000),
		mtimeFor(t, mainLastTC))
	writeRollout(t, newDay, "rollout-2026-07-04T00-10-00-019e5933-2289-7e72-88fd-bbbbbbbbbbbb.jsonl",
		ctxLine("2026-07-04T00:10:00.000Z", "gpt-exec", "/exec")+"\n"+
			tcLine(execTC, 500),
		mtimeFor(t, execTC))
	return root
}

// mtimeFor turns an event timestamp into the mtime a live rollout would
// carry: appends move mtime forward, so a file is at least as new as its
// last event.
func mtimeFor(t *testing.T, ts string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Add(time.Second)
}

// Regression for issue #133 (1): the cross-midnight main session's fresher
// token_count (01:30, in yesterday's directory) must outrank the older one-off
// exec run (00:10) in today's directory.
func TestCollectCrossMidnightFresherTokenCountWins(t *testing.T) {
	root := crossMidnightRoot(t, "2026-07-04T01:30:00.000Z", "2026-07-04T00:10:30.000Z")
	now, _ := time.Parse(time.RFC3339, "2026-07-04T02:00:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Session == nil || got.Session.Tokens == nil || got.Session.Tokens.Total != 150000 {
		t.Errorf("Session.Tokens = %+v, want the main session's fresher total 150000 (01:30 > 00:10)", got.Session.Tokens)
	}
	if got.Model == nil || got.Model.ID != "gpt-main" {
		t.Errorf("Model = %+v, want gpt-main (freshest token_count overall)", got.Model)
	}
}

// The flip side: when today's run is genuinely the freshest (02:30 > 01:30),
// it must win — cross-day comparison must not overshoot into always preferring
// the older directory.
func TestCollectCrossMidnightNewerExecStillWins(t *testing.T) {
	root := crossMidnightRoot(t, "2026-07-04T01:30:00.000Z", "2026-07-04T02:30:00.000Z")
	now, _ := time.Parse(time.RFC3339, "2026-07-04T03:00:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-exec" {
		t.Errorf("Model = %+v, want gpt-exec (its token_count is the freshest)", got.Model)
	}
}

// A session spanning a skipped-day gap (started Friday, still running Monday
// with no rollouts in between) must still be compared: candidate order comes
// from file mtime, so the empty days in between don't matter.
func TestCollectCrossMidnightSkipsEmptyDayGap(t *testing.T) {
	root := t.TempDir()
	friday := filepath.Join(root, "sessions", "2026", "07", "03")
	monday := filepath.Join(root, "sessions", "2026", "07", "06")
	for _, d := range []string{friday, monday} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Weekend-long session in Friday's directory, freshest token_count Monday 01:00.
	writeRollout(t, friday, "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-cccccccccccc.jsonl",
		ctxLine("2026-07-03T22:00:00.000Z", "gpt-weekend", "/w")+"\n"+
			tcLine("2026-07-06T01:00:00.000Z", 300000),
		time.Date(2026, 7, 6, 1, 0, 0, 0, time.UTC))
	// One-off exec Monday 00:30 in Monday's directory.
	writeRollout(t, monday, "rollout-2026-07-06T00-30-00-019e5933-2289-7e72-88fd-dddddddddddd.jsonl",
		ctxLine("2026-07-06T00:30:00.000Z", "gpt-exec", "/e")+"\n"+
			tcLine("2026-07-06T00:30:30.000Z", 400),
		time.Date(2026, 7, 6, 0, 30, 30, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-07-06T02:00:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-weekend" {
		t.Errorf("Model = %+v, want gpt-weekend (fresher token_count across the gap)", got.Model)
	}
}
