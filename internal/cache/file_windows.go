//go:build windows

package cache

import (
	"errors"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION: another handle has the
// file open in a way that excludes this one.
const errSharingViolation syscall.Errno = 32

// transientRenameError reports the errors Windows gives a rename over a file
// another process has open or is replacing at that moment — the ones the go
// command's robustio retries a rename on: ERROR_ACCESS_DENIED,
// ERROR_SHARING_VIOLATION, and ERROR_FILE_NOT_FOUND, which a rename racing
// another can get spuriously. Any open handle blocks the rename, even one
// that shares delete access, so a reader that is only reading the file is
// enough (#371).
func transientRenameError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ERROR_ACCESS_DENIED, errSharingViolation, syscall.ERROR_FILE_NOT_FOUND:
		return true
	}
	return false
}
