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
	for _, arg := range []string{"--version", "-version"} {
		var code int
		out := capture(t, &os.Stdout, func() { code = run([]string{arg}) })
		if code != 0 || !strings.HasPrefix(out, "tacho ") {
			t.Errorf("run(%q) = %d, stdout %q; want 0 and the version", arg, code, out)
		}
	}
}

// Flag parsing stops at the first non-flag argument or "--", so a -h after
// them is not a help request.
func TestOneShotWantsHelp(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"-no-color", "-h"}, true},
		{[]string{"-no-color"}, false},
		{[]string{"--", "-h"}, false},
		{[]string{"extra", "-h"}, false},
		{nil, false},
	} {
		if got := oneShotWantsHelp(c.args); got != c.want {
			t.Errorf("oneShotWantsHelp(%q) = %v, want %v", c.args, got, c.want)
		}
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
