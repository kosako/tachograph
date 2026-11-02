//go:build darwin || linux

package cache

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO where a lock file goes neither stalls the open nor is taken (#448).
func TestLockFileRefusesAFIFO(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", dir)
	if err := syscall.Mkfifo(filepath.Join(dir, "x.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		unlock, err := lockFile("x.lock", time.Second)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("lock taken on a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lockFile stalled on a FIFO")
	}
}
