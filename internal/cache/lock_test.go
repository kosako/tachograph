//go:build darwin || linux

package cache

import (
	"testing"
	"time"
)

// A second holder waits while the lock is held — two opens of the lock file
// exclude each other even within one process — and gets it once released.
func TestLockFileExcludesAnotherHolder(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	unlock, err := lockFile("x.lock", time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	got := make(chan error, 1)
	go func() {
		second, err := lockFile("x.lock", 5*time.Second)
		if err == nil {
			second()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("second holder returned (err = %v) while the lock was held", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("second lock after release: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("second holder still waiting after the lock was released")
	}
}

// Past its wait a holder gives up with an error instead of blocking the
// statusline, and the lock taken by the other is unaffected.
func TestLockFileGivesUpAfterWait(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	unlock, err := lockFile("x.lock", time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer unlock()
	start := time.Now()
	if second, err := lockFile("x.lock", 50*time.Millisecond); err == nil {
		second()
		t.Fatal("second lock taken while the first was held")
	}
	if waited := time.Since(start); waited < 50*time.Millisecond {
		t.Errorf("gave up after %v, want the 50ms wait", waited)
	}
}
