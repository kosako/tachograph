package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/schema"
)

// status.json is judged by its modification time against the TTL.
func TestReportCacheFile(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "status.json")
	report := func() string {
		return capture(t, &os.Stdout, func() {
			reportCacheFile("status.json", path, cache.StatusTTL, now, "run `tacho` once")
		})
	}
	if out := report(); !strings.Contains(out, "status.json:     missing — run `tacho` once") {
		t.Errorf("missing file:\n%s", out)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		age  time.Duration
		want string
	}{
		{10 * time.Second, "present (modified 10s ago, fresh)"},
		{cache.StatusTTL + time.Second, "present (modified 31s ago, expired)"},
	} {
		if err := os.Chtimes(path, now.Add(-c.age), now.Add(-c.age)); err != nil {
			t.Fatal(err)
		}
		if out := report(); !strings.Contains(out, c.want) {
			t.Errorf("modified %v ago:\n%s\nwant %q", c.age, out, c.want)
		}
	}
}

// doctor reports the Claude snapshot as the readers judge it (#379): one
// written for another config dir or schema version is present but not used,
// though its file was just modified, and the age is the snapshot's own
// collected_at, not the file's modification time.
func TestReportSnapshot(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	write := func(root string, age time.Duration) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			collected := now.Add(-age).Format(time.RFC3339)
			tool := schema.Tool{Tool: schema.ToolClaudeCode, Available: true, CollectedAt: &collected}
			if err := cache.WriteSnapshot(tool, time.Time{}, root); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw := func(body string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	this := func() string { return core.Roots(core.Options{})[schema.ToolClaudeCode] }

	for _, c := range []struct {
		name string
		seed func(t *testing.T, dir string)
		want string
	}{
		{"missing", func(*testing.T, string) {}, "missing — run Claude Code with `tacho statusline` configured"},
		{"fresh", func(t *testing.T, dir string) { write(this(), 2*time.Minute)(t, dir) }, "present (collected 2m ago, fresh)"},
		{"stale", func(t *testing.T, dir string) { write(this(), 2*time.Hour)(t, dir) }, "present (collected 2h ago, stale but usable)"},
		{"expired", func(t *testing.T, dir string) { write(this(), cache.SnapshotMaxAge+24*time.Hour)(t, dir) }, "present (collected 31d ago, expired)"},
		{"other root", write(filepath.Join(t.TempDir(), "other-profile"), 2*time.Minute),
			"present but not used — observed from another Claude config dir ("},
		{"other schema", raw(`{"schema_version":"9.9","collected_at":"` + now.Format(time.RFC3339) + `"}`),
			`present but not used — written for schema "9.9", this tacho reads ` + schema.Version},
		{"not JSON", raw(`{`), "present but not used — not a valid snapshot"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TACHO_CACHE_DIR", dir)
			isolateClaudeRoot(t)
			c.seed(t, dir)
			out := capture(t, &os.Stdout, func() { reportSnapshot(now) })
			if !strings.HasPrefix(out, "  Claude snapshot: ") || !strings.Contains(out, c.want) {
				t.Errorf("reportSnapshot =\n%s\nwant a Claude snapshot line with %q", out, c.want)
			}
		})
	}
}
