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

// doctor reports status.json as the reader judges it, by the same checks
// (#446): a file just modified but not valid, written for another schema
// version, or assembled for other config dirs is present but not used, and
// one modified in the future past the slack isn't used either (#415). Each
// line says fresh exactly when ReadStatus serves the file.
func TestReportStatusCache(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", dir)
	path := filepath.Join(dir, "status.json")
	roots := core.Roots(core.Options{})
	report := func() string {
		return capture(t, &os.Stdout, func() { reportStatusCache(now) })
	}
	if out := report(); !strings.Contains(out, "status.json:     missing — run `tacho` or `tacho status --json` once") {
		t.Errorf("missing file:\n%s", out)
	}
	valid := func(t *testing.T) {
		if err := cache.WriteStatus(&schema.Status{SchemaVersion: schema.Version}, roots); err != nil {
			t.Fatal(err)
		}
	}
	raw := func(body string) func(t *testing.T) {
		return func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	otherRoots := func(t *testing.T) {
		other := map[string]string{schema.ToolClaudeCode: filepath.Join(t.TempDir(), "other-profile")}
		if err := cache.WriteStatus(&schema.Status{SchemaVersion: schema.Version}, other); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		write func(t *testing.T)
		age   time.Duration
		want  string
	}{
		{"fresh", valid, 10 * time.Second, "present (modified 10s ago, fresh)"},
		{"expired", valid, cache.StatusTTL + time.Second, "present (modified 31s ago, expired)"},
		{"future within the slack", valid, -time.Second, "present (modified in the future, fresh)"},
		{"future", valid, -15 * time.Minute, "present (modified in the future, not used)"},
		{"not JSON", raw("not json"), 10 * time.Second, "present (modified 10s ago) but not used — not valid JSON; the next `tacho` run rewrites it"},
		{"no schema version", raw("{}"), 10 * time.Second, "present (modified 10s ago) but not used — it records no schema version, this tacho reads " + schema.Version},
		{"another schema", raw(`{"schema_version":"0.1"}`), 10 * time.Second, "present (modified 10s ago) but not used — written for schema \"0.1\", this tacho reads " + schema.Version},
		{"other config dirs", otherRoots, 10 * time.Second, "present (modified 10s ago) but not used — assembled for other Claude Code / Codex config dirs"},
		{"no config dirs recorded", raw(`{"schema_version":"` + schema.Version + `"}`), 10 * time.Second, "present (modified 10s ago) but not used — it records no config dirs"},
	} {
		c.write(t)
		if err := os.Chtimes(path, now.Add(-c.age), now.Add(-c.age)); err != nil {
			t.Fatal(err)
		}
		out := report()
		if !strings.Contains(out, c.want) {
			t.Errorf("%s:\n%s\nwant %q", c.name, out, c.want)
		}
		_, served := cache.ReadStatus(cache.StatusTTL, now, roots)
		if fresh := strings.Contains(out, ", fresh)"); fresh != served {
			t.Errorf("%s: doctor says fresh %v, ReadStatus serves %v:\n%s", c.name, fresh, served, out)
		}
	}
}

// A run that resolves no config dir (no CLAUDE_CONFIG_DIR, CODEX_HOME, or
// home) matches no status cache, not even the one it just wrote: doctor says
// so rather than blaming another profile (#446).
func TestReportStatusCacheWithoutConfigDirs(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // the home os.UserHomeDir reads on Windows
	roots := core.Roots(core.Options{})
	if len(roots) != 0 {
		t.Fatalf("roots = %v, want none without a home", roots)
	}
	if err := cache.WriteStatus(&schema.Status{SchemaVersion: schema.Version}, roots); err != nil {
		t.Fatal(err)
	}
	out := capture(t, &os.Stdout, func() { reportStatusCache(now) })
	if want := "but not used — this run's Claude Code and Codex config dirs can't be resolved"; !strings.Contains(out, want) {
		t.Errorf("got:\n%s\nwant %q", out, want)
	}
	if _, served := cache.ReadStatus(cache.StatusTTL, now, roots); served {
		t.Error("ReadStatus served a status cache with no roots")
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
		{"from the future", nil, func(t *testing.T, dir string) { write(this(), -10*time.Minute)(t, dir) },
			"present but not used — collected in the future (" + now.Add(10*time.Minute).Local().Format(time.RFC3339) + "; the clock was set back?)"},
		{"other root", nil, write(other, 2*time.Minute),
			"present but not used — observed from another Claude config dir (" + otherStored + ", not {here})"},
		{"no root recorded", nil, write("", 2*time.Minute),
			"present but not used — it records no Claude config dir (written by a tacho before v0.9.0, or one that couldn't resolve it)"},
		{"this run has no root", noHome, write(other, 2*time.Minute),
			"present but not used — this run's Claude config dir can't be resolved"},
		{"other schema", nil, raw(`{"schema_version":"9.9","collected_at":"` + now.Format(time.RFC3339) + `"}`),
			`present but not used — written for schema "9.9", this tacho reads ` + schema.Version},
		{"not JSON", nil, raw(`{`), "present but not used — not a valid snapshot"},
		{"unreadable", nil, func(t *testing.T, dir string) {
			// A directory where the snapshot goes: there, but not readable as a file.
			if err := os.Mkdir(filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "unreadable: "},
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
