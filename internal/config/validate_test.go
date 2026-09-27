package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Validate must reject exactly what Load falls back to defaults on: a
// wrongly typed value makes Load discard the whole file, so doctor relies on
// Validate to surface it (#260). Unknown enum values are not decode errors —
// they load and are reported as Warnings instead (#230).
func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid", `{"tools":["codex"],"menubar":{"metric":"cost"}}`, true},
		{"unknown enum value is a warning, not an error", `{"menubar":{"style":"bogus"}}`, true},
		{"tools as a string", `{"tools":"codex"}`, false},
		{"thresholds as strings", `{"notify":{"thresholds":["50"]}}`, false},
		{"syntax error", `{bad`, false},
	}
	for _, c := range cases {
		if err := Validate([]byte(c.in)); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// Load and Validate share one decode path: a file Validate rejects loads as
// the defaults, and LoadStrict reports the same error.
func TestLoadAgreesWithValidate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	in := []byte(`{"tools":"codex","menubar":{"metric":"cost"}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), in, 0o644); err != nil {
		t.Fatal(err)
	}
	if Validate(in) == nil {
		t.Fatal("Validate accepted a wrongly typed tools value")
	}
	if _, err := LoadStrict(); err == nil {
		t.Error("LoadStrict accepted a file Validate rejects")
	}
	if c := Load(); c.Menubar.Metric != Default().Menubar.Metric {
		t.Errorf("Load().Menubar.Metric = %q, want the default (whole file ignored)", c.Menubar.Metric)
	}
}
