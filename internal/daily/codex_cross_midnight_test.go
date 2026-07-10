package daily

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/pricing"
)

// codexSessionAt builds a rollout with explicit event timestamps, so a file
// can span midnight: one cumulative total_token_usage snapshot per entry.
func codexSessionAt(entries ...[2]any) string {
	out := `{"type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n"
	for _, e := range entries {
		out += fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d}}}}`+"\n",
			e[0], e[1], e[1])
	}
	return out
}

// codexDayClock pins a deterministic local midnight: dayStart is today's
// local 00:00 for the returned now (02:00). Deriving event timestamps from
// dayStart keeps the "before/after local midnight" contract valid on any CI
// timezone (CodexTotals splits at now.Local()'s midnight).
func codexDayClock() (now, dayStart time.Time) {
	dayStart = time.Date(2026, 7, 4, 0, 0, 0, 0, time.Local)
	return dayStart.Add(2 * time.Hour), dayStart
}

// codexDayDir returns root's sessions/YYYY/MM/DD directory for now shifted by
// back days (local time).
func codexDayDir(root string, now time.Time, back int) string {
	d := now.Local().AddDate(0, 0, -back)
	return filepath.Join(root, "sessions", d.Format("2006"), d.Format("01"), d.Format("02"))
}

// Regression for issue #133 (2): a session that started yesterday and kept
// working past midnight stores its rollout in YESTERDAY's day directory. Only
// its growth since local midnight (150000-100000) counts into today.
func TestCodexTotalsCrossMidnightCountsTodayPortion(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	beforeMidnight := dayStart.Add(-10 * time.Minute).Format(time.RFC3339)
	afterMidnight := dayStart.Add(90 * time.Minute).Format(time.RFC3339)

	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-cccccccccccc.jsonl"),
		codexSessionAt([2]any{beforeMidnight, 100000}, [2]any{afterMidnight, 150000}), now)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 50000 {
		t.Errorf("CodexTotals.Tokens = %d, want 50000 (today's portion of the cross-midnight session)", got)
	}
}

// A session that finished before midnight contributes nothing to today, even
// though its rollout sits inside the scanned window.
func TestCodexTotalsIgnoresSessionFinishedYesterday(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	ts := dayStart.Add(-3 * time.Hour).Format(time.RFC3339)

	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T10-00-00-019e5933-2289-7e72-88fd-dddddddddddd.jsonl"),
		codexSessionAt([2]any{ts, 80000}), now)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 0 {
		t.Errorf("CodexTotals.Tokens = %d, want 0 (session ended yesterday)", got)
	}
}

// codexModelSwitchSession builds a rollout whose turn_context lines are
// interleaved with token_count snapshots, so a test can switch models
// mid-session. Each entry is either ["ctx", model] or ["tc", ts, total].
func codexModelSwitchSession(entries ...[]any) string {
	out := ""
	for _, e := range entries {
		switch e[0] {
		case "ctx":
			out += fmt.Sprintf(`{"type":"turn_context","payload":{"model":%q}}`+"\n", e[1])
		case "tc":
			out += fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d}}}}`+"\n",
				e[1], e[2], e[2])
		}
	}
	return out
}

// Regression for #191: a mid-session model switch prices each token_count
// delta at the model current when it was emitted, not the whole day's delta
// at the last model.
func TestCodexTotalsModelSwitchPricesPerEvent(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	t1 := dayStart.Add(30 * time.Minute).Format(time.RFC3339)
	t2 := dayStart.Add(60 * time.Minute).Format(time.RFC3339)

	writeFile(t, filepath.Join(codexDayDir(root, now, 0), "rollout-2026-07-04T00-10-00-019e5933-2289-7e72-88fd-111111111111.jsonl"),
		codexModelSwitchSession(
			[]any{"ctx", "gpt-a"}, []any{"tc", t1, int64(1_000_000)},
			[]any{"ctx", "gpt-b"}, []any{"tc", t2, int64(2_000_000)},
		), now)

	prices := pricing.Table{"gpt-a": {In: 1}, "gpt-b": {In: 10}}
	got := mustCodexTotals(t, root, now, prices)
	// 1M tokens at gpt-a ($1/M) + 1M at gpt-b ($10/M). Pricing the whole 2M
	// at the last model (gpt-b) would read $20.
	if got.Cost != 11.0 {
		t.Errorf("Cost = %v, want 11.0 (per-event pricing across the switch)", got.Cost)
	}
	if got.Tokens != 2_000_000 {
		t.Errorf("Tokens = %d, want 2000000 (token accounting unchanged)", got.Tokens)
	}
}

// The per-event pricing also holds across midnight and a resumed file: the
// pre-midnight snapshot is the delta base, and each today event is priced at
// its own then-current model even when the events live in different files.
func TestCodexTotalsModelSwitchAcrossResume(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	beforeMidnight := dayStart.Add(-10 * time.Minute).Format(time.RFC3339)
	after1 := dayStart.Add(30 * time.Minute).Format(time.RFC3339)
	after2 := dayStart.Add(90 * time.Minute).Format(time.RFC3339)

	// Yesterday's file: 100k before midnight, grows to 150k today, all gpt-a.
	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T20-00-00-019e5933-2289-7e72-88fd-222222222222.jsonl"),
		codexModelSwitchSession(
			[]any{"ctx", "gpt-a"}, []any{"tc", beforeMidnight, int64(100_000)}, []any{"tc", after1, int64(150_000)},
		), now)
	// Resumed today on gpt-b, carrying the cumulative forward to 250k.
	writeFile(t, filepath.Join(codexDayDir(root, now, 0), "rollout-2026-07-04T01-00-00-019e5933-2289-7e72-88fd-222222222222.jsonl"),
		codexModelSwitchSession(
			[]any{"ctx", "gpt-b"}, []any{"tc", after2, int64(250_000)},
		), now)

	prices := pricing.Table{"gpt-a": {In: 1}, "gpt-b": {In: 10}}
	got := mustCodexTotals(t, root, now, prices)
	// gpt-a: 150k-100k = 50k at $1/M = $0.05; gpt-b: 250k-150k = 100k at
	// $10/M = $1.00.
	if got.Cost != 1.05 {
		t.Errorf("Cost = %v, want 1.05 (0.05 gpt-a + 1.00 gpt-b)", got.Cost)
	}
	if got.Tokens != 150_000 {
		t.Errorf("Tokens = %d, want 150000 (250k latest - 100k pre-midnight)", got.Tokens)
	}
}

// Regression for #188: a session started well before any fixed scan window
// (5 days ago) and still running today keeps appending to its start-day
// file. Candidates come from mtime, so today's growth is still counted.
func TestCodexTotalsCountsSessionOlderThanScanWindow(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	beforeMidnight := dayStart.Add(-10 * time.Minute).Format(time.RFC3339)
	afterMidnight := dayStart.Add(90 * time.Minute).Format(time.RFC3339)

	writeFile(t, filepath.Join(codexDayDir(root, now, 5), "rollout-2026-06-29T08-00-00-019e5933-2289-7e72-88fd-ffffffffffff.jsonl"),
		codexSessionAt([2]any{beforeMidnight, 100000}, [2]any{afterMidnight, 150000}), now)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 50000 {
		t.Errorf("CodexTotals.Tokens = %d, want 50000 (today's growth of a 5-day-old session)", got)
	}
}

// The pre-midnight file of a resumed session was last written days ago
// (mtime before today), so it is read only because today's resumed file
// shares its session id — and without it the whole cumulative would be
// misread as today's growth.
func TestCodexTotalsResumeReadsOldFileBySessionID(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	final := dayStart.Add(-30 * time.Hour) // last write: the day before yesterday
	resumed := dayStart.Add(60 * time.Minute).Format(time.RFC3339)
	latest := dayStart.Add(110 * time.Minute).Format(time.RFC3339)

	// Same session UUID: the old file holds the delta base (100000), the
	// resumed file carries the cumulative forward to 150000.
	writeFile(t, filepath.Join(codexDayDir(root, now, 3), "rollout-2026-07-01T20-00-00-019e5933-2289-7e72-88fd-abababababab.jsonl"),
		codexSessionAt([2]any{final.Format(time.RFC3339), 100000}), final)
	writeFile(t, filepath.Join(codexDayDir(root, now, 0), "rollout-2026-07-04T01-00-00-019e5933-2289-7e72-88fd-abababababab.jsonl"),
		codexSessionAt([2]any{resumed, 120000}, [2]any{latest, 150000}), now)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 50000 {
		t.Errorf("CodexTotals.Tokens = %d, want 50000 (150000 latest - 100000 base from the old file)", got)
	}
}

// A cross-midnight session that RESUMED into a second rollout today carries
// its cumulative total forward. The session must be counted once: growth from
// the pre-midnight snapshot (yesterday's file) to the resumed file's latest,
// not the naive sum of both files.
func TestCodexTotalsCrossMidnightResumeCountsOnce(t *testing.T) {
	root := t.TempDir()
	now, dayStart := codexDayClock()
	beforeMidnight := dayStart.Add(-10 * time.Minute).Format(time.RFC3339)
	resumed := dayStart.Add(60 * time.Minute).Format(time.RFC3339)
	latest := dayStart.Add(110 * time.Minute).Format(time.RFC3339)

	// Same session UUID in both files; the resumed file carries the total forward.
	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-eeeeeeeeeeee.jsonl"),
		codexSessionAt([2]any{beforeMidnight, 100000}), now)
	writeFile(t, filepath.Join(codexDayDir(root, now, 0), "rollout-2026-07-04T01-00-00-019e5933-2289-7e72-88fd-eeeeeeeeeeee.jsonl"),
		codexSessionAt([2]any{resumed, 120000}, [2]any{latest, 150000}), now)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 50000 {
		t.Errorf("CodexTotals.Tokens = %d, want 50000 (150000 latest - 100000 pre-midnight, counted once)", got)
	}
}
