//go:build darwin || linux || windows

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

// A waiter gets the lock once its holder lets go within the wait. The holder
// lets go only after the waiter has found the lock taken (lockContended), so
// the retry after contention is what takes it, however late the waiter
// starts (#445).
func TestLockFileAcquiresOnceReleasedDuringWait(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	unlock, err := lockFile("x.lock", time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	contended := make(chan struct{}, 1)
	lockContended = func() {
		select {
		case contended <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { lockContended = nil })
	got := make(chan error, 1)
	go func() {
		second, err := lockFile("x.lock", 5*time.Second)
		if err == nil {
			second()
		}
		got <- err
	}()
	select {
	case <-contended:
	case err := <-got:
		t.Fatalf("waiter finished (%v) without meeting the held lock", err)
	}
	unlock()
	if err := <-got; err != nil {
		t.Fatalf("lock after the holder let go: %v", err)
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
