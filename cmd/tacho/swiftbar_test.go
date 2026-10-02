package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// menubar.history=hide skips the history pass of the SwiftBar tick — no
// daily-history.json is read or written and the dropdown has no history
// section — while show (the default) computes and caches it (#302). The
// agents' data dirs are empty temp dirs, so no real transcripts are read;
// the cache and config dirs are temporary too.
func TestRunSwiftbarHistoryHiddenSkipsCache(t *testing.T) {
	cacheDir, cfgDir := t.TempDir(), t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", cacheDir)
	t.Setenv("TACHO_CONFIG_DIR", cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CMUX_WORKSPACE_ID", "")
	t.Setenv("SWIFTBAR_PLUGIN_PATH", "") // not under SwiftBar: no notifications
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	histFile := filepath.Join(cacheDir, "daily-history.json")

	tick := func(visibility string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"menubar":{"history":"`+visibility+`"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		return capture(t, &os.Stdout, func() {
			if code := runSwiftbar(nil); code != 0 {
				t.Errorf("runSwiftbar with history %s = %d, want 0", visibility, code)
			}
		})
	}

	out := tick("hide")
	if _, err := os.Stat(histFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("daily-history.json exists after a tick with the history hidden (stat err: %v)", err)
	}
	if strings.Contains(out, "日の cost/tokens") || strings.Contains(out, "日計") {
		t.Errorf("history section rendered with the history hidden:\n%s", out)
	}
	if !strings.Contains(out, "----☐ 直近 7 日 | bash=") {
		t.Errorf("sections menu missing the unchecked history entry:\n%s", out)
	}

	out = tick("show")
	if _, err := os.Stat(histFile); err != nil {
		t.Errorf("daily-history.json not written after a tick with the history shown: %v", err)
	}
	if !strings.Contains(out, "日の cost/tokens") || !strings.Contains(out, "----☑ 直近 7 日 | bash=") {
		t.Errorf("history section or its checked entry missing with the history shown:\n%s", out)
	}
}
