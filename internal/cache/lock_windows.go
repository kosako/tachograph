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
	return lockPath(filepath.Join(dir, name), wait)
}

// lockPath is lockFile on the file at path, wherever it is, making its
// directory if missing. The lock file is opened without following a reparse
// point (a symlink or junction) in its place and must be a regular file: a
// link planted there neither creates nor opens the file it leads to (#448),
// and the lock isn't taken.
func lockPath(path string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := openLockNoFollow(path)
	if err != nil {
		return nil, err
	}
	if err := checkLockFile(f); err != nil {
		f.Close()
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
		if lockContended != nil {
			lockContended()
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// openLockNoFollow opens path for reading and writing, creating it if missing,
// as os.OpenFile does, but on a reparse point itself rather than what it
// leads to (FILE_FLAG_OPEN_REPARSE_POINT), so checkLockFile sees the link.
func openLockNoFollow(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
