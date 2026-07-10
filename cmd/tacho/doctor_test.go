package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonFileState feeds the doctor's config.json / pricing.json lines; each
// branch carries a user-facing diagnosis, so pin all four.
func TestJSONFileState(t *testing.T) {
	dir := t.TempDir()

	if got := jsonFileState(filepath.Join(dir, "missing.json")); got != "(default)" {
		t.Errorf("missing = %q, want (default)", got)
	}

	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"tools": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(valid); got != "present" {
		t.Errorf("valid = %q, want present", got)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(broken); !strings.HasPrefix(got, "present but INVALID JSON") {
		t.Errorf("broken = %q, want INVALID JSON diagnosis", got)
	}

	// A directory in place of the file fails ReadFile with a non-NotExist
	// error on every platform.
	asDir := filepath.Join(dir, "dir.json")
	if err := os.Mkdir(asDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(asDir); !strings.HasPrefix(got, "unreadable — ") {
		t.Errorf("dir = %q, want unreadable diagnosis", got)
	}
}
