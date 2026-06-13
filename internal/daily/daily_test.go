package daily

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

	got := ClaudeTokens(root, now)
	if want := int64(5500 + 1156); got != want {
		t.Errorf("ClaudeTokens = %d, want %d", got, want)
	}
}

func codexSession(total int64) string {
	return fmt.Sprintf(`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d}}}}`+"\n", total)
}

func TestCodexTokens(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), codexSession(1000), now)
	writeFile(t, filepath.Join(dayDir, "s2.jsonl"), codexSession(500), now)

	// Yesterday's folder must be ignored.
	y := now.Add(-24 * time.Hour)
	yDir := filepath.Join(root, "sessions", y.Local().Format("2006"), y.Local().Format("01"), y.Local().Format("02"))
	writeFile(t, filepath.Join(yDir, "old.jsonl"), codexSession(9999), y)

	if got := CodexTokens(root, now); got != 1500 {
		t.Errorf("CodexTokens = %d, want 1500", got)
	}
}

func TestEmptyRoots(t *testing.T) {
	if got := ClaudeTokens(t.TempDir(), time.Now()); got != 0 {
		t.Errorf("ClaudeTokens(empty) = %d", got)
	}
	if got := CodexTokens(t.TempDir(), time.Now()); got != 0 {
		t.Errorf("CodexTokens(empty) = %d", got)
	}
}
