//go:build !darwin && !linux

package cache

import (
	"errors"
	"time"
)

// lockFile has no file lock to take here: the standard library offers none
// on these systems, and tacho keeps to it, so concurrent statusline merges
// keep their narrow race (#330).
func lockFile(string, time.Duration) (func(), error) {
	return nil, errors.ErrUnsupported
}
