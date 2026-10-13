package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kosako/tachograph/internal/config"
)

// The statusline reads its template from config.Dir, where `tacho config
// statusline-preset` writes it, however that directory is resolved (#383):
// TACHO_CONFIG_DIR, else XDG_CONFIG_HOME, else the home directory.
func TestLoadTemplateFollowsConfigDir(t *testing.T) {
	for _, c := range []struct {
		name       string
		tacho, xdg bool
	}{
		{"TACHO_CONFIG_DIR", true, true},
		{"XDG_CONFIG_HOME", false, true},
		{"home", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // the home os.UserHomeDir reads on Windows
			t.Setenv("TACHO_CONFIG_DIR", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			if c.xdg {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			}
			if c.tacho {
				t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
			}
			dir := config.Dir()
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			const tmpl = "{claude.model} from the config dir"
			if err := os.WriteFile(filepath.Join(dir, "statusline.tmpl"), []byte(tmpl+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := loadTemplate(); got != tmpl {
				t.Errorf("loadTemplate() = %q, want the template in config.Dir() (%s)", got, dir)
			}
		})
	}
}
