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

func TestEmptyRoots(t *testing.T) {
	if got := ClaudeTotals(t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("ClaudeTotals(empty) = %+v", got)
	}
	if got := CodexTotals(t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("CodexTotals(empty) = %+v", got)
	}
}
