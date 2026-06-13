package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kosako/tachograph/internal/schema"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	c := Load()
	if len(c.Tools) != 2 || c.Menubar.Style != StyleMeter || c.Menubar.Metric != DefaultMetric {
		t.Errorf("Load() = %+v, want defaults", c)
	}
}

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	want := Config{Tools: []string{schema.ToolCodex}, Menubar: Menubar{Style: StyleNumber, Metric: "cost"}}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if len(got.Tools) != 1 || got.Tools[0] != schema.ToolCodex {
		t.Errorf("Tools = %v", got.Tools)
	}
	if got.Menubar.Style != StyleNumber || got.Menubar.Metric != "cost" {
		t.Errorf("Menubar = %+v", got.Menubar)
	}
}

func TestLoadPartialFileKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	// Only menubar.metric set; tools and style should fall back to defaults.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"menubar":{"metric":"context"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Load()
	if c.Menubar.Metric != "context" {
		t.Errorf("Metric = %q", c.Menubar.Metric)
	}
	if c.Menubar.Style != StyleMeter {
		t.Errorf("Style = %q, want default meter", c.Menubar.Style)
	}
	if len(c.Tools) != 2 {
		t.Errorf("Tools = %v, want both defaults", c.Tools)
	}
}

func TestLoadInvalidFileYieldsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := Load(); len(c.Tools) != 2 || c.Menubar.Style != StyleMeter {
		t.Errorf("Load() = %+v, want defaults on invalid file", c)
	}
}

func TestToolEnabled(t *testing.T) {
	c := Config{Tools: []string{schema.ToolCodex}}
	if !c.ToolEnabled(schema.ToolCodex) || c.ToolEnabled(schema.ToolClaudeCode) {
		t.Errorf("ToolEnabled mismatch: %+v", c)
	}
}
