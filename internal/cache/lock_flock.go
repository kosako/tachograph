//go:build darwin || linux

package cache

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockFile takes an exclusive flock on the named file in the cache dir,
// polling for up to wait. Closing the file releases the lock, so a holder
// that dies never leaves it taken.
func lockFile(name string, wait time.Duration) (func(), error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return lockPath(filepath.Join(dir, name), wait)
}

// lockPath is lockFile on the file at path, wherever it is, making its
// directory if missing. The lock file is opened without following a link in
// its place (O_NOFOLLOW; O_NONBLOCK so a FIFO there can't stall the open) and
// must be a regular file: a link planted there neither creates nor opens
// the file it leads to (#448), and the lock isn't taken.
func lockPath(path string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	if err := checkLockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, err
		}
		if lockContended != nil {
			lockContended()
		}
		time.Sleep(2 * time.Millisecond)
	}
}
