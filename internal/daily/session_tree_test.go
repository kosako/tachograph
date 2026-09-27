package daily

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// ClaudeSessionTree counts one session across its main transcript and the
// subagent / workflow transcripts nested under the same-named directory, in
// one pass with one dedup set: the cumulative tokens (session.tokens, #262)
// span every day and every file of the tree, today's portion only today's
// lines. A response repeated across files counts once.
func TestClaudeSessionTree(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	shared := claudeMsgID(now, "msg_shared", "req_shared", 10, 20, 100, 5) // 135, main and a subagent

	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath,
		claudeMsg(yesterday, 10, 10, 10, 10)+"\n"+shared+"\n", now) // 40 yesterday + 135 today
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "agent-a.jsonl"),
		shared+"\n"+claudeMsg(now, 1, 2, 50, 3)+"\n", now) // dup 135 + 56 today
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_1", "agent-b.jsonl"),
		claudeMsg(now, 4, 5, 60, 6)+"\n", now) // 75 today
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_1", "journal.jsonl"),
		`{"timestamp":"`+now.Format(time.RFC3339)+`","event":"started"}`+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl"),
		claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday) // 4000, yesterday's file

	cum, today, ok := ClaudeSessionTree(mainPath, now, noPrices, nil)
	if !ok {
		t.Fatal("ok = false, want true (the main transcript has usage)")
	}
	if want := int64(40 + 135 + 56 + 75 + 4000); cum.Total != want {
		t.Errorf("cumulative Total = %d, want %d (every day, every file, the shared response once)", cum.Total, want)
	}
	if cum.Input+cum.Output != cum.Total {
		t.Errorf("cumulative Input+Output = %d, want Total %d", cum.Input+cum.Output, cum.Total)
	}
	if want := int64(135 + 56 + 75); today.Tokens != want {
		t.Errorf("today Tokens = %d, want %d (today's lines only)", today.Tokens, want)
	}
	// The wrapper session_today uses agrees with the tree's today.
	if got := ClaudeSessionToday(mainPath, now, noPrices); got.Tokens != today.Tokens {
		t.Errorf("ClaudeSessionToday = %d, want the tree's today %d", got.Tokens, today.Tokens)
	}
}

// The cumulative figure is unknown (ok=false) when the main transcript can't
// be read or carries no usage — never a partial zero. Today's portion keeps
// session_today's contract: whatever the readable files hold.
func TestClaudeSessionTreeUnknownMain(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "agent-a.jsonl"),
		claudeMsg(now, 1, 2, 50, 3)+"\n", now)

	if _, today, ok := ClaudeSessionTree(mainPath, now, noPrices, nil); ok || today.Tokens != 56 {
		t.Errorf("missing main: ok = %v, today = %d, want ok=false and today 56 from the subagent", ok, today.Tokens)
	}
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, mainPath, `{"type":"user","timestamp":"`+now.Format(time.RFC3339)+`"}`+"\n", now)
	if _, _, ok := ClaudeSessionTree(mainPath, now, noPrices, nil); ok {
		t.Error("main without usage: ok = true, want false")
	}
	if _, _, ok := ClaudeSessionTree("", now, noPrices, nil); ok {
		t.Error("empty path: ok = true, want false")
	}
}

type memTreeCache struct {
	m    map[string]memTreeEntry
	puts int
}

type memTreeEntry struct {
	size  int64
	mtime time.Time
	tok   schema.Tokens
}

func (c *memTreeCache) Get(path string, size int64, mtime time.Time) (schema.Tokens, bool) {
	e, ok := c.m[path]
	if !ok || e.size != size || !e.mtime.Equal(mtime) {
		return schema.Tokens{}, false
	}
	return e.tok, true
}

func (c *memTreeCache) Put(path string, size int64, mtime time.Time, tok schema.Tokens) {
	c.m[path] = memTreeEntry{size, mtime, tok}
	c.puts++
}

// A nested transcript last written before today is read once and then served
// from the cache while its size and mtime are unchanged — the statusline no
// longer re-reads a long session's finished subagents on every call (#262).
// Today's files are always read (today's portion needs their lines).
func TestClaudeSessionTreeCachesOlderFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath, claudeMsg(now, 10, 20, 100, 5)+"\n", now) // 135
	old := filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl")
	writeFile(t, old, claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday) // 4000
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "new-agent.jsonl"),
		claudeMsg(now, 1, 2, 50, 3)+"\n", now) // 56

	fc := &memTreeCache{m: map[string]memTreeEntry{}}
	cum, today, _ := ClaudeSessionTree(mainPath, now, noPrices, fc)
	if cum.Total != 135+4000+56 || today.Tokens != 135+56 {
		t.Fatalf("first pass: cumulative %d / today %d, want %d / %d", cum.Total, today.Tokens, 135+4000+56, 135+56)
	}
	if fc.puts != 1 {
		t.Fatalf("puts = %d, want 1 (only yesterday's file is cached)", fc.puts)
	}

	// Rewrite the old file with same-size content and restore its mtime: an
	// unchanged size+mtime must be served from the cache, not re-read.
	writeFile(t, old, claudeMsg(yesterday, 2000, 1000, 1000, 1000)+"\n", yesterday) // same size, 5000 if re-read
	if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, fc); cum.Total != 135+4000+56 {
		t.Errorf("second pass: cumulative %d, want %d from the cache", cum.Total, 135+4000+56)
	}
	if fc.puts != 1 {
		t.Errorf("puts = %d after an unchanged second pass, want 1", fc.puts)
	}

	// A changed size invalidates the entry.
	writeFile(t, old, claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n"+claudeMsg(yesterday, 1, 1, 1, 1)+"\n", yesterday)
	if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, fc); cum.Total != 135+4000+4+56 {
		t.Errorf("after growth: cumulative %d, want %d (re-read)", cum.Total, 135+4000+4+56)
	}
}
