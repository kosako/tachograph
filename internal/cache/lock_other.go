//go:build !darwin && !linux && !windows

package cache

import (
	"errors"
	"time"
)

// lockFile has no file lock to take on the systems tacho doesn't ship for:
// concurrent statusline merges there keep their narrow race (#330).
func lockFile(string, time.Duration) (func(), error) {
	return nil, errors.ErrUnsupported
}
