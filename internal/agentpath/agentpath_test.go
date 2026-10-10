package agentpath

import (
	"path/filepath"
	"testing"
)

// A root passed in wins, then CLAUDE_CONFIG_DIR / CODEX_HOME; a user who sets
// neither gets the agents' own defaults under the home directory, ~/.claude
// and ~/.codex. With no home directory either, there is no root.
func TestRootsResolveInOrder(t *testing.T) {
	cases := []struct {
		name    string
		resolve func(string) (string, bool)
		env     string
		def     string
	}{
		{"ClaudeRoot", ClaudeRoot, "CLAUDE_CONFIG_DIR", ".claude"},
		{"CodexRoot", CodexRoot, "CODEX_HOME", ".codex"},
	}
	for _, c := range cases {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // the home os.UserHomeDir reads on Windows
		t.Setenv(c.env, "")
		if got, ok := c.resolve(""); !ok || got != filepath.Join(home, c.def) {
			t.Errorf("%s with no %s = %q, %v; want %q", c.name, c.env, got, ok, filepath.Join(home, c.def))
		}

		env := t.TempDir()
		t.Setenv(c.env, env)
		if got, ok := c.resolve(""); !ok || got != env {
			t.Errorf("%s with %s set = %q, %v; want %q", c.name, c.env, got, ok, env)
		}
		explicit := t.TempDir()
		if got, ok := c.resolve(explicit); !ok || got != explicit {
			t.Errorf("%s(%q) with %s set = %q, %v; want the root passed in", c.name, explicit, c.env, got, ok)
		}

		t.Setenv(c.env, "")
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		if got, ok := c.resolve(""); ok {
			t.Errorf("%s with no %s and no home = %q, want none", c.name, c.env, got)
		}
	}
}
