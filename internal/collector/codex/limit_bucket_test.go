package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bucketTcLine builds a token_count tagged with a rate_limits.limit_id. Codex
// writes one token_count per limit bucket: "codex" carries the account's
// windows, while buckets such as "premium" (no windows) or model-specific
// ones like "codex_bengalfox" can follow it within the same turn (#258).
// windowMins <= 0 leaves primary null.
func bucketTcLine(ts, limitID string, total int64, windowMins int, used float64) string {
	primary := "null"
	if windowMins > 0 {
		primary = fmt.Sprintf(`{"used_percent":%g,"window_minutes":%d,"resets_at":1790000000}`, used, windowMins)
	}
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d},"model_context_window":200000},"rate_limits":{"limit_id":%q,"primary":%s,"secondary":null,"credits":{"has_credits":false,"unlimited":false,"balance":"0"},"plan_type":"plus"}}}`,
		ts, total, total, limitID, primary)
}

func bucketDay(t *testing.T) (root, day string) {
	t.Helper()
	root = t.TempDir()
	day = filepath.Join(root, "sessions", "2026", "09", "26")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, day
}

func onlyLimit(t *testing.T, gotLimits int, wantMins int, gotMins *int, gotUsed *float64, wantUsed float64) {
	t.Helper()
	if gotLimits != 1 {
		t.Fatalf("len(Limits) = %d, want 1", gotLimits)
	}
	if gotMins == nil || *gotMins != wantMins {
		t.Errorf("WindowMinutes = %v, want %d", gotMins, wantMins)
	}
	if gotUsed == nil || *gotUsed != wantUsed {
		t.Errorf("UsedPct = %v, want %v", gotUsed, wantUsed)
	}
}

// A "premium" token_count (usage info and plan, but no windows) written right
// after the "codex" one must not replace the account's limits: the limits
// come from the codex bucket, while session usage still comes from the
// latest token_count.
func TestCollectLimitsIgnorePremiumBucketAtEnd(t *testing.T) {
	root, day := bucketDay(t)
	writeRollout(t, day, "rollout-2026-09-26T10-00-00-019e5933-2289-7e72-88fd-000000000001.jsonl",
		ctxLine("2026-09-26T10:00:00.000Z", "gpt-x", "/x")+"\n"+
			bucketTcLine("2026-09-26T10:05:00.000Z", "codex", 100, 10080, 40)+"\n"+
			bucketTcLine("2026-09-26T10:05:01.000Z", "premium", 110, 0, 0),
		time.Date(2026, 9, 26, 10, 5, 1, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-09-26T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	var mins *int
	var used *float64
	if len(got.Limits) > 0 {
		mins, used = got.Limits[0].WindowMinutes, got.Limits[0].UsedPct
	}
	onlyLimit(t, len(got.Limits), 10080, mins, used, 40)
	if got.Plan == nil || *got.Plan != "plus" {
		t.Errorf("Plan = %v, want plus", got.Plan)
	}
	if got.Session == nil || got.Session.Tokens == nil || got.Session.Tokens.Total != 110 {
		t.Errorf("Session.Tokens = %+v, want 110 from the latest token_count", got.Session)
	}
}

// A model-specific bucket (e.g. "codex_bengalfox") has its own weekly window;
// it must not stand in for the account's codex window.
func TestCollectLimitsIgnoreModelSpecificBucket(t *testing.T) {
	root, day := bucketDay(t)
	writeRollout(t, day, "rollout-2026-09-26T10-00-00-019e5933-2289-7e72-88fd-000000000002.jsonl",
		ctxLine("2026-09-26T10:00:00.000Z", "gpt-x", "/x")+"\n"+
			bucketTcLine("2026-09-26T10:05:00.000Z", "codex", 100, 10080, 40)+"\n"+
			bucketTcLine("2026-09-26T10:05:01.000Z", "codex_bengalfox", 110, 10080, 90),
		time.Date(2026, 9, 26, 10, 5, 1, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-09-26T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	var mins *int
	var used *float64
	if len(got.Limits) > 0 {
		mins, used = got.Limits[0].WindowMinutes, got.Limits[0].UsedPct
	}
	onlyLimit(t, len(got.Limits), 10080, mins, used, 40)
}

// When the freshest rollout holds only non-codex buckets, its session usage
// still wins, but the limits come from the freshest codex bucket in an older
// rollout — rate limits are account-global.
func TestCollectLimitsFromOlderRolloutWhenNewestHasNoCodexBucket(t *testing.T) {
	root, day := bucketDay(t)
	writeRollout(t, day, "rollout-2026-09-26T10-00-00-019e5933-2289-7e72-88fd-000000000003.jsonl",
		ctxLine("2026-09-26T10:00:00.000Z", "gpt-old", "/old")+"\n"+
			bucketTcLine("2026-09-26T10:05:00.000Z", "codex", 100, 10080, 40),
		time.Date(2026, 9, 26, 10, 5, 0, 0, time.UTC))
	writeRollout(t, day, "rollout-2026-09-26T10-08-00-019e5933-2289-7e72-88fd-000000000004.jsonl",
		ctxLine("2026-09-26T10:08:00.000Z", "gpt-new", "/new")+"\n"+
			bucketTcLine("2026-09-26T10:10:00.000Z", "premium", 500, 0, 0),
		time.Date(2026, 9, 26, 10, 10, 0, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-09-26T10:11:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Error != nil {
		t.Fatalf("Error = %+v", got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-new" {
		t.Errorf("Model = %+v, want gpt-new (the freshest session)", got.Model)
	}
	if got.Session == nil || got.Session.Tokens == nil || got.Session.Tokens.Total != 500 {
		t.Errorf("Session.Tokens = %+v, want 500 from the freshest session", got.Session)
	}
	var mins *int
	var used *float64
	if len(got.Limits) > 0 {
		mins, used = got.Limits[0].WindowMinutes, got.Limits[0].UsedPct
	}
	onlyLimit(t, len(got.Limits), 10080, mins, used, 40)
}

// The limits search is bounded: limits more than the stale window older than
// the freshest session are not resurrected (they could only render stale, and
// a session that never carries limits must not make every rollout get read).
func TestCollectLimitsSearchBoundedByStaleWindow(t *testing.T) {
	root, day := bucketDay(t)
	writeRollout(t, day, "rollout-2026-09-26T01-00-00-019e5933-2289-7e72-88fd-000000000005.jsonl",
		ctxLine("2026-09-26T01:00:00.000Z", "gpt-old", "/old")+"\n"+
			bucketTcLine("2026-09-26T01:05:00.000Z", "codex", 100, 10080, 40),
		time.Date(2026, 9, 26, 1, 5, 0, 0, time.UTC))
	writeRollout(t, day, "rollout-2026-09-26T10-08-00-019e5933-2289-7e72-88fd-000000000006.jsonl",
		ctxLine("2026-09-26T10:08:00.000Z", "gpt-new", "/new")+"\n"+
			tcLine("2026-09-26T10:10:00.000Z", 500),
		time.Date(2026, 9, 26, 10, 10, 0, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-09-26T10:11:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Session == nil || got.Session.Tokens == nil || got.Session.Tokens.Total != 500 {
		t.Errorf("Session.Tokens = %+v, want 500 from the freshest session", got.Session)
	}
	if got.Limits != nil || got.Plan != nil {
		t.Errorf("Limits = %+v, Plan = %v, want none (limits 9h older than the session are out of range)", got.Limits, got.Plan)
	}
	if got.Stale {
		t.Error("Stale = true, want false (the session itself is fresh)")
	}
}

// The stale-window bound applies to the limits' event time, not the file's
// mtime: an older rollout touched recently (a later non-token event) must not
// contribute limits recorded 9h before the freshest session.
func TestCollectLimitsBoundByEventTimeNotMtime(t *testing.T) {
	root, day := bucketDay(t)
	writeRollout(t, day, "rollout-2026-09-26T01-00-00-019e5933-2289-7e72-88fd-000000000007.jsonl",
		ctxLine("2026-09-26T01:00:00.000Z", "gpt-old", "/old")+"\n"+
			bucketTcLine("2026-09-26T01:05:00.000Z", "codex", 100, 10080, 40)+"\n"+
			ctxLine("2026-09-26T10:09:00.000Z", "gpt-old", "/old"),
		time.Date(2026, 9, 26, 10, 9, 0, 0, time.UTC))
	writeRollout(t, day, "rollout-2026-09-26T10-08-00-019e5933-2289-7e72-88fd-000000000008.jsonl",
		ctxLine("2026-09-26T10:08:00.000Z", "gpt-new", "/new")+"\n"+
			tcLine("2026-09-26T10:10:00.000Z", 500),
		time.Date(2026, 9, 26, 10, 10, 0, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-09-26T10:11:00Z")
	got := Collect(Options{Root: root, Now: now})
	if got.Limits != nil || got.Plan != nil {
		t.Errorf("Limits = %+v, Plan = %v, want none (limits recorded 9h before the session)", got.Limits, got.Plan)
	}
	if got.Stale {
		t.Error("Stale = true, want false (the session itself is fresh)")
	}
}

// When session and limits come from different token_counts, collected_at and
// stale follow the older one: here the limits (10:05) are past the 5h stale
// threshold at 15:07 while the session (10:10) is not.
func TestCollectCollectedAtFollowsOlderSource(t *testing.T) {
	root, day := bucketDay(t)
	writeRollout(t, day, "rollout-2026-09-26T10-00-00-019e5933-2289-7e72-88fd-000000000009.jsonl",
		ctxLine("2026-09-26T10:00:00.000Z", "gpt-old", "/old")+"\n"+
			bucketTcLine("2026-09-26T10:05:00.000Z", "codex", 100, 10080, 40),
		time.Date(2026, 9, 26, 10, 5, 0, 0, time.UTC))
	writeRollout(t, day, "rollout-2026-09-26T10-08-00-019e5933-2289-7e72-88fd-000000000010.jsonl",
		ctxLine("2026-09-26T10:08:00.000Z", "gpt-new", "/new")+"\n"+
			bucketTcLine("2026-09-26T10:10:00.000Z", "premium", 500, 0, 0),
		time.Date(2026, 9, 26, 10, 10, 0, 0, time.UTC))

	now, _ := time.Parse(time.RFC3339, "2026-09-26T15:07:00Z")
	got := Collect(Options{Root: root, Now: now})
	if len(got.Limits) != 1 {
		t.Fatalf("len(Limits) = %d, want 1 (limits within the window are used)", len(got.Limits))
	}
	want := time.Date(2026, 9, 26, 10, 5, 0, 0, time.UTC)
	if got.CollectedAt == nil {
		t.Fatal("CollectedAt is nil")
	}
	if at, err := time.Parse(time.RFC3339, *got.CollectedAt); err != nil || !at.Equal(want) {
		t.Errorf("CollectedAt = %s, want %s (the older source)", *got.CollectedAt, want.Local().Format(time.RFC3339))
	}
	if !got.Stale {
		t.Error("Stale = false, want true (the limits are 5h02m old)")
	}
}

// lastEvents stops looking for limits past the stale window once the
// session's token_count and turn_context are found, instead of parsing a
// limits-less session back to its start.
func TestLastEventsLimitsSearchBounded(t *testing.T) {
	lines := [][]byte{
		[]byte(bucketTcLine("2026-09-26T03:00:00.000Z", "codex", 50, 10080, 40)),
		[]byte(ctxLine("2026-09-26T09:59:00.000Z", "gpt-x", "/x")),
		[]byte(tcLine("2026-09-26T10:00:00.000Z", 100)),
	}
	tc, lim, turn := lastEvents(lines)
	if tc == nil || turn == nil {
		t.Fatalf("tc = %v, turn = %v, want both found", tc, turn)
	}
	if lim != nil {
		t.Errorf("lim = %+v, want nil (7h before the session, past the window)", lim)
	}
	// Within the window the same line is found.
	lines[0] = []byte(bucketTcLine("2026-09-26T06:00:00.000Z", "codex", 50, 10080, 40))
	if _, lim, _ := lastEvents(lines); lim == nil {
		t.Error("lim = nil, want the codex bucket 4h before the session")
	}
}

func TestAccountLimits(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{bucketTcLine("2026-09-26T10:00:00Z", "codex", 1, 10080, 1), true},
		{bucketTcLine("2026-09-26T10:00:00Z", "premium", 1, 0, 0), false},
		{bucketTcLine("2026-09-26T10:00:00Z", "codex_bengalfox", 1, 10080, 1), false},
		{tcLine("2026-09-26T10:00:00Z", 1), false}, // no rate_limits at all
		// Legacy token_counts carry no limit_id; they are the account bucket.
		{`{"timestamp":"2026-09-26T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":5,"window_minutes":300,"resets_at":1},"secondary":null,"plan_type":null}}}`, true},
	}
	for i, c := range cases {
		ev, ok := ParseEvent([]byte(c.line))
		if !ok {
			t.Fatalf("case %d: ParseEvent failed", i)
		}
		tc := ev.TokenCount()
		if tc == nil {
			t.Fatalf("case %d: not a token_count", i)
		}
		if got := tc.AccountLimits(); got != c.want {
			t.Errorf("case %d: AccountLimits() = %v, want %v", i, got, c.want)
		}
	}
}
