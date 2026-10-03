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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
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
		time.Sleep(2 * time.Millisecond)
	}
}
