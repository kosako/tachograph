//go:build darwin || linux || windows

package cache

import (
	"os"
	"path/filepath"
	"runtime"
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

// A link planted where a lock file goes is neither followed nor taken: the
// lock fails, a missing file the link leads to isn't created, and an
// existing one is left as it was (#448). Each lock goes through lockPath.
func TestLockFileDoesNotFollowLink(t *testing.T) {
	for _, existing := range []bool{false, true} {
		dir := t.TempDir()
		t.Setenv("TACHO_CACHE_DIR", dir)
		victim := filepath.Join(t.TempDir(), "victim")
		old := time.Now().Add(-40 * 24 * time.Hour).Truncate(time.Second)
		if existing {
			if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(victim, old, old); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(victim, filepath.Join(dir, "snapshot-claude.lock")); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("can't create a symlink on windows: %v", err)
			}
			t.Fatal(err)
		}
		if unlock, err := LockSnapshot("claude"); err == nil {
			unlock()
			t.Errorf("existing target %v: lock taken through a planted link", existing)
		}
		info, err := os.Stat(victim)
		switch {
		case !existing && !os.IsNotExist(err):
			t.Errorf("the link's missing target was created (stat: %v, %v)", info, err)
		case existing && (err != nil || !info.ModTime().Equal(old) || info.Size() != 4):
			t.Errorf("the link's target = %v, %v; want it left as it was", info, err)
		}
	}
}
