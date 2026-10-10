package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives the whole run a throwaway home (#377). Tests isolate
// themselves through variables only the code under test honors
// (CLAUDE_CONFIG_DIR, CODEX_HOME, TACHO_CONFIG_DIR, TACHO_CACHE_DIR); should
// it stop honoring one, the fallback it writes to must still not be the
// developer's real files — Claude Code's settings.json, tacho's config and
// cache. So HOME and what os.UserHomeDir, os.UserConfigDir, and
// os.UserCacheDir read on each platform point into a temporary directory, and
// the developer's own overrides are cleared. A test's t.Setenv still wins
// while it runs. The packages whose tests write there carry the same
// main_test.go; keep the copies in step.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "tacho-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain:", err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"HOME":              home,
		"USERPROFILE":       home, // os.UserHomeDir's source on Windows
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":    filepath.Join(home, ".cache"),
		"APPDATA":           filepath.Join(home, "AppData", "Roaming"), // os.UserConfigDir on Windows
		"LOCALAPPDATA":      filepath.Join(home, "AppData", "Local"),   // os.UserCacheDir on Windows
		"CLAUDE_CONFIG_DIR": "",
		"CODEX_HOME":        "",
		"TACHO_CONFIG_DIR":  "",
		"TACHO_CACHE_DIR":   "",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "TestMain:", err)
			os.RemoveAll(home)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: removing the test home:", err)
	}
	os.Exit(code)
}
