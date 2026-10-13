// Package atomicfile replaces a file through a temp file and a rename, so a
// reader sees the old content or the new, never a partial write. The cache,
// the config, and the files `tacho setup --write` writes all go through it,
// so a failed write is handled the same way everywhere (#379).
package atomicfile

import (
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
