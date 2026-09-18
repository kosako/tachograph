package daily

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/pricing"
)

// daysClock pins a deterministic local clock for the multi-day tests: now is
// 02:00 on 2026-07-04, so "today" starts at that local midnight.
func daysClock() (now, today time.Time) {
	today = time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	return today.Add(2 * time.Hour), today
}

func dayKeyBack(today time.Time, back int) string {
	return DayKey(today.AddDate(0, 0, -back))
}

// One transcript touched today holds messages from three days; a second one
// last written two days ago holds a message from that day. Each message lands
// on its own day, the window's mtime filter keeps the older file when its day
// is inside the window, and today's figure is exactly ClaudeTotals.
func TestClaudeDaysSplitsMessagesByDay(t *testing.T) {
	root := t.TempDir()
	now, today := daysClock()
	proj := filepath.Join(root, "projects", "p")
	writeFile(t, filepath.Join(proj, "live.jsonl"),
		claudeMsg(today.AddDate(0, 0, -2).Add(10*time.Hour), 100, 0, 0, 10)+"\n"+
			claudeMsg(today.AddDate(0, 0, -1).Add(10*time.Hour), 200, 0, 0, 20)+"\n"+
			claudeMsg(today.Add(1*time.Hour), 300, 0, 0, 30)+"\n", now)
	old := today.AddDate(0, 0, -2).Add(11 * time.Hour)
	writeFile(t, filepath.Join(proj, "old.jsonl"), claudeMsg(old, 1000, 0, 0, 0)+"\n", old)

	got, err := ClaudeDays(root, today.AddDate(0, 0, -3), today.AddDate(0, 0, 1), noPrices)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{dayKeyBack(today, 2): 1110, dayKeyBack(today, 1): 220, dayKeyBack(today, 0): 330}
	for day, tokens := range want {
		if got[day].Tokens != tokens {
			t.Errorf("ClaudeDays[%s].Tokens = %d, want %d", day, got[day].Tokens, tokens)
		}
	}
	if _, ok := got[dayKeyBack(today, 3)]; ok {
		t.Errorf("ClaudeDays has an entry for a day without usage: %+v", got)
	}
	if len(got) != 3 {
		t.Errorf("ClaudeDays = %+v, want exactly 3 days", got)
	}

	// A window opening after old.jsonl's last write skips that file.
	got, err = ClaudeDays(root, today.AddDate(0, 0, -1), today.AddDate(0, 0, 1), noPrices)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[dayKeyBack(today, 2)]; ok || len(got) != 2 {
		t.Errorf("ClaudeDays(2-day window) = %+v, want only the last two days", got)
	}

	tot := mustClaudeTotals(t, root, now, noPrices)
	if tot.Tokens != 330 {
		t.Errorf("ClaudeTotals.Tokens = %d, want 330 (today's row of ClaudeDays)", tot.Tokens)
	}
}

// A response copied forward into a second transcript (resume) is counted
// once even when the copies sit on different days' files.
func TestClaudeDaysDedupAcrossFiles(t *testing.T) {
	root := t.TempDir()
	now, today := daysClock()
	proj := filepath.Join(root, "projects", "p")
	ts := today.AddDate(0, 0, -1).Add(10 * time.Hour)
	writeFile(t, filepath.Join(proj, "a.jsonl"), claudeMsgID(ts, "msg_1", "req_1", 100, 0, 0, 10)+"\n", ts)
	writeFile(t, filepath.Join(proj, "b.jsonl"), claudeMsgID(ts, "msg_1", "req_1", 100, 0, 0, 10)+"\n", now)

	got, err := ClaudeDays(root, today.AddDate(0, 0, -1), today.AddDate(0, 0, 1), noPrices)
	if err != nil {
		t.Fatal(err)
	}
	if got[dayKeyBack(today, 1)].Tokens != 110 || len(got) != 1 {
		t.Errorf("ClaudeDays = %+v, want yesterday 110 only", got)
	}
}

// A single Codex session that ran across three days contributes each day's
// growth to that day: the delta base of a day is the last snapshot before
// it. A snapshot past the window's end is left out without disturbing the
// earlier days.
func TestCodexDaysSplitsSessionGrowthByDay(t *testing.T) {
	root := t.TempDir()
	now, today := daysClock()
	d2 := today.AddDate(0, 0, -2)
	d1 := today.AddDate(0, 0, -1)
	writeFile(t, filepath.Join(codexDayDir(root, now, 2), "rollout-2026-07-02T10-00-00-019e5933-2289-7e72-88fd-111111111111.jsonl"),
		codexSessionAt(
			[2]any{d2.Add(10 * time.Hour).Format(time.RFC3339), 1000},
			[2]any{d2.Add(11 * time.Hour).Format(time.RFC3339), 1500},
			[2]any{d1.Add(9 * time.Hour).Format(time.RFC3339), 1800},
			[2]any{today.Add(1 * time.Hour).Format(time.RFC3339), 2000},
		), now)

	got, err := CodexDays(root, d2, today.AddDate(0, 0, 1), pricing.Table{"gpt-5.5": {In: 1}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{DayKey(d2): 1500, DayKey(d1): 300, DayKey(today): 200}
	for day, tokens := range want {
		if got[day].Tokens != tokens {
			t.Errorf("CodexDays[%s].Tokens = %d, want %d", day, got[day].Tokens, tokens)
		}
		if cost := float64(tokens) * 1 / 1e6; got[day].Cost != cost {
			t.Errorf("CodexDays[%s].Cost = %v, want %v", day, got[day].Cost, cost)
		}
	}
	if len(got) != 3 {
		t.Errorf("CodexDays = %+v, want exactly 3 days", got)
	}

	// Window ending yesterday: today's snapshot is outside it, yesterday's
	// delta is unchanged.
	got, err = CodexDays(root, d2, today, noPrices)
	if err != nil {
		t.Fatal(err)
	}
	if got[DayKey(d1)].Tokens != 300 || len(got) != 2 {
		t.Errorf("CodexDays(ending today) = %+v, want d2=1500 / d1=300 only", got)
	}

	if tot := mustCodexTotals(t, root, now, noPrices); tot.Tokens != 200 {
		t.Errorf("CodexTotals.Tokens = %d, want 200 (today's row of CodexDays)", tot.Tokens)
	}
}

// A session resumed inside the window carries its cumulative forward from a
// file last written before the window opened; that file supplies the delta
// base for the first day the session appears in, and later days chain from
// the resumed file's own snapshots.
func TestCodexDaysResumeChainsBaseAcrossDays(t *testing.T) {
	root := t.TempDir()
	now, today := daysClock()
	d1 := today.AddDate(0, 0, -1)
	id := "019e5933-2289-7e72-88fd-222222222222"
	final := today.AddDate(0, 0, -3).Add(20 * time.Hour)
	writeFile(t, filepath.Join(codexDayDir(root, now, 3), "rollout-2026-07-01T20-00-00-"+id+".jsonl"),
		codexSessionAt([2]any{final.Format(time.RFC3339), 100000}), final)
	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T09-00-00-"+id+".jsonl"),
		codexSessionAt(
			[2]any{d1.Add(9 * time.Hour).Format(time.RFC3339), 120000},
			[2]any{today.Add(1 * time.Hour).Format(time.RFC3339), 150000},
		), now)

	got, err := CodexDays(root, today.AddDate(0, 0, -2), today.AddDate(0, 0, 1), noPrices)
	if err != nil {
		t.Fatal(err)
	}
	if got[DayKey(d1)].Tokens != 20000 || got[DayKey(today)].Tokens != 30000 || len(got) != 2 {
		t.Errorf("CodexDays = %+v, want yesterday 20000 (120000-100000 from the old file) / today 30000", got)
	}
}

// A rollout without parsable timestamps is attributed whole to its own day
// directory when that day is inside the window, and dropped otherwise.
func TestCodexDaysTimestampLessRolloutUsesDirectoryDay(t *testing.T) {
	root := t.TempDir()
	now, today := daysClock()
	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "s1.jsonl"), codexSession(700), now)
	writeFile(t, filepath.Join(codexDayDir(root, now, 5), "s2.jsonl"), codexSession(9999), now)

	got, err := CodexDays(root, today.AddDate(0, 0, -2), today.AddDate(0, 0, 1), noPrices)
	if err != nil {
		t.Fatal(err)
	}
	if got[dayKeyBack(today, 1)].Tokens != 700 || len(got) != 1 {
		t.Errorf("CodexDays = %+v, want yesterday 700 only", got)
	}
}

// An unreadable day directory inside the window makes the whole result
// unknown (#180), even when it isn't today's.
func TestCodexDaysErrorWhenWindowDayDirUnreadable(t *testing.T) {
	root := t.TempDir()
	now, today := daysClock()
	writeFile(t, filepath.Join(codexDayDir(root, now, 0), "s1.jsonl"), codexSession(100), now)
	// A regular file where yesterday's directory should be: listing it fails.
	writeFile(t, codexDayDir(root, now, 1), "not a directory", now)

	if _, err := CodexDays(root, today.AddDate(0, 0, -1), today.AddDate(0, 0, 1), noPrices); err == nil {
		t.Error("CodexDays = nil error, want an error for the unlistable day directory")
	}
}
