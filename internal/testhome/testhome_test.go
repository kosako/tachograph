package testhome_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kosako/tachograph/internal/testhome"
)

func TestMain(m *testing.M) { testhome.Run(m) }

// TestRunPointsUserDirsIntoAThrowawayHome checks the barrier itself: every
// directory the standard library resolves on this platform lies under the
// throwaway home, and the overrides tests set for themselves start cleared.
func TestRunPointsUserDirsIntoAThrowawayHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(home), "tacho-test-home-") {
		t.Fatalf("os.UserHomeDir() = %q, want the throwaway home", home)
	}
	for name, dir := range map[string]func() (string, error){
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
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "TACHO_CONFIG_DIR", "TACHO_CACHE_DIR"} {
		if v := os.Getenv(k); v != "" {
			t.Errorf("%s = %q, want it cleared", k, v)
		}
	}
}
