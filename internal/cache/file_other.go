//go:build !windows

package cache

// transientRenameError is always false: rename replaces atomically here,
// whoever has the file open, so one that failed fails again.
func transientRenameError(error) bool { return false }
