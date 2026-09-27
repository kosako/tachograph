package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/pricing"
)

// jsonFileState feeds the doctor's config.json / pricing.json lines; each
// branch carries a user-facing diagnosis, so pin all four.
func TestJSONFileState(t *testing.T) {
	dir := t.TempDir()

	if got := jsonFileState(filepath.Join(dir, "missing.json"), nil); got != "(default)" {
		t.Errorf("missing = %q, want (default)", got)
	}

	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"tools": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(valid, nil); got != "present" {
		t.Errorf("valid = %q, want present", got)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(broken, nil); !strings.HasPrefix(got, "present but INVALID JSON") {
		t.Errorf("broken = %q, want INVALID JSON diagnosis", got)
	}

	// A directory in place of the file fails ReadFile with a non-NotExist
	// error on every platform.
	asDir := filepath.Join(dir, "dir.json")
	if err := os.Mkdir(asDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(asDir, nil); !strings.HasPrefix(got, "unreadable — ") {
		t.Errorf("dir = %q, want unreadable diagnosis", got)
	}
}

// A file that is valid JSON but doesn't decode into what the loader expects
// (a wrongly typed value) is ignored in full by the render path, so doctor
// must flag it too — not report it as present (#260).
func TestJSONFileStateFlagsWrongTypes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		content  string
		validate func([]byte) error
	}{
		{"pricing.json", `{"claude-fable":{"input":"10"},"claude-opus":{"input":99}}`, pricing.Validate},
		{"config.json", `{"tools":"codex","menubar":{"metric":"cost"}}`, config.Validate},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		got := jsonFileState(p, c.validate)
		if !strings.HasPrefix(got, "present but INVALID (ignored) — ") {
			t.Errorf("%s = %q, want the INVALID (ignored) diagnosis", c.name, got)
		}
	}
	// A well-typed file stays "present" under the same validators.
	ok := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(ok, []byte(`{"claude-opus":{"input":99}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := jsonFileState(ok, pricing.Validate); got != "present" {
		t.Errorf("well-typed pricing.json = %q, want present", got)
	}
}

// The doctor's config section lists the values Load ignores; a missing or
// unparsable file yields no value warnings (its state is reported instead).
func TestConfigValueWarnings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if got := configValueWarnings(); len(got) != 0 {
		t.Errorf("no file: warnings = %q, want none", got)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"limits":{"display":"usage"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := configValueWarnings()
	if len(got) != 1 || !strings.Contains(got[0], `limits.display: "usage"`) {
		t.Errorf("warnings = %q, want the limits.display one", got)
	}
	if err := os.WriteFile(path, []byte(`{"limits": INVALID`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := configValueWarnings(); len(got) != 0 {
		t.Errorf("invalid JSON: warnings = %q, want none (reported as invalid JSON instead)", got)
	}
}
