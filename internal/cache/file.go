package cache

import (
	"math/rand/v2"
	"os"
	"time"
)

// renameRetryBudget bounds how long a cache write keeps retrying a rename
// that another tacho process is in the way of for the moment (#371).
const renameRetryBudget = 500 * time.Millisecond

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
