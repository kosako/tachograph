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
	other := filepath.Join(t.TempDir(), "other-profile")
	otherStored, _ := cache.NormalizeRoot(other)
	// noHome leaves this run without a Claude config dir: no CLAUDE_CONFIG_DIR
	// and no home directory to find ~/.claude in.
	noHome := func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "") // the home os.UserHomeDir reads on Windows
	}

	for _, c := range []struct {
		name string
		env  func(t *testing.T) // after the Claude root is isolated
		seed func(t *testing.T, dir string)
		want string // {here} stands for this run's root, as stored
	}{
		{"missing", nil, func(*testing.T, string) {}, "missing — run Claude Code with `tacho statusline` configured"},
		{"fresh", nil, func(t *testing.T, dir string) { write(this(), 2*time.Minute)(t, dir) }, "present (collected 2m ago, fresh)"},
		{"stale", nil, func(t *testing.T, dir string) { write(this(), 2*time.Hour)(t, dir) }, "present (collected 2h ago, stale but usable)"},
		{"expired", nil, func(t *testing.T, dir string) { write(this(), cache.SnapshotMaxAge+24*time.Hour)(t, dir) }, "present (collected 31d ago, expired)"},
		{"other root", nil, write(other, 2*time.Minute),
			"present but not used — observed from another Claude config dir (" + otherStored + ", not {here})"},
		{"no root recorded", nil, write("", 2*time.Minute),
			"present but not used — it records no Claude config dir (written by a tacho before v0.9.0, or one that couldn't resolve it)"},
		{"this run has no root", noHome, write(other, 2*time.Minute),
			"present but not used — this run's Claude config dir can't be resolved"},
		{"other schema", nil, raw(`{"schema_version":"9.9","collected_at":"` + now.Format(time.RFC3339) + `"}`),
			`present but not used — written for schema "9.9", this tacho reads ` + schema.Version},
		{"not JSON", nil, raw(`{`), "present but not used — not a valid snapshot"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TACHO_CACHE_DIR", dir)
			isolateClaudeRoot(t)
			if c.env != nil {
				c.env(t)
			}
			c.seed(t, dir)
			here, _ := cache.NormalizeRoot(this())
			want := strings.ReplaceAll(c.want, "{here}", here)
			out := capture(t, &os.Stdout, func() { reportSnapshot(now) })
			if !strings.HasPrefix(out, "  Claude snapshot: ") || !strings.Contains(out, want) {
				t.Errorf("reportSnapshot =\n%s\nwant a Claude snapshot line with %q", out, want)
			}
		})
	}
}
