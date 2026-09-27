package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// npmLayout builds an npm global install: <prefix>/lib/node_modules/tachograph/bin
// holds the launcher tacho.js and the platform binary next to it, and
// <prefix>/bin/tacho links to the launcher (#259). Returns the PATH entry
// (<prefix>/bin/tacho) and the platform binary (what os.Executable reports).
func npmLayout(t *testing.T) (pathTacho, binary string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the unix npm layout uses a symlink")
	}
	prefix := t.TempDir()
	pkgBin := filepath.Join(prefix, "lib", "node_modules", "tachograph", "bin")
	if err := os.MkdirAll(pkgBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tacho.js", "tacho"} {
		if err := os.WriteFile(filepath.Join(pkgBin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathTacho = filepath.Join(prefix, "bin", "tacho")
	if err := os.Symlink(filepath.Join("..", "lib", "node_modules", "tachograph", "bin", "tacho.js"), pathTacho); err != nil {
		t.Fatal(err)
	}
	return pathTacho, filepath.Join(pkgBin, "tacho")
}

// The npm launcher on the PATH runs this very binary, so it is the same
// install — doctor must not warn about a "different binary" (#259).
func TestSameInstallRecognizesNpmLauncher(t *testing.T) {
	pathTacho, binary := npmLayout(t)
	if sameExecutable(pathTacho, binary) {
		t.Fatal("precondition: the launcher and the binary are different files")
	}
	if !sameInstall(pathTacho, binary) {
		t.Error("the npm launcher for this binary should count as the same install")
	}
	other := filepath.Join(t.TempDir(), "tacho")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if sameInstall(pathTacho, other) {
		t.Error("an npm launcher for another binary must not count as the same install")
	}
}

// On Windows npm writes a tacho.cmd / tacho.ps1 shim in the prefix that runs
// node_modules/tachograph/bin/tacho.js; the binary sits next to tacho.js.
func TestNpmLauncherTargetWindowsShim(t *testing.T) {
	prefix := t.TempDir()
	name := "tacho"
	if runtime.GOOS == "windows" {
		name = "tacho.exe"
	}
	want := filepath.Join(prefix, "node_modules", "tachograph", "bin", name)
	for _, shim := range []string{"tacho.cmd", "tacho.ps1"} {
		if got := npmLauncherTarget(filepath.Join(prefix, shim)); got != want {
			t.Errorf("npmLauncherTarget(%s) = %q, want %q", shim, got, want)
		}
	}
	if got := npmLauncherTarget(filepath.Join(prefix, "tacho")); got != "" {
		t.Errorf("a plain binary is not a launcher, got %q", got)
	}
}

// doctor's statusLine check: a command that doesn't resolve, one that runs a
// different tacho than this one (e.g. left behind in an old Node version's
// directory), and one that is not tacho at all (a user's own script).
func TestStatusLineWarning(t *testing.T) {
	pathTacho, binary := npmLayout(t)
	stale := filepath.Join(t.TempDir(), "tacho")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "statusline.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, command, want string
	}{
		{"this binary", binary + " statusline", ""},
		{"the npm launcher for this binary", pathTacho + " statusline", ""},
		{"missing", filepath.Join(t.TempDir(), "gone", "tacho") + " statusline", "does not resolve"},
		{"another tacho", stale + " statusline", "different tacho"},
		{"not tacho", script, ""},
	}
	for _, c := range cases {
		got := statusLineWarning(c.command, binary)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: statusLineWarning = %q, want containing %q", c.name, got, c.want)
		}
	}
}

// setup keeps baking the binary's absolute path in for an npm install (no
// Node startup per status line refresh), but says so and warns that the path
// is tied to the Node version.
func TestSetupNoteForNpmLauncher(t *testing.T) {
	pathTacho, binary := npmLayout(t)
	t.Setenv("PATH", filepath.Dir(pathTacho))
	note := setupNote(binary+" statusline", binary)
	if !strings.Contains(note, "npm launcher") || !strings.Contains(note, "Node") {
		t.Errorf("setupNote = %q, want the npm launcher explanation", note)
	}
	if strings.Contains(note, "doesn't resolve") {
		t.Errorf("setupNote = %q, must not claim the binary doesn't resolve on PATH", note)
	}
}
