package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// A write whose rename fails leaves no temp file behind, on every system
// (#371): here a directory takes the cache file's place, which no rename can
// replace.
func TestWriteJSONRemovesTempWhenRenameFails(t *testing.T) {
	dir := setCacheDir(t)
	if err := os.Mkdir(filepath.Join(dir, "blocked.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON("blocked.json", map[string]int{"a": 1}); err == nil {
		t.Fatal("WriteJSON over a directory succeeded")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("leftover temp files after a failed write: %v", leftovers)
	}
}

// Every cache file is owner-only (0600), new or replacing a looser one: they
// hold session cwds, transcript paths, and usage that other local users must
// not read. The notification record goes through WriteJSON.
func TestCacheFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permission bits on Windows, only the read-only attribute")
	}
	dir := setCacheDir(t)
	now := time.Now()
	at := now.Format(time.RFC3339)
	tree := OpenSessionTree("/p/session-a")
	for _, c := range []struct {
		file  string
		write func() error
	}{
		{"status.json", func() error {
			return WriteStatus(&schema.Status{SchemaVersion: schema.Version, GeneratedAt: at}, testRoots)
		}},
		{"snapshot-" + schema.ToolClaudeCode + ".json", func() error {
			return WriteSnapshot(schema.Tool{Tool: schema.ToolClaudeCode, CollectedAt: &at}, time.Time{}, snapRoot)
		}},
		{"daily-history.json", func() error { return WriteDailyHistory("key", testRoots, nil) }},
		{tree.name, func() error {
			tree.Put("/p/session-a/sub.jsonl", 1, now, []byte{1}) // Save writes only a changed cache
			return tree.Save(now)
		}},
		{"notify-state.json", func() error { return WriteJSON("notify-state.json", map[string]int{"a": 1}) }},
	} {
		path := filepath.Join(dir, c.file)
		for _, when := range []string{"new", "replacing a 0644 file"} {
			if err := c.write(); err != nil {
				t.Fatalf("%s (%s): %v", c.file, when, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Errorf("%s (%s): mode %v, want 0600", c.file, when, got)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
