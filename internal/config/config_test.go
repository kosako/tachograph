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

// An explicit empty tools array means "show nothing" and must be honored, not
// silently restored to both tools (the disable-the-last-tool case).
func TestLoadHonorsExplicitEmptyTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tools":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := Load(); len(c.Tools) != 0 {
		t.Errorf("Tools = %v, want empty (explicit [] must be honored, not reset to defaults)", c.Tools)
	}
}

// A null tools key (manual edit / older config) is ambiguous and falls back to
// defaults; only an explicit [] means empty.
func TestLoadNullToolsFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tools":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := Load(); len(c.Tools) != 2 {
		t.Errorf("Tools = %v, want both defaults for null", c.Tools)
	}
}

// Saving an empty selection must round-trip as empty, not reload as defaults.
func TestSaveEmptyToolsRoundTrips(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	if err := Save(Config{Tools: []string{}, Menubar: Menubar{Style: StyleMeter, Metric: DefaultMetric}}); err != nil {
		t.Fatal(err)
	}
	if c := Load(); len(c.Tools) != 0 {
		t.Errorf("Tools = %v, want empty after saving an empty selection", c.Tools)
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

func TestFilterStatusUsesConfiguredOrder(t *testing.T) {
	c := Config{Tools: []string{schema.ToolCodex, schema.ToolClaudeCode}}
	s := schema.Status{Tools: []schema.Tool{
		{Tool: schema.ToolClaudeCode},
		{Tool: schema.ToolCodex},
	}}

	got := c.FilterStatus(s)
	if len(got.Tools) != 2 || got.Tools[0].Tool != schema.ToolCodex || got.Tools[1].Tool != schema.ToolClaudeCode {
		t.Fatalf("FilterStatus tools = %+v", got.Tools)
	}
}

func TestFilterStatusHonorsEmptyTools(t *testing.T) {
	got := (Config{Tools: []string{}}).FilterStatus(schema.Status{Tools: []schema.Tool{
		{Tool: schema.ToolClaudeCode},
		{Tool: schema.ToolCodex},
	}})
	if len(got.Tools) != 0 {
		t.Fatalf("FilterStatus tools = %+v, want empty", got.Tools)
	}
}
