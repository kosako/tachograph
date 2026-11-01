package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateRun points every directory a command can read or write at temp
// dirs, so a command that wrongly went on can't touch the real ones.
func isolateRun(t *testing.T) (cfgDir, claudeDir string) {
	t.Helper()
	cfgDir, claudeDir = t.TempDir(), t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("SWIFTBAR_PLUGIN_PATH", "")
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	return cfgDir, claudeDir
}

// A command that takes no positional arguments stops with exit 2 and the
// argument it didn't expect, before doing anything: the flag package stops
// at the first non-flag, so `tacho daily 7 --days 1` used to show 30 days
// and `tacho setup claude --write unexpected` wrote settings.json, both with
// exit 0. `config statusline-preset` takes one name, or --list (#441).
func TestUnexpectedArgumentsExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{"extra"},
		{"-no-color", "extra"},
		{"status", "--json", "extra"},
		{"daily", "7", "--days", "1"},
		{"watch", "extra"},
		{"doctor", "extra"},
		{"setup", "claude", "--write", "unexpected"},
		{"setup", "swiftbar", "--write", "unexpected"},
		{"config", "statusline-preset", "moon", "--list"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfgDir, claudeDir := isolateRun(t)
			var code int
			stderr := capture(t, &os.Stderr, func() {
				capture(t, &os.Stdout, func() { code = run(args) })
			})
			if code != 2 {
				t.Errorf("exit %d, want 2 (stderr %q)", code, stderr)
			}
			if !strings.Contains(stderr, "unexpected argument") && !strings.Contains(stderr, "usage:") {
				t.Errorf("stderr %q, want the unexpected argument or the usage", stderr)
			}
			for _, p := range []string{filepath.Join(claudeDir, "settings.json"), filepath.Join(cfgDir, "statusline.tmpl")} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("%s was written (stat err: %v)", filepath.Base(p), err)
				}
			}
		})
	}
}

// The commands another program runs (Claude Code's statusLine, SwiftBar's
// plugin) warn on stderr and go on, so a hand-edited command line with an
// extra word doesn't blank the status line or the menu bar (#441).
func TestUnexpectedArgumentsWarnWhereAnotherProgramRuns(t *testing.T) {
	stubSystemDark(t, false)
	for _, c := range []struct {
		name string
		run  func() int
	}{
		{"statusline", func() int {
			return runStatuslineWithIO([]string{"--no-color", "extra"}, strings.NewReader(`{}`), &strings.Builder{}, time.Now())
		}},
		{"swiftbar", func() int { return runSwiftbar([]string{"extra"}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolateRun(t)
			var code int
			stderr := capture(t, &os.Stderr, func() {
				capture(t, &os.Stdout, func() { code = c.run() })
			})
			if code != 0 || !strings.Contains(stderr, `ignoring unexpected argument "extra"`) {
				t.Errorf("exit %d, stderr %q; want 0 and the warning", code, stderr)
			}
		})
	}
}
