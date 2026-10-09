package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
	"github.com/kosako/tachograph/internal/setup"
)

// Re-running `setup claude --write` must not clobber the .bak: the merge is
// idempotent, so without the once-only guard the second run would overwrite the
// original backup with tacho's own merged output and lose the user's pre-tacho
// statusLine.
func TestSetupWriteBackupNotClobberedOnRerun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	settings := filepath.Join(dir, "settings.json")
	original := []byte(`{"statusLine":{"type":"command","command":"OLD-USER-STATUSLINE","padding":0},"theme":"dark"}`)
	if err := os.WriteFile(settings, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runSetup([]string{"claude", "--write"}); code != 0 {
		t.Fatalf("first --write returned %d", code)
	}
	bak := settings + ".bak"
	if b, err := os.ReadFile(bak); err != nil || !bytes.Equal(b, original) {
		t.Fatalf(".bak after first write = %s (err %v), want the original settings", b, err)
	}
	requirePerm(t, bak, 0o600)
	requirePerm(t, settings, 0o600)

	if code := runSetup([]string{"claude", "--write"}); code != 0 {
		t.Fatalf("second --write returned %d", code)
	}
	if b, _ := os.ReadFile(bak); !bytes.Equal(b, original) {
		t.Errorf(".bak clobbered on rerun: got %s, want the original settings preserved", b)
	}
}

func requirePerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %v, want %v", path, got, want)
	}
}

func TestNewestJSONLPicksNewestNestedFile(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "projects", "old.jsonl")
	newPath := filepath.Join(root, "sessions", "2026", "07", "02", "new.jsonl")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, count, err := newestJSONL(root)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if !got.Equal(newTime) {
		t.Fatalf("newest = %v, want %v", got, newTime)
	}
}

func TestDoctorErrorHints(t *testing.T) {
	got := doctorErrorHint(schema.ToolCodex, "no_token_count")
	if !strings.Contains(got, "token_count") || !strings.Contains(got, "CODEX_HOME") {
		t.Fatalf("Codex no_token_count hint = %q", got)
	}
	got = doctorErrorHint(schema.ToolClaudeCode, "no_usage")
	if !strings.Contains(got, "Claude Code") || !strings.Contains(got, "usage") {
		t.Fatalf("Claude no_usage hint = %q", got)
	}
}

// doctor must find the plugin where SwiftBar actually loads it: the
// user-chosen PluginDirectory, SWIFTBAR_PLUGINS_PATH, or the default folder,
// under any tacho.*.sh name (#265).
func TestFindSwiftBarPlugin(t *testing.T) {
	home, setting := isolateSwiftBar(t)

	write := func(dir, name string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Another plugin and a folder named like ours don't count.
	defaultDir := filepath.Join(home, "Library", "Application Support", "SwiftBar", "Plugins")
	write(defaultDir, "other.30s.sh")
	if err := os.MkdirAll(filepath.Join(defaultDir, "tacho.dir.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findSwiftBarPlugin(); got != "" {
		t.Fatalf("no tacho plugin: findSwiftBarPlugin() = %q, want \"\"", got)
	}

	// Renamed interval in the default folder.
	renamed := write(defaultDir, "tacho.1m.sh")
	if got := findSwiftBarPlugin(); got != renamed {
		t.Errorf("renamed plugin: got %q, want %q", got, renamed)
	}

	// The PluginDirectory setting is searched before the default folder.
	custom := write(filepath.Join(t.TempDir(), "plugins"), "tacho.30s.sh")
	*setting = filepath.Dir(custom)
	if got := findSwiftBarPlugin(); got != custom {
		t.Errorf("PluginDirectory: got %q, want %q", got, custom)
	}

	// SWIFTBAR_PLUGINS_PATH is a folder; SWIFTBAR_PLUGIN_PATH is the file.
	viaEnv := write(t.TempDir(), "tacho.5m.sh")
	t.Setenv("SWIFTBAR_PLUGINS_PATH", filepath.Dir(viaEnv))
	if got := findSwiftBarPlugin(); got != viaEnv {
		t.Errorf("SWIFTBAR_PLUGINS_PATH: got %q, want %q", got, viaEnv)
	}
	self := write(t.TempDir(), "tacho.10s.sh")
	t.Setenv("SWIFTBAR_PLUGIN_PATH", self)
	if got := findSwiftBarPlugin(); got != self {
		t.Errorf("SWIFTBAR_PLUGIN_PATH: got %q, want %q", got, self)
	}
}

// isolateSwiftBar keeps doctor's plugin detection off the machine's real
// SwiftBar setup: a temp HOME, no SwiftBar plugin variables, and a
// PluginDirectory setting read from *setting (initially empty).
func isolateSwiftBar(t *testing.T) (home string, setting *string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWIFTBAR_PLUGINS_PATH", "")
	t.Setenv("SWIFTBAR_PLUGIN_PATH", "")
	setting = new(string)
	orig := swiftBarPluginDirectory
	swiftBarPluginDirectory = func() string { return *setting }
	t.Cleanup(func() { swiftBarPluginDirectory = orig })
	return home, setting
}

// The bare-command decision must check identity, not mere presence: a
// different tacho on the PATH would silently serve the statusline instead of
// the binary being configured (#193).
func TestPathTachoIsSelf(t *testing.T) {
	dir := t.TempDir()
	selfDir := filepath.Join(dir, "self")
	otherDir := filepath.Join(dir, "other")
	emptyDir := filepath.Join(dir, "empty")
	linkDir := filepath.Join(dir, "link")
	for _, d := range []string{selfDir, otherDir, emptyDir, linkDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self := filepath.Join(selfDir, "tacho")
	other := filepath.Join(otherDir, "tacho")
	for _, p := range []string{self, other} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// The negative cases must hold on every platform: an unverifiable or
	// mismatching PATH lookup never claims the bare command.
	if pathTachoIsSelf("") {
		t.Error("an unknown running binary must never claim the bare command")
	}
	t.Setenv("PATH", emptyDir)
	if pathTachoIsSelf(self) {
		t.Error("no tacho on PATH must not count as self")
	}
	t.Setenv("PATH", otherDir)
	if pathTachoIsSelf(self) {
		t.Error("a different tacho on PATH must not count as self")
	}

	// The positive cases need unix PATH lookup semantics (no PATHEXT) and
	// unprivileged symlinks.
	if runtime.GOOS == "windows" {
		t.Skip("positive cases need unix PATH lookup and symlinks")
	}
	if err := os.Symlink(self, filepath.Join(linkDir, "tacho")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", selfDir)
	if !pathTachoIsSelf(self) {
		t.Error("the same tacho on PATH should count as self")
	}
	// A symlink on the PATH pointing at this binary is still this binary.
	t.Setenv("PATH", linkDir)
	if !pathTachoIsSelf(self) {
		t.Error("a PATH symlink to this binary should count as self")
	}
}

// When the running binary can't be resolved, setup must fail without writing
// anything — the empty-exe fallback used to produce a bare command that could
// configure a different install (#199 must).
func TestSetupRefusesUnresolvableBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	orig := resolveExe
	resolveExe = func() string { return "" }
	t.Cleanup(func() { resolveExe = orig })

	if code := runSetup([]string{"claude", "--write"}); code != 1 {
		t.Fatalf("runSetup with unresolvable binary = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("settings.json was written despite the unresolvable binary")
	}
}

// A quoted-and-escaped command generated by setup.Command must round-trip
// through firstToken so the doctor resolves the real binary path instead of
// misdiagnosing a working setup (#194 L-01 / PR #208 review should).
func TestFirstTokenUnescapesQuotedCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, `my"quoted $HOME`+"`bin`", "tacho")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := setup.Command(false, bin)
	if got, ok := firstToken(command); got != bin || !ok {
		t.Errorf("firstToken(%q) = %q, %v, want %q, true", command, got, ok, bin)
	}
	if !statusLineResolves(command) {
		t.Errorf("statusLineResolves(%q) = false, want true (binary exists)", command)
	}
}

// firstToken reads the first word the way the shell Claude Code runs the
// command in does (#362): a leading ~ is the home directory and quotes and
// escapes come off, so the form Claude Code's own docs use
// (~/.claude/statusline.sh) and a single-quoted name are what they run. Syntax
// tacho doesn't evaluate is reported as such (ok false) rather than as a name
// that doesn't resolve.
func TestFirstTokenShellWords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cases := []struct {
		command, want string
		ok            bool
	}{
		{"~/.claude/statusline.sh", home + "/.claude/statusline.sh", true},
		{"~", home, true},
		{"'tacho' statusline", "tacho", true},
		{"'/opt/my dir'/tacho statusline", "/opt/my dir/tacho", true},
		{`"/opt/my dir/tacho" statusline`, "/opt/my dir/tacho", true},
		{`/opt/my\ dir/tacho statusline`, "/opt/my dir/tacho", true},
		{" \ttacho\tstatusline", "tacho", true},
		{"tacho;echo", "tacho", true},
		{"tacho|cat", "tacho", true},
		{"/opt/a#b/tacho", "/opt/a#b/tacho", true},
		{"/opt/a~b/tacho", "/opt/a~b/tacho", true},
		{"", "", true},
		{"'tacho statusline", "", true}, // unterminated: the shell won't run it
		{`"tacho statusline`, "", true},
		{"$HOME/.claude/statusline.sh", "", false},
		{`"$HOME"/.claude/statusline.sh`, "", false},
		{"${HOME}/.claude/statusline.sh", "", false},
		{"$(which tacho) statusline", "", false},
		{"`which tacho` statusline", "", false},
		{"~other/statusline.sh", "", false},
		{"FOO=1 tacho statusline", "", false},
		{"(tacho statusline)", "", false},
		{"{ tacho statusline; }", "", false},
		{"if true; then tacho statusline; fi", "", false},
		{"cd ~/.claude && ./statusline.sh", "", false},
		{"exec tacho statusline", "", false},
		{"/opt/*/tacho statusline", "", false},
		{"2>/dev/null tacho statusline", "", false},
		{"# tacho statusline", "", false},
	}
	for _, c := range cases {
		if got, ok := firstToken(c.command); got != c.want || ok != c.ok {
			t.Errorf("firstToken(%q) = %q, %v, want %q, %v", c.command, got, ok, c.want, c.ok)
		}
	}
}

// doctor accepts a statusLine that runs a script by ~/ or a quoted path, still
// warns when that script is missing, and only notes, without the warning and
// its advice to re-run setup, a command it can't follow to a program (#362).
func TestStatusLineWarningShellForms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, p := range []string{
		filepath.Join(home, ".claude", "statusline.sh"),
		filepath.Join(home, "my dir", "statusline.sh"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	quoted := "'" + filepath.ToSlash(filepath.Join(home, "my dir", "statusline.sh")) + "'"
	cases := []struct {
		name, command, warning, note string
	}{
		{"~/ path", "~/.claude/statusline.sh", "", ""},
		{"single-quoted path", quoted, "", ""},
		{"missing ~/ path", "~/.claude/gone.sh", "does not resolve", ""},
		{"expansion", "$HOME/.claude/statusline.sh", "", "not checked"},
		{"compound", "cd ~/.claude && ./statusline.sh", "", "not checked"},
	}
	for _, c := range cases {
		warning, note := statusLineWarning(c.command, "")
		if (c.warning == "") != (warning == "") || !strings.Contains(warning, c.warning) {
			t.Errorf("%s: warning = %q, want containing %q", c.name, warning, c.warning)
		}
		if (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
			t.Errorf("%s: note = %q, want containing %q", c.name, note, c.note)
		}
	}
}

// goBin must name where `go install` actually puts binaries: GOBIN when set,
// else the first GOPATH entry's bin — both with the go toolchain and, when go
// isn't callable, from the environment (#264).
func TestGoBin(t *testing.T) {
	home, profile := os.Getenv("HOME"), os.Getenv("USERPROFILE")
	a, b := t.TempDir(), t.TempDir()
	gobin := filepath.Join(t.TempDir(), "gobin")
	sep := string(filepath.ListSeparator)
	// Ignore the developer's own go env file (`go env -w GOBIN=...`).
	t.Setenv("GOENV", "off")

	t.Setenv("GOBIN", gobin)
	t.Setenv("GOPATH", a)
	if got := goBin(); got != gobin {
		t.Errorf("GOBIN set: goBin() = %q, want %q", got, gobin)
	}
	// Paths are used verbatim: a trailing space is part of a valid path.
	spaced := gobin + " "
	t.Setenv("GOBIN", spaced)
	if got := goBin(); got != spaced {
		t.Errorf("GOBIN with a trailing space: goBin() = %q, want %q", got, spaced)
	}

	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", a+sep+b)
	if got, want := goBin(), filepath.Join(a, "bin"); got != want {
		t.Errorf("GOPATH list: goBin() = %q, want %q (the first entry)", got, want)
	}

	// Values only in the go env file must come from `go env` (the
	// environment alone would give ~/go/bin). With no home directory
	// (HOME, or USERPROFILE on Windows) GOPATH is empty, so the output ends
	// in an empty line that must still count.
	if _, err := exec.LookPath("go"); err == nil {
		envFile := filepath.Join(t.TempDir(), "go.env")
		t.Setenv("GOENV", envFile)
		t.Setenv("GOPATH", "")
		if err := os.WriteFile(envFile, []byte("GOPATH="+b+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, want := goBin(), filepath.Join(b, "bin"); got != want {
			t.Errorf("go env file GOPATH: goBin() = %q, want %q", got, want)
		}
		if err := os.WriteFile(envFile, []byte("GOBIN="+gobin+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		if got := goBin(); got != gobin {
			t.Errorf("go env file GOBIN, empty GOPATH: goBin() = %q, want %q", got, gobin)
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", profile)
		t.Setenv("GOENV", "off")
		t.Setenv("GOPATH", a+sep+b)
	}

	// No go toolchain on PATH: read the same variables from the environment.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOBIN", gobin)
	if got := goBin(); got != gobin {
		t.Errorf("no go, GOBIN set: goBin() = %q, want %q", got, gobin)
	}
	t.Setenv("GOBIN", "")
	if got, want := goBin(), filepath.Join(a, "bin"); got != want {
		t.Errorf("no go, GOPATH list: goBin() = %q, want %q", got, want)
	}
}

// `tacho setup swiftbar` prints the plugin alone on stdout (so it can be
// redirected), with this binary's absolute path on the exec line (#270).
func TestSetupSwiftBarPrints(t *testing.T) {
	orig := resolveExe
	resolveExe = func() string { return "/opt/tools/tacho" }
	t.Cleanup(func() { resolveExe = orig })

	var code int
	out := capture(t, &os.Stdout, func() { code = runSetup([]string{"swiftbar"}) })
	if code != 0 || out != setup.SwiftBarPlugin("/opt/tools/tacho") {
		t.Errorf("setup swiftbar = %d, stdout %q; want 0 and the plugin", code, out)
	}
}

// --write installs into SwiftBar's PluginDirectory: a new tacho.30s.sh, or an
// installed tacho plugin replaced in place (keeping its interval name) with
// the old script backed up outside the plugin folder.
func TestSetupSwiftBarWrite(t *testing.T) {
	_, setting := isolateSwiftBar(t)
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	orig := resolveExe
	resolveExe = func() string { return "/opt/tools/tacho" }
	t.Cleanup(func() { resolveExe = orig })
	want := setup.SwiftBarPlugin("/opt/tools/tacho")
	write := func() int {
		var code int
		capture(t, &os.Stdout, func() { code = runSetup([]string{"swiftbar", "--write"}) })
		return code
	}

	// No plugin folder configured: nothing to guess.
	if code := write(); code != 1 {
		t.Errorf("unset PluginDirectory: exit = %d, want 1", code)
	}

	*setting = t.TempDir()
	if code := write(); code != 0 {
		t.Fatalf("fresh install: exit = %d, want 0", code)
	}
	fresh := filepath.Join(*setting, "tacho.30s.sh")
	info, err := os.Stat(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); string(b) != want {
		t.Errorf("fresh install: content %q, want the plugin", b)
	}
	// Windows has no execute bit (regular files report 0666).
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("fresh install: mode %v, want 0755", info.Mode().Perm())
	}

	// An installed, customized plugin is replaced in place and backed up.
	*setting = t.TempDir()
	old := "#!/bin/bash\nexport TACHO_APPEARANCE=light\nexec tacho swiftbar\n"
	renamed := filepath.Join(*setting, "tacho.1m.sh")
	if err := os.WriteFile(renamed, []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := write(); code != 0 {
		t.Fatalf("replace: exit = %d, want 0", code)
	}
	if b, _ := os.ReadFile(renamed); string(b) != want {
		t.Errorf("replace: %s = %q, want the plugin", renamed, b)
	}
	if _, err := os.Stat(filepath.Join(*setting, "tacho.30s.sh")); !os.IsNotExist(err) {
		t.Error("replace: added tacho.30s.sh next to the installed tacho.1m.sh")
	}
	bak := filepath.Join(os.Getenv("TACHO_CONFIG_DIR"), "swiftbar-plugin.bak")
	if b, _ := os.ReadFile(bak); string(b) != old {
		t.Errorf("backup %s = %q, want the replaced script", bak, b)
	}
}

// --write changes nothing when it can't do so safely: no config directory
// for the backup, or a plugin folder it can't list (which could hide an
// installed tacho plugin and leave two of them).
func TestSetupSwiftBarWriteRefusesUnsafe(t *testing.T) {
	_, setting := isolateSwiftBar(t)
	orig := resolveExe
	resolveExe = func() string { return "/opt/tools/tacho" }
	t.Cleanup(func() { resolveExe = orig })
	write := func() int {
		var code int
		capture(t, &os.Stdout, func() { code = runSetup([]string{"swiftbar", "--write"}) })
		return code
	}

	*setting = t.TempDir()
	installed := filepath.Join(*setting, "tacho.1m.sh")
	old := "#!/bin/bash\nexec tacho swiftbar\n"
	if err := os.WriteFile(installed, []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACHO_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // os.UserHomeDir's source on Windows
	if code := write(); code != 1 {
		t.Errorf("no config dir: exit = %d, want 1", code)
	}
	if b, _ := os.ReadFile(installed); string(b) != old {
		t.Errorf("no config dir: plugin rewritten to %q", b)
	}

	if runtime.GOOS == "windows" {
		return // directory permission bits don't block listing there
	}
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	if err := os.Chmod(*setting, 0o300); err != nil { // writable, not listable
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(*setting, 0o755) })
	if _, err := os.ReadDir(*setting); err == nil {
		t.Skip("directory stays listable (running as root?)")
	}
	if code := write(); code != 1 {
		t.Errorf("unlistable folder: exit = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(*setting, "tacho.30s.sh")); !os.IsNotExist(err) {
		t.Error("unlistable folder: added tacho.30s.sh next to the installed plugin")
	}
}
