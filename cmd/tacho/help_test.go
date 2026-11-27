package main

import (
	"os"
	"strings"
	"testing"
)

// -h / --help / help print the command list on stdout and succeed; they used
// to print only the one-shot flags (-h) or fail as an unknown command (help)
// (#269).
func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{
		{"-h"}, {"--h"}, {"-help"}, {"--help"}, {"help"}, {"doctor", "-h"},
		// After other one-shot flags, where the FlagSet used to answer.
		{"-no-color", "-h"}, {"--no-cache", "--help"}, {"-no-color", "-no-cache", "-help"},
		// help doesn't check what follows it; the usage lists daily too (#502).
		{"help", "daily"}, {"help", "extra"},
	} {
		var code int
		out := capture(t, &os.Stdout, func() { code = run(args) })
		if code != 0 {
			t.Errorf("run(%q) = %d, want 0", args, code)
		}
		if out != usage {
			t.Errorf("run(%q) stdout = %q, want the usage", args, out)
		}
	}
}

func TestRunVersionFlag(t *testing.T) {
	// A leading --version answers before anything else, as it always did,
	// and like -h it doesn't check what follows (#502); the version command
	// does (TestUnexpectedArgumentsExitTwo).
	for _, args := range [][]string{{"--version"}, {"-version"}, {"--version", "-h"}, {"--version", "extra"}, {"-version", "extra"}} {
		var code int
		out := capture(t, &os.Stdout, func() { code = run(args) })
		if code != 0 || !strings.HasPrefix(out, "tacho ") {
			t.Errorf("run(%q) = %d, stdout %q; want 0 and the version", args, code, out)
		}
	}
}

// Help is decided by the one-shot flag parsing itself: an invalid flag
// before -h is still an error (stderr, exit 2), as it was with
// flag.ExitOnError.
func TestRunFlagErrorBeforeHelp(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus", "-h"}, "flag provided but not defined: -bogus"},
		{[]string{"--no-cache=invalid", "--help"}, "invalid boolean value"},
	} {
		var code int
		var stdout string
		stderr := capture(t, &os.Stderr, func() {
			stdout = capture(t, &os.Stdout, func() { code = run(c.args) })
		})
		if code != 2 || stdout != "" || !strings.Contains(stderr, c.want) || !strings.Contains(stderr, "-no-cache") {
			t.Errorf("run(%q) = %d, stdout %q, stderr %q; want 2 and %q with the flag list on stderr", c.args, code, stdout, stderr, c.want)
		}
	}
}

// A subcommand's flag errors come back from run with the exit code
// flag.ExitOnError used to end the process with: 2 for a bad flag, 0 for -h,
// both with the flag package's message and flag list on stderr and nothing
// on stdout. A command that still ended the process here would end the test
// binary instead (#494).
func TestSubcommandFlagErrors(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
		want []string // on stderr
	}{
		{[]string{"swiftbar", "--bogus"}, 2, []string{"flag provided but not defined: -bogus", "Usage of swiftbar:", "-png"}},
		{[]string{"swiftbar", "-h"}, 0, []string{"Usage of swiftbar:", "-png"}},
		{[]string{"daily", "--days=x"}, 2, []string{`invalid value "x" for flag -days`, "Usage of daily:"}},
		{[]string{"daily", "-h"}, 0, []string{"Usage of daily:", "-days"}},
		{[]string{"watch", "--bogus"}, 2, []string{"flag provided but not defined: -bogus", "Usage of watch:"}},
		{[]string{"watch", "-help"}, 0, []string{"Usage of watch:", "-n int"}},
		{[]string{"statusline", "-template"}, 2, []string{"flag needs an argument: -template", "Usage of statusline:"}},
		{[]string{"statusline", "-h"}, 0, []string{"Usage of statusline:", "-template"}},
		{[]string{"status", "--json=x"}, 2, []string{`invalid boolean value "x" for -json`, "Usage of status:"}},
		{[]string{"status", "-h"}, 0, []string{"Usage of status:", "-json"}},
		{[]string{"setup", "claude", "--bogus"}, 2, []string{"flag provided but not defined: -bogus", "Usage of setup:"}},
		{[]string{"setup", "claude", "-h"}, 0, []string{"Usage of setup:", "-write"}},
		{[]string{"setup", "swiftbar", "--write=x"}, 2, []string{`invalid boolean value "x" for -write`, "Usage of setup swiftbar:"}},
		{[]string{"setup", "swiftbar", "-h"}, 0, []string{"Usage of setup swiftbar:", "-write"}},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			isolateRun(t)
			var code int
			var stdout string
			stderr := capture(t, &os.Stderr, func() {
				stdout = capture(t, &os.Stdout, func() { code = run(c.args) })
			})
			if code != c.code || stdout != "" {
				t.Errorf("run(%q) = %d, stdout %q; want %d and no stdout", c.args, code, stdout, c.code)
			}
			for _, w := range c.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("run(%q) stderr %q, want %q", c.args, stderr, w)
				}
			}
		})
	}
}

// An unknown command still prints the usage on stderr and exits 2.
func TestRunUnknownCommand(t *testing.T) {
	var code int
	out := capture(t, &os.Stderr, func() { code = run([]string{"bogus"}) })
	if code != 2 || out != usage {
		t.Errorf("run(bogus) = %d, stderr %q; want 2 and the usage", code, out)
	}
}

// The usage lists the commands `tacho config`'s own usage has, and the
// version / help spellings.
func TestUsageListsCommands(t *testing.T) {
	for _, want := range []string{"tacho config path", "tacho config statusline-preset", "--version", "tacho help", "-no-color", "-no-cache"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage missing %q", want)
		}
	}
}
