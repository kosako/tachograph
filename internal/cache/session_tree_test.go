package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// The session-tree cache round-trips through its file, misses on a changed
// size or mtime, and starts empty for another session tree (#262).
func TestSessionTreeCacheRoundTrip(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	now := time.Now()
	mtime := now.Add(-48 * time.Hour).Truncate(time.Second)
	tok := schema.Tokens{Input: 40, CachedInput: 10, Output: 2, Total: 42}

	c := OpenSessionTree("/p/session-a")
	if _, ok := c.Get("/p/session-a/sub.jsonl", 100, mtime); ok {
		t.Fatal("a fresh cache must miss")
	}
	c.Put("/p/session-a/sub.jsonl", 100, mtime, tok)
	if err := c.Save(now); err != nil {
		t.Fatal(err)
	}

	c = OpenSessionTree("/p/session-a")
	if got, ok := c.Get("/p/session-a/sub.jsonl", 100, mtime); !ok || got != tok {
		t.Errorf("reopened Get = %+v, %v, want %+v", got, ok, tok)
	}
	if _, ok := c.Get("/p/session-a/sub.jsonl", 101, mtime); ok {
		t.Error("a changed size must miss")
	}
	if _, ok := c.Get("/p/session-a/sub.jsonl", 100, mtime.Add(time.Second)); ok {
		t.Error("a changed mtime must miss")
	}
	if _, ok := OpenSessionTree("/p/session-b").Get("/p/session-a/sub.jsonl", 100, mtime); ok {
		t.Error("another session tree must not see session-a's entries")
	}
}

// Saving removes other session trees' cache files unused for 30 days.
func TestSessionTreeCachePrunesOldFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", dir)
	now := time.Now()
	stale := filepath.Join(dir, "session-tree-0000000000000000.json")
	if err := os.WriteFile(stale, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	c := OpenSessionTree("/p/session-a")
	c.Put("/p/session-a/sub.jsonl", 1, now, schema.Tokens{Total: 1})
	if err := c.Save(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a session-tree cache file unused for 31 days should be removed")
	}
}
