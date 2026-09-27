package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// capture runs fn with *target (os.Stdout or os.Stderr) redirected and
// returns what was written. The pipe is drained concurrently so output larger
// than its buffer can't block fn, and both ends are always closed.
func capture(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	orig := *target
	*target = w
	func() {
		defer func() {
			*target = orig
			w.Close()
		}()
		fn()
	}()
	return <-out
}

// `tacho cmux` says the integration is deprecated (#273) before anything
// else — here with a bad subcommand, so no cmux CLI is ever invoked — and
// keeps its exit code.
func TestRunCmuxShowsDeprecation(t *testing.T) {
	var code int
	out := capture(t, &os.Stderr, func() { code = runCmux([]string{"bogus"}) })
	if code != 2 {
		t.Errorf("exit = %d, want 2 (usage error unchanged)", code)
	}
	if !strings.Contains(out, "deprecated") || !strings.Contains(out, "#273") {
		t.Errorf("stderr = %q, want the deprecation notice", out)
	}
}

// doctor's integrations section flags cmux as deprecated (#273).
func TestReportIntegrationsShowsCmuxDeprecation(t *testing.T) {
	t.Setenv("CMUX_WORKSPACE_ID", "")
	isolateSwiftBar(t)
	out := capture(t, &os.Stdout, reportIntegrations)
	if !strings.Contains(out, "cmux:       deprecated") {
		t.Errorf("integrations = %q, want the cmux deprecation line", out)
	}
}
