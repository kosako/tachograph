//go:build darwin || linux

package cache

import (
	"testing"
	"time"
)

// A second open of the lock file is excluded while the first holds it —
// even within one process — and gets it once the first lets go.
func TestLockFileExcludesAnotherHolder(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	unlock, err := lockFile("x.lock", time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if second, err := lockFile("x.lock", 20*time.Millisecond); err == nil {
		second()
		unlock()
		t.Fatal("second lock taken while the first was held")
	}
	unlock()
	second, err := lockFile("x.lock", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("lock after the first let go: %v", err)
	}
	second()
}

// A waiter gets the lock as soon as its holder lets go within the wait.
func TestLockFileAcquiresOnceReleasedDuringWait(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	unlock, err := lockFile("x.lock", time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		unlock()
	}()
	start := time.Now()
	second, err := lockFile("x.lock", 5*time.Second)
	if err != nil {
		t.Fatalf("lock after the holder let go: %v", err)
	}
	second()
	if waited := time.Since(start); waited < 40*time.Millisecond {
		t.Errorf("acquired after %v, before the holder let go", waited)
	}
}

// Past its wait a holder gives up with an error instead of blocking the
// statusline.
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
