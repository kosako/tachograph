package daily

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func claudeMsgCacheCreation(ts time.Time, in, cc5m, cc1h, cr, out int64) string {
	return claudeMsgCacheCreationTotal(ts, in, cc5m+cc1h, cc5m, cc1h, cr, out)
}

func claudeMsgCacheCreationTotal(ts time.Time, in, totalCC, cc5m, cc1h, cr, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		ts.Format(time.RFC3339), in, totalCC, cr, out, cc5m, cc1h)
}

// TestDayInLocalBoundary pins the local-day boundary used to slice "today":
// the first and last instant of the calendar day count, one second either side
// does not. The window is built from DayStart (time.Date in time.Local, no 24h
// arithmetic), so this stays correct across DST transitions. Instants are
// built explicitly in time.Local so the RFC 3339 offset matches what dayIn
// re-localizes to.
func TestDayInLocalBoundary(t *testing.T) {
	from := DayStart(time.Date(2026, 6, 28, 12, 0, 0, 0, time.Local))
	to := from.AddDate(0, 0, 1)
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
		day, got := dayIn(ts, from, to)
		if got != c.want {
			t.Errorf("%s: dayIn(%q) = %v, want %v", c.name, ts, got, c.want)
		}
		if got && day != "2026-06-28" {
			t.Errorf("%s: dayIn(%q) day = %q, want 2026-06-28", c.name, ts, day)
		}
	}
	if _, got := dayIn("not a time", from, to); got {
		t.Error("dayIn(unparsable) = true, want false")
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

	// Tokens is the billing volume: input + cache writes + cache reads + output
	// per message (#234), with the same breakdown as session.tokens.
	got := mustClaudeTotals(t, root, now, noPrices)
	if want := int64((100 + 5000 + 30000 + 400) + (2 + 673 + 39451 + 481)); got.Tokens != want {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got.Tokens, want)
	}
	if got.Input != (100+5000+30000)+(2+673+39451) || got.CachedInput != 30000+39451 || got.Output != 400+481 {
		t.Errorf("ClaudeTotals breakdown = in %d / cached %d / out %d, want %d / %d / %d",
			got.Input, got.CachedInput, got.Output, (100+5000+30000)+(2+673+39451), 30000+39451, 400+481)
	}

	// With a price for the model, cost is summed across today's messages.
	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	cost := mustClaudeTotals(t, root, now, prices).Cost
	// msg1: (100*15 + 5000*18.75 + 30000*1.5 + 400*75)/1e6
	// msg2: (2*15 + 673*18.75 + 39451*1.5 + 481*75)/1e6
	want := (100*15.0+5000*18.75+30000*1.5+400*75)/1e6 +
		(2*15.0+673*18.75+39451*1.5+481*75)/1e6
	if diff := cost - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, want)
	}
}

func TestClaudeTotalsUsesClaudeConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	now := time.Now()

	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"),
		claudeMsg(now, 10, 20, 100, 5)+"\n", now)

	if got := mustClaudeTotals(t, "", now, noPrices).Tokens; got != 10+20+100+5 {
		t.Errorf("mustClaudeTotals(t, empty root).Tokens = %d, want %d from CLAUDE_CONFIG_DIR", got, 10+20+100+5)
	}
}

func TestClaudeCostWithCacheCreationTTL(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"),
		claudeMsgCacheCreation(now, 100, 200, 300, 1000, 10)+"\n", now)

	prices := pricing.Table{"claude-fable": {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5}}
	got := mustClaudeTotals(t, root, now, prices)
	if got.Tokens != 100+200+300+1000+10 {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got.Tokens, 100+200+300+1000+10)
	}
	// API estimate: 5m cache write uses cache_write, 1h cache write uses 2x input,
	// cache read uses the API cache-read price.
	wantCost := (100*10.0 + 200*12.5 + 300*20.0 + 1000*1.0 + 10*50.0) / 1e6
	if diff := got.Cost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", got.Cost, wantCost)
	}
}

func TestClaudeCostClampsInconsistentCacheCreationTTL(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"),
		claudeMsgCacheCreationTotal(now, 100, 250, 200, 300, 1000, 10)+"\n", now)

	prices := pricing.Table{"claude-fable": {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5}}
	got := mustClaudeTotals(t, root, now, prices)
	if got.Tokens != 100+250+1000+10 {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got.Tokens, 100+250+1000+10)
	}
	// The TTL split exceeds the top-level total, so the authoritative total is
	// counted once as unknown cache creation instead of over-counting the split.
	wantCost := (100*10.0 + 250*12.5 + 1000*1.0 + 10*50.0) / 1e6
	if diff := got.Cost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", got.Cost, wantCost)
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

	if got, want := ClaudeSessionToday(path, now, noPrices).Tokens, int64((100+5000+30000+400)+(2+673+39451+481)); got != want {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d", got, want)
	}
	// Empty path and missing file are both zero, not a crash.
	if got := ClaudeSessionToday("", now, noPrices); got.Tokens != 0 {
		t.Errorf("empty path = %+v, want zero", got)
	}
	if got := ClaudeSessionToday(filepath.Join(root, "nope.jsonl"), now, noPrices); got.Tokens != 0 {
		t.Errorf("missing file = %+v, want zero", got)
	}
}

func TestClaudeTotalsIncludesSubagentsAndWorkflows(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath,
		claudeMsg(now, 10, 20, 100, 5)+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "agent-a.jsonl"),
		claudeMsg(now, 1, 2, 50, 3)+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_123", "agent-b.jsonl"),
		claudeMsg(now, 4, 5, 60, 6)+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_123", "journal.jsonl"),
		`{"timestamp":"`+now.Format(time.RFC3339)+`","event":"started"}`+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl"),
		claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday)

	// main 10+20+100+5=135, subagent 1+2+50+3=56, workflow 4+5+60+6=75.
	if got := mustClaudeTotals(t, root, now, noPrices).Tokens; got != int64(135+56+75) {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d (main + subagent + workflow)", got, 135+56+75)
	}

	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	wantCost := (10*15.0+20*18.75+100*1.5+5*75)/1e6 +
		(1*15.0+2*18.75+50*1.5+3*75)/1e6 +
		(4*15.0+5*18.75+60*1.5+6*75)/1e6
	if cost := mustClaudeTotals(t, root, now, prices).Cost; cost-wantCost > 1e-9 || cost-wantCost < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, wantCost)
	}
	if got := ClaudeSessionToday(mainPath, now, noPrices).Tokens; got != int64(135+56+75) {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d (main + subagent + workflow)", got, 135+56+75)
	}
}

// A single response is written once per content block, each line repeating the
// same usage; it must be counted once. The same response duplicated across
// files (resume/compaction copies prior turns forward) must not double-count.
func TestClaudeDedup(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// Response A as 3 content-block lines (identical usage) + distinct B once.
	// A=10+20+100+5=135, B=1+2+50+3=56.
	a := claudeMsgID(now, "msg_a", "req_a", 10, 20, 100, 5)
	b := claudeMsgID(now, "msg_b", "req_b", 1, 2, 50, 3)
	writeFile(t, filepath.Join(root, "projects", "p", "s1.jsonl"),
		a+"\n"+a+"\n"+a+"\n"+b+"\n", now)
	// A second file re-includes response A (resume copies the prior turn).
	writeFile(t, filepath.Join(root, "projects", "p", "s2.jsonl"), a+"\n", now)

	if got := mustClaudeTotals(t, root, now, noPrices).Tokens; got != int64(135+56) {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d (A once + B once)", got, 135+56)
	}

	// Cost dedups identically: A + B priced once each.
	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	wantCost := (10*15.0+20*18.75+100*1.5+5*75)/1e6 + (1*15.0+2*18.75+50*1.5+3*75)/1e6
	if cost := mustClaudeTotals(t, root, now, prices).Cost; cost-wantCost > 1e-9 || cost-wantCost < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, wantCost)
	}

	// ClaudeSessionToday dedups within the file: A counted once.
	if got := ClaudeSessionToday(filepath.Join(root, "projects", "p", "s1.jsonl"), now, noPrices).Tokens; got != int64(135+56) {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d", got, 135+56)
	}
}

func codexSession(total int64) string {
	return fmt.Sprintf(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`+"\n"+
		`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d}}}}`+"\n", total, total)
}

// Codex tokens are the growth of the cumulative total as reported — cached
// input included — with the same breakdown as session.tokens (#234); the cost
// still prices cached input at the cache-read rate.
func TestCodexTotalsCountCachedInput(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	session := `{"type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n" +
		`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200,"total_tokens":1200}}}}` + "\n"
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), session, now)

	got := mustCodexTotals(t, root, now, pricing.Table{"gpt-5.5": {In: 2, Out: 8, CacheRead: 0.5}})
	if got.Tokens != 1200 || got.Input != 1000 || got.CachedInput != 600 || got.Output != 200 {
		t.Errorf("CodexTotals = %+v, want tokens 1200 / in 1000 / cached 600 / out 200", got)
	}
	if want := (400*2.0 + 600*0.5 + 200*8.0) / 1e6; got.Cost != want {
		t.Errorf("CodexTotals.Cost = %v, want %v", got.Cost, want)
	}
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

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 1500 {
		t.Errorf("CodexTotals.Tokens = %d, want 1500", got)
	}
	// input=1000/500 all non-cached, priced at In=2/Mtok → (1000+500)*2/1e6.
	prices := pricing.Table{"gpt-5": {In: 2}}
	if cost := mustCodexTotals(t, root, now, prices).Cost; cost != 1500*2.0/1e6 {
		t.Errorf("CodexTotals.Cost = %v, want %v", cost, 1500*2.0/1e6)
	}
}

func TestCodexTotalsUsesCodexHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	now := time.Now()

	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), codexSession(1000), now)

	if got := mustCodexTotals(t, "", now, noPrices).Tokens; got != 1000 {
		t.Errorf("mustCodexTotals(t, empty root).Tokens = %d, want 1000 from CODEX_HOME", got)
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

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != int64(1500+500) {
		t.Errorf("CodexTotals.Tokens = %d, want %d (resumed session counted once at max cumulative + distinct)", got, 1500+500)
	}
}

func TestEmptyRoots(t *testing.T) {
	if got := mustClaudeTotals(t, t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("mustClaudeTotals(t, empty) = %+v", got)
	}
	if got := mustCodexTotals(t, t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("mustCodexTotals(t, empty) = %+v", got)
	}
}

// mustClaudeTotals / mustCodexTotals unwrap the error for the happy-path
// tests, which all operate on readable roots.
func mustClaudeTotals(t *testing.T, root string, now time.Time, prices pricing.Table) Totals {
	t.Helper()
	tot, err := ClaudeTotals(root, now, prices)
	if err != nil {
		t.Fatalf("ClaudeTotals(%q) error: %v", root, err)
	}
	return tot
}

func mustCodexTotals(t *testing.T, root string, now time.Time, prices pricing.Table) Totals {
	t.Helper()
	tot, err := CodexTotals(root, now, prices)
	if err != nil {
		t.Fatalf("CodexTotals(%q) error: %v", root, err)
	}
	return tot
}

func TestClaudeTotalsZeroWhenProjectsMissing(t *testing.T) {
	now := time.Now()
	tot, err := ClaudeTotals(t.TempDir(), now, noPrices)
	if err != nil {
		t.Fatalf("ClaudeTotals error: %v (a missing projects dir is a real zero, not unknown)", err)
	}
	if tot.Tokens != 0 {
		t.Errorf("Tokens = %d, want 0", tot.Tokens)
	}
}

func TestClaudeTotalsErrorWhenProjectsUnreadable(t *testing.T) {
	root := t.TempDir()
	// projects as a regular file makes os.ReadDir fail with a non-NotExist
	// error on every platform — the "unknown, keep daily null" case.
	if err := os.WriteFile(filepath.Join(root, "projects"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaudeTotals(root, time.Now(), noPrices); err == nil {
		t.Fatal("ClaudeTotals error = nil, want error for unreadable projects dir")
	}
}

func TestCodexTotalsErrorWhenDayDirUnreadable(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	monthDir := filepath.Join(root, "sessions", now.Format("2006"), now.Format("01"))
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Today's day directory as a regular file: non-NotExist ReadDir error.
	if err := os.WriteFile(filepath.Join(monthDir, now.Format("02")), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexTotals(root, now, noPrices); err == nil {
		t.Fatal("CodexTotals error = nil, want error for unreadable day dir")
	}
}

// A transcript that is listed but can't be read must make the whole total
// unknown (error), not silently smaller (#187). A dangling symlink reproduces
// the read failure portably without chmod (which is a no-op as root).
func TestClaudeTotalsErrorWhenTranscriptUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "projects", "p", "ok.jsonl"),
		claudeMsg(now, 10, 0, 0, 0)+"\n", now)
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "projects", "p", "broken.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaudeTotals(root, now, noPrices); err == nil {
		t.Fatal("ClaudeTotals error = nil, want error when one transcript can't be read")
	}
}

// Same contract on the Codex side: one unreadable rollout in a scanned day
// directory means the total is unknown, not partial (#187).
func TestCodexTotalsErrorWhenRolloutUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "ok.jsonl"), codexSession(1000), now)
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(dayDir, "broken.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexTotals(root, now, noPrices); err == nil {
		t.Fatal("CodexTotals error = nil, want error when one rollout can't be read")
	}
}
