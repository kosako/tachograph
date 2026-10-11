package testhome_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kosako/tachograph/internal/testhome"
)

// overrides are the variables Run must clear.
var overrides = []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "TACHO_CONFIG_DIR", "TACHO_CACHE_DIR"}

// TestMain stands in for a developer's shell: before Run, every variable it
// must point into the throwaway home or clear is set to somewhere outside it.
// So a variable Run leaves alone shows up below whatever the machine's own
// environment and whichever OS runs the tests; on macOS, say,
// os.UserConfigDir never reads XDG_CONFIG_HOME.
func TestMain(m *testing.M) {
	outer := filepath.Join(os.TempDir(), "tacho-outer-home")
	for _, k := range append([]string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"}, overrides...) {
		if err := os.Setenv(k, filepath.Join(outer, k)); err != nil {
			fmt.Fprintln(os.Stderr, "TestMain:", err)
			os.Exit(1)
		}
	}
	testhome.Run(m)
}

// TestRunPointsHomeIntoATempDir checks the barrier itself, variable by
// variable, and that what the standard library resolves on this platform
// follows.
func TestRunPointsHomeIntoATempDir(t *testing.T) {
	home := os.Getenv("HOME")
	if !strings.HasPrefix(filepath.Base(home), "tacho-test-home-") {
		t.Fatalf("HOME = %q, want the throwaway home", home)
	}
	for k, want := range map[string]string{
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for _, k := range overrides {
		if v := os.Getenv(k); v != "" {
			t.Errorf("%s = %q, want it cleared", k, v)
		}
	}
	for name, dir := range map[string]func() (string, error){
		"os.UserHomeDir":   os.UserHomeDir,
		"os.UserConfigDir": os.UserConfigDir,
		"os.UserCacheDir":  os.UserCacheDir,
	} {
		got, err := dir()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if rel, err := filepath.Rel(home, got); err != nil || !filepath.IsLocal(rel) {
			t.Errorf("%s() = %q, want it under %q", name, got, home)
		}
	}
}
