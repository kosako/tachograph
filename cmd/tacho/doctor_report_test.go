package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// doctor's data-source lines tell a missing tree, an empty one, an
// unreadable one, and one with logs apart (#445).
func TestReportJSONLTree(t *testing.T) {
	now := time.Now()
	present := t.TempDir()
	for i, name := range []string{"a.jsonl", "b.jsonl", "notes.txt"} {
		p := filepath.Join(present, "p", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := now.Add(-time.Duration(10-i) * time.Minute)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, want string
	}{
		{"missing", filepath.Join(t.TempDir(), "none"), "missing ("},
		{"empty", t.TempDir(), "but no .jsonl files — the hint"},
		{"present", present, "present (2 .jsonl, newest "},
	}
	// Under a regular file the path is ENOTDIR on Unix; Windows reports it
	// as not found, which is the missing case.
	if runtime.GOOS != "windows" {
		cases = append(cases, struct{ name, path, want string }{"unreadable", filepath.Join(file, "sub"), "unreadable ("})
		// A tree linked into place counts the files where it leads, as the
		// collectors read them (#440).
		linked := filepath.Join(t.TempDir(), "projects")
		if err := os.Symlink(present, linked); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ name, path, want string }{"linked", linked, "present (2 .jsonl, newest "})
		// A link to a log file where the tree should be is no tree: the
		// collectors can't list it.
		linkedFile := filepath.Join(t.TempDir(), "sessions")
		if err := os.Symlink(filepath.Join(present, "p", "a.jsonl"), linkedFile); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ name, path, want string }{"linked to a file", linkedFile, "unreadable ("})
	}
	cases = append(cases, struct{ name, path, want string }{"a file", file, "unreadable ("})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := capture(t, &os.Stdout, func() { reportJSONLTree("Logs", c.path, now, "the hint") })
			if !strings.HasPrefix(out, "  Logs:") || !strings.Contains(out, c.want) {
				t.Errorf("reportJSONLTree(%s) = %q, want a Logs line with %q", c.name, out, c.want)
			}
		})
	}
}

// doctor's current-status lines tell a tool that isn't found, one that
// errors, one that is ok, and one whose reading is stale apart (#445).
func TestReportToolStatus(t *testing.T) {
	now := time.Now()
	old := now.Add(-3 * time.Hour).Format(time.RFC3339)
	for _, c := range []struct {
		name string
		tool schema.Tool
		want string
	}{
		{"not found", schema.Tool{Tool: schema.ToolCodex}, "Codex:   not found — "},
		{"error", schema.Tool{Tool: schema.ToolClaudeCode, Available: true, Error: &schema.Error{Code: "no_usage"}}, "Claude:  error no_usage — "},
		{"ok", schema.Tool{Tool: schema.ToolCodex, Available: true}, "Codex:   ok\n"},
		{"stale", schema.Tool{Tool: schema.ToolClaudeCode, Available: true, Stale: true, CollectedAt: &old}, "Claude:  ok, but stale ("},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := capture(t, &os.Stdout, func() { reportToolStatus(c.tool, now) })
			if !strings.HasPrefix(out, "  "+c.want) {
				t.Errorf("reportToolStatus(%s) = %q, want it to start with %q", c.name, out, "  "+c.want)
			}
		})
	}
}

// A whole doctor run, isolated from the machine (data, config, cache,
// SwiftBar, PATH, Go), reports every section and the data sources it was
// pointed at (#445).
func TestRunDoctorReportsEverySection(t *testing.T) {
	isolateSwiftBar(t)
	claudeRoot := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	if err := os.MkdirAll(filepath.Join(claudeRoot, "projects", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeRoot, "projects", "p", "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var code int
	out := capture(t, &os.Stdout, func() { code = runDoctor(nil) })
	if code != 0 {
		t.Errorf("runDoctor = %d, want 0", code)
	}
	for _, want := range []string{
		"tachograph doctor\n",
		"\nbinary:\n",
		"\nconfig (",
		"\ndata sources:\n",
		"Claude projects: present (1 .jsonl, newest ",
		"Codex sessions:  missing (",
		"\ncache:\n",
		"\nintegrations:\n",
		"\ncurrent status:\n",
		"Codex:   not found — ",
		"\nClaude Code statusLine (",
		"not configured — run `tacho setup claude --write`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
}

// doctor's statusLine section tells a settings.json that can't be read (here
// a directory) from one that isn't there: the former names the error and
// asks for the file to be fixed, since `tacho setup claude --write` would fail
// on the same read. Without a home directory there is no path to report
// (#440).
func TestReportClaudeStatusLine(t *testing.T) {
	dir := t.TempDir()
	notThere := filepath.Join(dir, "settings.json")
	aDir := filepath.Join(dir, "settings-dir.json")
	if err := os.Mkdir(aDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, path, want, not string }{
		{"missing", notThere, "not configured — run `tacho setup claude --write`", "can't be read"},
		{"unreadable", aDir, "settings.json can't be read: ", "not configured"},
		{"no home", "", "unavailable — cannot locate the home directory", "not configured"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := capture(t, &os.Stdout, func() { reportClaudeStatusLine(c.path, "/usr/local/bin/tacho") })
			if !strings.Contains(out, c.want) || strings.Contains(out, c.not) {
				t.Errorf("reportClaudeStatusLine(%s) = %q, want %q and not %q", c.name, out, c.want, c.not)
			}
		})
	}
}
