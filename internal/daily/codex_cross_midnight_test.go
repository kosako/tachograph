package daily

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
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
	// 02:00 UTC = 11:00 JST — "today" locally; midnight JST = 15:00 UTC prev day.
	now := time.Date(2026, 7, 4, 2, 0, 0, 0, time.UTC)
	beforeMidnight := time.Date(2026, 7, 3, 14, 50, 0, 0, time.UTC).Format(time.RFC3339) // 23:50 JST 07-03
	afterMidnight := time.Date(2026, 7, 3, 16, 30, 0, 0, time.UTC).Format(time.RFC3339)  // 01:30 JST 07-04

	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-cccccccccccc.jsonl"),
		codexSessionAt([2]any{beforeMidnight, 100000}, [2]any{afterMidnight, 150000}), now)

	if got := CodexTotals(root, now, noPrices).Tokens; got != 50000 {
		t.Errorf("CodexTotals.Tokens = %d, want 50000 (today's portion of the cross-midnight session)", got)
	}
}

// A session that finished before midnight contributes nothing to today, even
// though its rollout sits inside the scanned window.
func TestCodexTotalsIgnoresSessionFinishedYesterday(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 4, 2, 0, 0, 0, time.UTC)
	ts := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).Format(time.RFC3339) // 21:00 JST 07-03

	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T10-00-00-019e5933-2289-7e72-88fd-dddddddddddd.jsonl"),
		codexSessionAt([2]any{ts, 80000}), now)

	if got := CodexTotals(root, now, noPrices).Tokens; got != 0 {
		t.Errorf("CodexTotals.Tokens = %d, want 0 (session ended yesterday)", got)
	}
}

// A cross-midnight session that RESUMED into a second rollout today carries
// its cumulative total forward. The session must be counted once: growth from
// the pre-midnight snapshot (yesterday's file) to the resumed file's latest,
// not the naive sum of both files.
func TestCodexTotalsCrossMidnightResumeCountsOnce(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 4, 2, 0, 0, 0, time.UTC)
	beforeMidnight := time.Date(2026, 7, 3, 14, 50, 0, 0, time.UTC).Format(time.RFC3339) // 23:50 JST 07-03
	resumed := time.Date(2026, 7, 3, 16, 0, 0, 0, time.UTC).Format(time.RFC3339)         // 01:00 JST 07-04
	latest := time.Date(2026, 7, 3, 17, 0, 0, 0, time.UTC).Format(time.RFC3339)          // 02:00 JST 07-04

	// Same session UUID in both files; the resumed file carries the total forward.
	writeFile(t, filepath.Join(codexDayDir(root, now, 1), "rollout-2026-07-03T22-00-00-019e5933-2289-7e72-88fd-eeeeeeeeeeee.jsonl"),
		codexSessionAt([2]any{beforeMidnight, 100000}), now)
	writeFile(t, filepath.Join(codexDayDir(root, now, 0), "rollout-2026-07-04T01-00-00-019e5933-2289-7e72-88fd-eeeeeeeeeeee.jsonl"),
		codexSessionAt([2]any{resumed, 120000}, [2]any{latest, 150000}), now)

	if got := CodexTotals(root, now, noPrices).Tokens; got != 50000 {
		t.Errorf("CodexTotals.Tokens = %d, want 50000 (150000 latest - 100000 pre-midnight, counted once)", got)
	}
}
