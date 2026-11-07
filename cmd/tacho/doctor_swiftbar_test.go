package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kosako/tachograph/internal/setup"
)

// doctor's SwiftBar check reads the program the plugin's exec line runs
// (#482): this binary or its npm launcher, one that is missing or not
// executable, a different tacho, or a user's own program. The plugins are
// the one `tacho setup swiftbar` writes, also with a display setting added
// and with a path it quotes, and hand-written ones of the same shape.
func TestSwiftBarPluginWarning(t *testing.T) {
	pathTacho, binary := npmLayout(t) // skips on Windows
	home := t.TempDir()
	t.Setenv("HOME", home)
	write := func(path string, mode os.FileMode) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write(filepath.Join(t.TempDir(), "old node", "tacho"), 0o755) // setup quotes the space
	script := write(filepath.Join(t.TempDir(), "gauges.sh"), 0o755)
	unexecutable := write(filepath.Join(t.TempDir(), "tacho"), 0o644)
	missing := filepath.Join(t.TempDir(), "gone", "tacho")
	tweaked := strings.Replace(setup.SwiftBarPlugin(missing),
		"# export TACHO_APPEARANCE=light", "export TACHO_APPEARANCE=light", 1)
	if !strings.Contains(tweaked, "\nexport TACHO_APPEARANCE=light   # ") {
		t.Fatalf("the plugin no longer has the TACHO_APPEARANCE line to uncomment:\n%s", tweaked)
	}

	const gone = "missing or not executable — re-run `tacho setup swiftbar --write`"
	cases := []struct {
		name, script, warning string
	}{
		{"this binary", setup.SwiftBarPlugin(binary), ""},
		{"the npm launcher for this binary", setup.SwiftBarPlugin(pathTacho), ""},
		{"missing", setup.SwiftBarPlugin(missing), "runs " + missing + ", which is " + gone},
		{"not executable", setup.SwiftBarPlugin(unexecutable), "runs " + unexecutable + ", which is " + gone},
		{"another tacho at a quoted path", setup.SwiftBarPlugin(stale), "different tacho (" + stale + ")"},
		{"not tacho", setup.SwiftBarPlugin(script), ""},
		{"a display setting added", tweaked, "runs " + missing + ", which is " + gone},
		{"an export of names, quotes, and a ~", "#!/bin/bash\nexport A=~/bin:$X1:'b c'\"$PATH d\"\nexec " + missing + " swiftbar\n", gone},
		{"a ~/ path", "#!/bin/bash\nexec ~/gone/tacho swiftbar\n", "runs " + filepath.Join(home, "gone", "tacho") + ", which is " + gone},
		{"an env shebang", "#!/usr/bin/env bash\nexec " + missing + " swiftbar\n", gone},
		{"sh, indented, a comment, no final newline", "#!/bin/sh\n\n  exec " + missing + " swiftbar # gauges", gone},
	}
	for _, c := range cases {
		warning, note := swiftBarPluginWarning(c.script, binary)
		if (c.warning == "") != (warning == "") || !strings.Contains(warning, c.warning) {
			t.Errorf("%s: warning = %q, want containing %q", c.name, warning, c.warning)
		}
		if note != "" {
			t.Errorf("%s: note = %q, want none (the plugin is checkable)", c.name, note)
		}
	}
}

// A plugin whose program doctor can't tell from its text gets a note, not a
// warning (#482), as a statusLine does (#362). Each runs a missing program
// if misread, so a warning means doctor took the script for one it can
// check. The first case is the control: the same program in the plugin
// setup writes is checked.
func TestSwiftBarPluginWarningNotChecked(t *testing.T) {
	const gone = "/nonexistent-482/tacho"
	if warning, _ := swiftBarPluginWarning(setup.SwiftBarPlugin(gone), ""); !strings.Contains(warning, "missing or not executable") {
		t.Fatalf("control: setup's plugin for %s: warning = %q, want the missing one", gone, warning)
	}
	contrib, err := os.ReadFile(filepath.Join("..", "..", "contrib", "tacho.30s.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may turn the line endings to CRLF.
	contrib = []byte(strings.ReplaceAll(string(contrib), "\r\n", "\n"))
	const unknown = "not checked — tacho can't tell which program this plugin runs"
	execGone := "exec " + gone + " swiftbar\n"
	cases := []struct {
		name, script, note string
	}{
		{"contrib's PATH lookup", string(contrib), "not checked — the plugin looks `tacho` up on the PATH SwiftBar gives it"},
		{"a name off the PATH", "#!/bin/bash\nexec gone-482 swiftbar\n", "looks `gone-482` up on the PATH"},
		{"a relative path", "#!/bin/bash\nexec ./tacho swiftbar\n", unknown},
		{"an expansion", "#!/bin/bash\nexec \"$HOME/gone/tacho\" swiftbar\n", unknown},
		{"an option of exec", "#!/bin/bash\nexec -a tacho " + execGone[len("exec "):], unknown},
		{"an unterminated quote", "#!/bin/bash\nexec \"" + gone + " swiftbar\n", unknown},
		{"exec without a command", "#!/bin/bash\nexec\n", unknown},
		{"a fallback on the exec line", "#!/bin/bash\nexec " + gone + " swiftbar || echo tacho\n", unknown},
		{"a command after exec", "#!/bin/bash\n" + execGone + "echo tacho\n", unknown},
		{"another exec after exec", "#!/bin/bash\n" + execGone + "exec /bin/sh -c true\n", unknown},
		{"an export after exec", "#!/bin/bash\n" + execGone + "export A=1\n", unknown},
		{"a branch before exec", "#!/bin/bash\nif [ -x /opt/tacho ]; then exec /opt/tacho swiftbar; fi\n" + execGone, unknown},
		{"another command before exec", "#!/bin/bash\ncd /tmp\n" + execGone, unknown},
		{"a line continuation", "#!/bin/bash\nexec " + gone + " \\\n  swiftbar\n", unknown},
		// The backslash-newline joins the next line to the word, so it is
		// no comment: the program is /nonexistent-482/tacho#x.
		{"a word continued onto a comment-like line", "#!/bin/bash\nexec " + gone + "\\\n#x swiftbar\n", unknown},
		{"a command substitution in an export", "#!/bin/bash\nexport A=$(/opt/tacho swiftbar)\n" + execGone, unknown},
		{"an expansion that can exit", "#!/bin/bash\nexport A=${UNSET:?}\n" + execGone, unknown},
		{"a backslash in double quotes", "#!/bin/bash\nexport A=\"a\\\"b\"\n" + execGone, unknown},
		// \" doesn't end the quotes, so the shell runs exit before exec.
		{"an escaped quote hiding a command", "#!/bin/bash\nexport A=\"\\\" #\"; exit\n" + execGone, unknown},
		{"a backquote in double quotes", "#!/bin/bash\nexport A=\"`/opt/tacho swiftbar`\"\n" + execGone, unknown},
		{"a special parameter", "#!/bin/bash\nexport A=$1\n" + execGone, unknown},
		{"two assignments", "#!/bin/bash\nexport A=1 B=2\n" + execGone, unknown},
		{"an export with no value", "#!/bin/bash\nexport A\n" + execGone, unknown},
		{"an unterminated quote in an export", "#!/bin/bash\nexport A='x\n" + execGone, unknown},
		{"another shell", "#!/bin/zsh\n" + execGone, unknown},
		{"a shell option", "#!/bin/bash -n\n" + execGone, unknown},
		{"a relative interpreter", "#!bash\n" + execGone, unknown},
		{"no shebang", execGone, unknown},
		{"CRLF line ends", "#!/bin/bash\r\nexec " + gone + " swiftbar\r\n", unknown},
		{"no exec line", "#!/bin/bash\nexport A=1\n" + gone + " swiftbar\n", unknown},
		{"too large", "#!/bin/bash\n" + strings.Repeat("#\n", maxPluginScript/2) + execGone, unknown},
	}
	for _, c := range cases {
		warning, note := swiftBarPluginWarning(c.script, "")
		if warning != "" {
			t.Errorf("%s: warning = %q, want none (the plugin isn't checkable)", c.name, warning)
		}
		if !strings.Contains(note, c.note) {
			t.Errorf("%s: note = %q, want containing %q", c.name, note, c.note)
		}
	}
}

// doctor's integrations lines: the plugin found, then the warning or note
// on what it runs, or neither when it's fine (#482).
func TestReportSwiftBarPlugin(t *testing.T) {
	isolateSwiftBar(t)
	plugin := filepath.Join(t.TempDir(), "tacho.30s.sh")
	t.Setenv("SWIFTBAR_PLUGIN_PATH", plugin)
	report := func(script string) string {
		t.Helper()
		if err := os.WriteFile(plugin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return capture(t, &os.Stdout, func() { reportSwiftBarPlugin("") })
	}
	found := "  SwiftBar:   plugin found (" + plugin + ")\n"

	out := report(setup.SwiftBarPlugin("/nonexistent-482/tacho"))
	if want := found + "    warning:  the plugin runs /nonexistent-482/tacho, which is missing or not executable"; !strings.HasPrefix(out, want) {
		t.Errorf("missing program: got %q, want it to start with %q", out, want)
	}
	out = report("#!/bin/bash\nexec tacho swiftbar\n")
	if want := found + "    note:     not checked — the plugin looks `tacho` up on the PATH"; !strings.HasPrefix(out, want) {
		t.Errorf("PATH lookup: got %q, want it to start with %q", out, want)
	}
	if runtime.GOOS == "windows" {
		return // the rest runs a POSIX path, and Windows has no mode bits to deny reading
	}
	own := filepath.Join(t.TempDir(), "gauges.sh")
	if err := os.WriteFile(own, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := report(setup.SwiftBarPlugin(own)); out != found {
		t.Errorf("a program that's there: got %q, want only %q", out, found)
	}
	if os.Geteuid() == 0 {
		return // root reads the file whatever its mode
	}
	report("#!/bin/bash\n")
	if err := os.Chmod(plugin, 0o000); err != nil {
		t.Fatal(err)
	}
	out = capture(t, &os.Stdout, func() { reportSwiftBarPlugin("") })
	if want := found + "    note:     not checked — can't read it: "; !strings.HasPrefix(out, want) {
		t.Errorf("unreadable plugin: got %q, want it to start with %q", out, want)
	}
}
