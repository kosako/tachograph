package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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

	if code := runSetup([]string{"claude", "--write"}); code != 0 {
		t.Fatalf("second --write returned %d", code)
	}
	if b, _ := os.ReadFile(bak); !bytes.Equal(b, original) {
		t.Errorf(".bak clobbered on rerun: got %s, want the original settings preserved", b)
	}
}
