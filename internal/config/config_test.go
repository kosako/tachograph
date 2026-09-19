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
	if len(c.Tools) != 2 || c.Menubar.Style != StyleMeter || c.Menubar.Metric != DefaultMetric || c.Limits.Display != DefaultLimitDisplay {
		t.Errorf("Load() = %+v, want defaults", c)
	}
}

// limits.display round-trips, and a config written before the key existed
// (#228) keeps the headroom display it had.
func TestLimitsDisplayRoundTripAndDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	c := Default()
	c.Limits.Display = "used"
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	if got := Load().Limits.Display; got != "used" {
		t.Errorf("Limits.Display = %q, want \"used\"", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"menubar":{"style":"number"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load().Limits.Display; got != DefaultLimitDisplay {
		t.Errorf("Limits.Display without the key = %q, want %q", got, DefaultLimitDisplay)
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

// LoadStrict must surface a broken file (write paths refuse to overwrite it)
// while Load stays lenient for the render paths.
func TestLoadStrictReportsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadStrict()
	if err == nil {
		t.Fatal("LoadStrict error = nil, want parse error")
	}
	// The returned config is still usable defaults (for `config show`).
	if len(c.Tools) != 2 || c.Menubar.Style != StyleMeter {
		t.Errorf("LoadStrict fallback = %+v, want defaults", c)
	}
	// Load keeps the lenient contract.
	if got := Load(); len(got.Tools) != 2 {
		t.Errorf("Load on broken file = %+v, want defaults", got)
	}
}

// LoadStrict's full contract: a missing file is defaults with no error, a
// file that starts as valid JSON but breaks mid-way must not leak the
// partially unmarshaled values, and an unreadable file (directory in place
// of the file) is an error.
func TestLoadStrictContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)

	// Missing file: plain defaults, no error.
	c, err := LoadStrict()
	if err != nil || len(c.Tools) != 2 {
		t.Errorf("missing file: got %+v, %v; want defaults, nil", c, err)
	}

	// Partial unmarshal: "tools" decodes before the syntax error, and that
	// partial value must not leak into the returned defaults.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"tools": ["codex"], INVALID`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = LoadStrict()
	if err == nil {
		t.Fatal("partial unmarshal: error = nil, want parse error")
	}
	if len(c.Tools) != 2 {
		t.Errorf("partial unmarshal leaked into fallback: Tools = %v, want defaults", c.Tools)
	}

	// Unreadable file: a directory in place of config.json fails ReadFile on
	// every platform.
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStrict(); err == nil {
		t.Error("unreadable file: error = nil, want read error")
	}
}

// notify.thresholds round-trips normalized (invalid and duplicate values
// dropped, descending), a file written before the key existed reads as off,
// and off persists as an explicit empty list.
func TestNotifyThresholdsRoundTripAndNormalize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if got := Load().Notify.Thresholds; got == nil || len(got) != 0 {
		t.Errorf("default Notify.Thresholds = %#v, want []", got)
	}
	c := Default()
	c.Notify.Thresholds = []int{30, 50, 30, 0, 100, 10}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got := Load().Notify.Thresholds
	if want := []int{50, 30, 10}; !equalInts(got, want) {
		t.Errorf("Notify.Thresholds = %v, want %v (saved as given, normalized on load)", got, want)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tools":["codex"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load().Notify.Thresholds; got == nil || len(got) != 0 {
		t.Errorf("Notify.Thresholds without the key = %#v, want [] (off)", got)
	}
}

func TestNormalizeThresholds(t *testing.T) {
	if got := NormalizeThresholds(nil); got == nil || len(got) != 0 {
		t.Errorf("NormalizeThresholds(nil) = %#v, want []", got)
	}
	if got := NormalizeThresholds([]int{10, 99, 1, 10, -5, 100}); !equalInts(got, []int{99, 10, 1}) {
		t.Errorf("NormalizeThresholds = %v, want [99 10 1]", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
