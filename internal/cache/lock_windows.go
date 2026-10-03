//go:build windows

package cache

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// The standard library has no file lock on Windows; LockFileEx is called
// from kernel32 directly, as golang.org/x/sys/windows would, without the
// dependency.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	// errLockViolation is ERROR_LOCK_VIOLATION: another handle holds the lock.
	errLockViolation syscall.Errno = 33
)

// lockFile takes an exclusive LockFileEx lock on the first byte of the named
// file in the cache dir, polling for up to wait. Byte-range locks belong to
// the handle, so a second open excludes the first even within one process;
// the lock is released before the file is closed.
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
	h := f.Fd()
	deadline := time.Now().Add(wait)
	for {
		var ol syscall.Overlapped
		r, _, errno := procLockFileEx.Call(h, lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
		if r != 0 {
			return func() {
				var ol syscall.Overlapped
				procUnlockFileEx.Call(h, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
				f.Close()
			}, nil
		}
		if !errors.Is(errno, errLockViolation) || time.Now().After(deadline) {
			f.Close()
			return nil, errno
		}
		time.Sleep(2 * time.Millisecond)
	}
}
