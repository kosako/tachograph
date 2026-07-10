package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kosako/tachograph/internal/config"
)

// Disabling the last tool via the dropdown (configToggleTool) must persist an
// explicit "tools": [] — not "tools": null, which would reload as both tools.
// This exercises the actual user path; a config-level Load test passes even on
// the buggy nil writer because Load already honors [].
func TestConfigToggleLastToolPersistsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)

	if code := configToggleTool("claude-code"); code != 0 { // both -> [codex]
		t.Fatalf("toggle claude-code returned %d", code)
	}
	if code := configToggleTool("codex"); code != 0 { // [codex] -> []
		t.Fatalf("toggle codex returned %d", code)
	}

	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"tools": []`)) {
		t.Errorf("persisted config = %s, want explicit \"tools\": [] (not null)", b)
	}
	if c := config.Load(); len(c.Tools) != 0 {
		t.Errorf("Load().Tools = %v, want empty after disabling every tool", c.Tools)
	}
}

// `tacho config set tools ""` is the CLI equivalent and must also persist [].
func TestConfigSetEmptyToolsPersistsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)

	if code := configSet("tools", ""); code != 0 {
		t.Fatalf("config set tools \"\" returned %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"tools": []`)) {
		t.Errorf("persisted config = %s, want explicit \"tools\": []", b)
	}
	if c := config.Load(); len(c.Tools) != 0 {
		t.Errorf("Load().Tools = %v, want empty", c.Tools)
	}
}

// menubar.metric must reject context (it isn't a menu-bar metric) and accept a
// real one, through the actual configSet path.
func TestConfigSetMenubarMetricRejectsContext(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	if code := configSet("menubar.metric", "context"); code == 0 {
		t.Error("configSet menubar.metric context returned 0, want non-zero (context excluded)")
	}
	if code := configSet("menubar.metric", "cost"); code != 0 {
		t.Errorf("configSet menubar.metric cost returned %d, want 0", code)
	}
}

// A config.json that fails to parse must not be clobbered by write commands:
// configSet / configToggleTool refuse instead of saving defaults over the
// user's (fixable) file.
func TestConfigWritesRefuseInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")
	broken := []byte(`{"tools": ["claude-code"], INVALID`)
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := configSet("menubar.style", "number"); code != 1 {
		t.Fatalf("configSet on broken config returned %d, want 1", code)
	}
	if code := configToggleTool("codex"); code != 1 {
		t.Fatalf("configToggleTool on broken config returned %d, want 1", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, broken) {
		t.Fatalf("broken config was rewritten:\n%s", got)
	}
}
