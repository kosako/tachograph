// Package atomicfile replaces a file through a temp file and a rename, so a
// reader sees the old content or the new, never a partial write. The cache,
// the config, and the files `tacho setup --write` writes all go through it,
// so a failed write is handled the same way everywhere (#379). The config and
// the setup files follow a symlink to the file it leads to (WriteThrough);
// the cache doesn't (#413).
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

// renameRetryBudget bounds how long a write keeps retrying a rename that
// another tacho process is in the way of for the moment (#371).
const renameRetryBudget = 500 * time.Millisecond

// Write writes b to a temp file in path's directory, gives it perm, and
// renames it over path. The temp file is removed whenever the write fails,
// so a failure leaves path as it was and nothing beside it.
func Write(path string, b []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = replaceFile(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// WriteThrough is Write for a file the user may keep elsewhere and link into
// place — settings.json, config.json, or the SwiftBar plugin in a dotfiles
// repository: when path is a symlink, it replaces the file the link leads to,
// in that file's directory, and the link stays as it is, as git writes
// through a link (#413). A link whose target can't be resolved (it leads
// nowhere, or round in a loop) is an error, and so is a target whose
// directory can't take the temp file (a read-only store); either way
// nothing is changed. tacho's own cache files go through Write instead: a
// link planted in the cache dir is replaced, never followed elsewhere.
func WriteThrough(path string, b []byte, perm os.FileMode) error {
	target, err := Target(path)
	if err != nil {
		return err
	}
	if err := Write(target, b, perm); err != nil {
		if target != path {
			return fmt.Errorf("%s is a symlink to %s: %w", path, target, err)
		}
		return err
	}
	return nil
}

// Target is the file WriteThrough writes for path: path itself when it is
// missing or not a link, else the file the link leads to. A link whose target
// can't be resolved is an error, as is any other failure to look at path.
// Whatever guards a WriteThrough (a lock beside the file) keys on Target, so
// two paths linked to one file share it (#439).
func Target(path string) (string, error) {
	// Only a path known not to exist, or known not to be a link, is written
	// in place: after any other failure to look, it may be a link that a
	// rename would replace.
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return path, nil // a missing path is created
	case err != nil:
		return "", err
	case info.Mode()&fs.ModeSymlink == 0:
		return path, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a symlink whose target can't be resolved: %w", path, err)
	}
	return target, nil
}

// replaceFile renames tmp over path. A rename that fails with an error
// transientRenameError accepts — on Windows, another process has the file
// open or is replacing it — is retried for up to renameRetryBudget; macOS
// and Linux replace atomically, so there a failure is returned at once.
func replaceFile(tmp, path string) error {
	return retry(func() error { return os.Rename(tmp, path) }, transientRenameError, renameRetryBudget, time.Sleep)
}

// retry calls f until it succeeds, fails with an error transient rejects, or
// the next wait would take the time slept past budget, and returns f's last
// error. The wait starts at 1ms and grows by a random amount up to itself
// each time, as the go command's robustio does.
func retry(f func() error, transient func(error) bool, budget time.Duration, sleep func(time.Duration)) error {
	wait := time.Millisecond
	var slept time.Duration
	for {
		err := f()
		if err == nil || !transient(err) || slept+wait > budget {
			return err
		}
		sleep(wait)
		slept += wait
		wait += rand.N(wait)
	}
}
