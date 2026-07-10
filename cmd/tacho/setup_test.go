package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
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

func TestSwiftBarPluginCandidates(t *testing.T) {
	swiftDir := t.TempDir()
	xbarDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("SWIFTBAR_PLUGIN_DIR", swiftDir)
	t.Setenv("XBAR_PLUGIN_PATH", xbarDir)
	t.Setenv("HOME", home)

	got := strings.Join(swiftBarPluginCandidates(), "\n")
	for _, want := range []string{
		filepath.Join(swiftDir, "tacho.30s.sh"),
		filepath.Join(xbarDir, "tacho.30s.sh"),
		filepath.Join(home, "Library", "Application Support", "SwiftBar", "Plugins", "tacho.30s.sh"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("swiftBarPluginCandidates() missing %q in:\n%s", want, got)
		}
	}
}

// The bare-command decision must check identity, not mere presence: a
// different tacho on the PATH would silently serve the statusline instead of
// the binary being configured (#193).
func TestPathTachoIsSelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup and symlinks differ on windows")
	}
	dir := t.TempDir()
	selfDir := filepath.Join(dir, "self")
	otherDir := filepath.Join(dir, "other")
	linkDir := filepath.Join(dir, "link")
	for _, d := range []string{selfDir, otherDir, linkDir} {
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
	if err := os.Symlink(self, filepath.Join(linkDir, "tacho")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", otherDir)
	if pathTachoIsSelf(self) {
		t.Error("a different tacho on PATH must not count as self")
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
	if pathTachoIsSelf("") {
		t.Error("an unknown running binary must never claim the bare command")
	}
}
