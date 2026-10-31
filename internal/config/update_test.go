package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two updates that overlap both keep their edit: the second waits on the
// lock and reads the config only after the first has saved, rather than
// saving its edit over a copy read before the first's save (#439). The order
// is forced, not left to timing: the first's edit waits until the second has
// asked for the lock, and an Update that read before locking would by then
// have read the stale config.
func TestUpdateSerializesOverlappingEdits(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	var mu sync.Mutex
	requested := make(chan struct{}, 2)
	lock := func() (func(), error) {
		requested <- struct{}{}
		mu.Lock()
		return mu.Unlock, nil
	}
	second := make(chan error, 1)
	err := Update(lock, func(c *Config) error {
		<-requested // this update's own request
		go func() {
			second <- Update(lock, func(c *Config) error {
				c.Limits.Display = "used"
				return nil
			})
		}()
		<-requested // the second has asked for the lock, after any read before it
		c.Menubar.Style = StyleNumber
		return nil
	})
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second update: %v", err)
	}
	c, err := LoadStrict()
	if err != nil {
		t.Fatal(err)
	}
	if c.Menubar.Style != StyleNumber || c.Limits.Display != "used" {
		t.Errorf("style %q, display %q; want both edits kept (number, used)", c.Menubar.Style, c.Limits.Display)
	}
}

// What Update saves, and doesn't, when something stops it (#439).
func TestUpdateFailures(t *testing.T) {
	held := func() (func(), error) { return nil, errors.New("resource temporarily unavailable") }
	unsupported := func() (func(), error) { return nil, errors.ErrUnsupported }
	edit := func(c *Config) error { c.Menubar.Style = StyleNumber; return nil }
	saved := func(t *testing.T) string {
		t.Helper()
		b, err := os.ReadFile(Path())
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("the lock held past its wait", func(t *testing.T) {
		t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
		if err := Update(held, edit); err == nil {
			t.Error("Update = nil, want the lock's error")
		}
		if got := saved(t); got != "" {
			t.Errorf("saved %q while the lock was held elsewhere", got)
		}
	})
	t.Run("no file lock on this system", func(t *testing.T) {
		t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
		if err := Update(unsupported, edit); err != nil {
			t.Fatalf("Update = %v, want it to save unlocked", err)
		}
		if c := Load(); c.Menubar.Style != StyleNumber {
			t.Errorf("style %q, want the edit saved unlocked", c.Menubar.Style)
		}
	})
	t.Run("the edit refused", func(t *testing.T) {
		t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
		refused := errors.New("refused")
		err := Update(unsupported, func(c *Config) error { c.Menubar.Style = StyleNumber; return refused })
		if !errors.Is(err, refused) {
			t.Errorf("Update = %v, want the edit's error", err)
		}
		if got := saved(t); got != "" {
			t.Errorf("saved %q after the edit was refused", got)
		}
	})
	t.Run("an unreadable config", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("TACHO_CONFIG_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		var loadErr *LoadError
		if err := Update(unsupported, edit); !errors.As(err, &loadErr) {
			t.Errorf("Update = %v, want a *LoadError", err)
		}
		if got := saved(t); got != "{not json" {
			t.Errorf("config.json = %q, want it left as it was", got)
		}
	})
	t.Run("the lock released", func(t *testing.T) {
		t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
		released := false
		lock := func() (func(), error) { return func() { released = true }, nil }
		if err := Update(lock, edit); err != nil || !released {
			t.Errorf("Update = %v, released %v; want nil and the lock let go", err, released)
		}
	})
}
