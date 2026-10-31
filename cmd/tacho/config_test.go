package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
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

// config set tools keeps each tool once, in the order first written (#379).
func TestConfigSetToolsDropsRepeats(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)

	if code := configSet("tools", "codex, claude-code,codex"); code != 0 {
		t.Fatalf("config set tools returned %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Tools []string }
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if want := []string{"codex", "claude-code"}; !slices.Equal(saved.Tools, want) {
		t.Errorf("saved tools = %q, want %q", saved.Tools, want)
	}
}

// A tool written twice in a hand-edited config.json is summed once: tacho
// daily's table, total $ included, reads as with the tool written once
// (#379).
func TestDailyTableCountsRepeatedToolOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tools":["codex","codex"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cost := 2.0
	days := []string{"2026-10-12"}
	cols := map[string][]*schema.Daily{schema.ToolCodex: {{Tokens: 1000, CostUSD: &cost}}}
	got := render.DailyTable(days, config.Load().Tools, cols)
	if want := render.DailyTable(days, []string{schema.ToolCodex}, cols); got != want {
		t.Errorf("DailyTable =\n%s\nwant\n%s", got, want)
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

// limits.display accepts only remaining / used and persists the choice
// through the actual configSet path (#228).
func TestConfigSetLimitsDisplay(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	if code := configSet("limits.display", "headroom"); code == 0 {
		t.Error("configSet limits.display headroom returned 0, want non-zero")
	}
	if code := configSet("limits.display", "used"); code != 0 {
		t.Errorf("configSet limits.display used returned %d, want 0", code)
	}
	if got := config.Load().Limits.Display; got != "used" {
		t.Errorf("Load().Limits.Display = %q, want \"used\"", got)
	}
	if code := configSet("limits.display", "remaining"); code != 0 {
		t.Errorf("configSet limits.display remaining returned %d, want 0", code)
	}
	if got := config.Load().Limits.Display; got != "remaining" {
		t.Errorf("Load().Limits.Display = %q, want \"remaining\"", got)
	}
}

// menubar.limits accepts show / hide only (#301).
func TestConfigSetMenubarLimits(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	if code := configSet("menubar.limits", "off"); code == 0 {
		t.Error("configSet menubar.limits off returned 0, want non-zero")
	}
	if code := configSet("menubar.limits", "hide"); code != 0 {
		t.Errorf("configSet menubar.limits hide returned %d, want 0", code)
	}
	if !config.Load().LimitsHidden() {
		t.Error("Load().LimitsHidden() = false after set hide, want true")
	}
	if code := configSet("menubar.limits", "show"); code != 0 {
		t.Errorf("configSet menubar.limits show returned %d, want 0", code)
	}
	if config.Load().LimitsHidden() {
		t.Error("Load().LimitsHidden() = true after set show, want false")
	}
}

// menubar.history accepts show / hide only (#302).
func TestConfigSetMenubarHistory(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	if code := configSet("menubar.history", "never"); code == 0 {
		t.Error("configSet menubar.history never returned 0, want non-zero")
	}
	if code := configSet("menubar.history", "hide"); code != 0 {
		t.Errorf("configSet menubar.history hide returned %d, want 0", code)
	}
	if !config.Load().HistoryHidden() {
		t.Error("Load().HistoryHidden() = false after set hide, want true")
	}
	if code := configSet("menubar.history", "show"); code != 0 {
		t.Errorf("configSet menubar.history show returned %d, want 0", code)
	}
	if config.Load().HistoryHidden() {
		t.Error("Load().HistoryHidden() = true after set show, want false")
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

// `config show` must succeed on a valid or missing config and exit 1 (with
// defaults still printed) on a broken one — silence would hide the breakage
// that the write commands refuse to touch.
func TestConfigShowExitCodes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if code := runConfig([]string{"show"}); code != 0 {
		t.Errorf("show with no config = %d, want 0", code)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tools": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runConfig([]string{"show"}); code != 0 {
		t.Errorf("show with valid config = %d, want 0", code)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runConfig([]string{"show"}); code != 1 {
		t.Errorf("show with broken config = %d, want 1", code)
	}
}

// notify.thresholds accepts a comma-separated list of 1–99, normalizes it
// (descending, deduped), rejects anything else, and an empty value turns
// notifications off.
func TestConfigSetNotifyThresholds(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	for _, bad := range []string{"0", "100", "abc", "50,x", "-5"} {
		if code := configSet("notify.thresholds", bad); code == 0 {
			t.Errorf("configSet notify.thresholds %q returned 0, want non-zero", bad)
		}
	}
	if code := configSet("notify.thresholds", "30, 50,10,30"); code != 0 {
		t.Fatalf("configSet notify.thresholds returned %d, want 0", code)
	}
	got := config.Load().Notify.Thresholds
	if len(got) != 3 || got[0] != 50 || got[1] != 30 || got[2] != 10 {
		t.Errorf("Load().Notify.Thresholds = %v, want [50 30 10]", got)
	}
	if code := configSet("notify.thresholds", ""); code != 0 {
		t.Fatalf("configSet notify.thresholds \"\" returned %d, want 0", code)
	}
	if got := config.Load().Notify.Thresholds; len(got) != 0 {
		t.Errorf("Load().Notify.Thresholds after clearing = %v, want []", got)
	}
}

// While another process holds the config lock past its wait, `config set` and
// the dropdown's tool toggle save nothing and exit 1 with the reason, instead
// of saving over that process's update (#439); once it lets go they save.
func TestConfigEditsWaitForTheConfigLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	unlock, err := cache.LockConfig()
	if err != nil {
		t.Fatalf("hold the config lock: %v", err)
	}
	for name, edit := range map[string]func() int{
		"config set":  func() int { return configSet("menubar.style", "number") },
		"toggle-tool": func() int { return configToggleTool("codex") },
	} {
		var code int
		stderr := capture(t, &os.Stderr, func() { code = edit() })
		if code != 1 || !strings.Contains(stderr, "another update holds the config") {
			t.Errorf("%s while the lock is held: exit %d, stderr %q; want 1 and the reason", name, code, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Errorf("config.json written while the lock was held elsewhere (%v)", err)
	}
	unlock()
	if code := configSet("menubar.style", "number"); code != 0 {
		t.Fatalf("config set after the lock was let go: exit %d", code)
	}
	if c := config.Load(); c.Menubar.Style != "number" {
		t.Errorf("style %q after the lock was let go, want number", c.Menubar.Style)
	}
}
