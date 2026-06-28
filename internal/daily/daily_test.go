package daily

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/pricing"
)

var noPrices = pricing.Table{}

func writeFile(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func claudeMsg(ts time.Time, in, cc, cr, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		ts.Format(time.RFC3339), in, cc, cr, out)
}

func claudeMsgID(ts time.Time, id, req string, in, cc, cr, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":%q,"message":{"id":%q,"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		ts.Format(time.RFC3339), req, id, in, cc, cr, out)
}

// TestSameDayLocalBoundary pins the local-day boundary used to slice "today":
// the first and last instant of the calendar day count, one second either side
// does not. sameDay compares formatted calendar dates (no 24h arithmetic), so
// this stays correct across DST transitions. Instants are built explicitly in
// time.Local so the RFC 3339 offset matches what sameDay re-localizes to.
func TestSameDayLocalBoundary(t *testing.T) {
	day := "2026-06-28"
	cases := []struct {
		name string
		ts   time.Time
		want bool
	}{
		{"start of day", time.Date(2026, 6, 28, 0, 0, 0, 0, time.Local), true},
		{"end of day", time.Date(2026, 6, 28, 23, 59, 59, 0, time.Local), true},
		{"one second before", time.Date(2026, 6, 27, 23, 59, 59, 0, time.Local), false},
		{"start of next day", time.Date(2026, 6, 29, 0, 0, 0, 0, time.Local), false},
	}
	for _, c := range cases {
		ts := c.ts.Format(time.RFC3339)
		if got := sameDay(ts, day); got != c.want {
			t.Errorf("%s: sameDay(%q, %q) = %v, want %v", c.name, ts, day, got, c.want)
		}
	}
}

func TestClaudeTokens(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	// today.jsonl: two of today's messages + one yesterday (excluded).
	// New tokens exclude cache reads: msg1=100+5000+400=5500, msg2=2+673+481=1156.
	today := claudeMsg(now, 100, 5000, 30000, 400) + "\n" +
		claudeMsg(now, 2, 673, 39451, 481) + "\n" +
		claudeMsg(yesterday, 9, 9, 9, 9) + "\n"
	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"), today, now)

	// old.jsonl: only yesterday → skipped by the mtime filter.
	writeFile(t, filepath.Join(root, "projects", "p", "old.jsonl"),
		claudeMsg(yesterday, 1000, 0, 0, 1000)+"\n", yesterday)

	got := ClaudeTotals(root, now, noPrices).Tokens
	if want := int64(5500 + 1156); got != want {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got, want)
	}

	// With a price for the model, cost is summed across today's messages.
	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	cost := ClaudeTotals(root, now, prices).Cost
	// msg1: (100*15 + 5000*18.75 + 30000*1.5 + 400*75)/1e6
	// msg2: (2*15 + 673*18.75 + 39451*1.5 + 481*75)/1e6
	want := (100*15.0+5000*18.75+30000*1.5+400*75)/1e6 +
		(2*15.0+673*18.75+39451*1.5+481*75)/1e6
	if diff := cost - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, want)
	}
}

func TestClaudeSessionToday(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	// One session file with today's two messages + one from yesterday (excluded).
	// New tokens exclude cache reads: 100+5000+400=5500, 2+673+481=1156.
	path := filepath.Join(root, "sess.jsonl")
	content := claudeMsg(now, 100, 5000, 30000, 400) + "\n" +
		claudeMsg(now, 2, 673, 39451, 481) + "\n" +
		claudeMsg(yesterday, 9999, 0, 0, 9999) + "\n"
	writeFile(t, path, content, now)

	if got := ClaudeSessionToday(path, now, noPrices).Tokens; got != int64(5500+1156) {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d", got, 5500+1156)
	}
	// Empty path and missing file are both zero, not a crash.
	if got := ClaudeSessionToday("", now, noPrices); got.Tokens != 0 {
		t.Errorf("empty path = %+v, want zero", got)
	}
	if got := ClaudeSessionToday(filepath.Join(root, "nope.jsonl"), now, noPrices); got.Tokens != 0 {
		t.Errorf("missing file = %+v, want zero", got)
	}
}

// A single response is written once per content block, each line repeating the
// same usage; it must be counted once. The same response duplicated across
// files (resume/compaction copies prior turns forward) must not double-count.
func TestClaudeDedup(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// Response A as 3 content-block lines (identical usage) + distinct B once.
	// New tokens exclude cache reads: A=10+20+5=35, B=1+2+3=6.
	a := claudeMsgID(now, "msg_a", "req_a", 10, 20, 100, 5)
	b := claudeMsgID(now, "msg_b", "req_b", 1, 2, 50, 3)
	writeFile(t, filepath.Join(root, "projects", "p", "s1.jsonl"),
		a+"\n"+a+"\n"+a+"\n"+b+"\n", now)
	// A second file re-includes response A (resume copies the prior turn).
	writeFile(t, filepath.Join(root, "projects", "p", "s2.jsonl"), a+"\n", now)

	if got := ClaudeTotals(root, now, noPrices).Tokens; got != int64(35+6) {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d (A once + B once)", got, 35+6)
	}

	// Cost dedups identically: A + B priced once each.
	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	wantCost := (10*15.0+20*18.75+100*1.5+5*75)/1e6 + (1*15.0+2*18.75+50*1.5+3*75)/1e6
	if cost := ClaudeTotals(root, now, prices).Cost; cost-wantCost > 1e-9 || cost-wantCost < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, wantCost)
	}

	// ClaudeSessionToday dedups within the file: A counted once.
	if got := ClaudeSessionToday(filepath.Join(root, "projects", "p", "s1.jsonl"), now, noPrices).Tokens; got != int64(35+6) {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d", got, 35+6)
	}
}

func codexSession(total int64) string {
	return fmt.Sprintf(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`+"\n"+
		`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d}}}}`+"\n", total, total)
}

func TestCodexTotals(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), codexSession(1000), now)
	writeFile(t, filepath.Join(dayDir, "s2.jsonl"), codexSession(500), now)

	// Yesterday's folder must be ignored.
	y := now.Add(-24 * time.Hour)
	yDir := filepath.Join(root, "sessions", y.Local().Format("2006"), y.Local().Format("01"), y.Local().Format("02"))
	writeFile(t, filepath.Join(yDir, "old.jsonl"), codexSession(9999), y)

	if got := CodexTotals(root, now, noPrices).Tokens; got != 1500 {
		t.Errorf("CodexTotals.Tokens = %d, want 1500", got)
	}
	// input=1000/500 all non-cached, priced at In=2/Mtok → (1000+500)*2/1e6.
	prices := pricing.Table{"gpt-5": {In: 2}}
	if cost := CodexTotals(root, now, prices).Cost; cost != 1500*2.0/1e6 {
		t.Errorf("CodexTotals.Cost = %v, want %v", cost, 1500*2.0/1e6)
	}
}

// A Codex session that resumed into a second rollout carries its cumulative
// total forward; daily must count it once (the largest cumulative), not sum
// both files. Distinct sessions still add up.
func TestCodexTotalsDedupSession(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	id := "019e5933-2289-7e72-88fd-c494201693fa"
	// Same session id in two files: cumulative 1000 then 1500 (resumed).
	writeFile(t, filepath.Join(dayDir, "rollout-2026-05-24T10-00-00-"+id+".jsonl"), codexSession(1000), now)
	writeFile(t, filepath.Join(dayDir, "rollout-2026-05-24T11-00-00-"+id+".jsonl"), codexSession(1500), now)
	// A distinct session adds 500.
	writeFile(t, filepath.Join(dayDir, "rollout-2026-05-24T12-00-00-019e0000-0000-7000-8000-000000000000.jsonl"), codexSession(500), now)

	if got := CodexTotals(root, now, noPrices).Tokens; got != int64(1500+500) {
		t.Errorf("CodexTotals.Tokens = %d, want %d (resumed session counted once at max cumulative + distinct)", got, 1500+500)
	}
}

func TestEmptyRoots(t *testing.T) {
	if got := ClaudeTotals(t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("ClaudeTotals(empty) = %+v", got)
	}
	if got := CodexTotals(t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("CodexTotals(empty) = %+v", got)
	}
}
