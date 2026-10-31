package cache

import (
	"errors"
	"time"
)

// snapshotLockWait bounds the wait for another process's merge of the same
// snapshot, which takes milliseconds. Past it the caller still draws its line
// from its merge but doesn't save it, so as not to roll back what the holder
// saves; only a system without a file lock saves unlocked (see the statusline's
// writeStatuslineSnapshot).
const snapshotLockWait = time.Second

// lockContended, when set (tests only), is called each time lockFile finds
// the lock held and is about to wait and retry, so a test can tell the
// contended path ran before it lets the holder go.
var lockContended func()

// LockSnapshot holds tool's snapshot across processes for one read-merge-
// write: every Claude session's statusline rewrites the one file, and a
// merge read before another session's write would save over it, rolling a
// newer observation back (#330). unlock releases it. err is non-nil when the
// lock isn't held — still taken by another process after snapshotLockWait,
// or not supported on this system.
func LockSnapshot(tool string) (unlock func(), err error) {
	return lockFile("snapshot-"+tool+".lock", snapshotLockWait)
}

// notifyStateLockWait bounds the wait for another plugin's notification
// pass, which takes about as long as one `open`.
const notifyStateLockWait = time.Second

// LockNotifyState holds the notification record across processes for one
// read-evaluate-send-write pass: SwiftBar plugins for different profiles
// share the record, and a pass read before another's write would save over
// it, losing that profile's announcements (#364). unlock and err as for
// LockSnapshot.
func LockNotifyState() (unlock func(), err error) {
	return lockFile("notify-state.lock", notifyStateLockWait)
}

// configLockWait bounds the wait for another process's config update, which
// reads, edits, and rewrites one small file.
const configLockWait = time.Second

// LockConfig holds the config file at path across processes for one
// read-edit-write update (config.Update): every `tacho config` edit rewrites
// the whole file, so two that overlap — a `config set` and a SwiftBar
// Settings click — would otherwise each save over the other's edit (#439).
// The lock is the file's sibling, path + ".lock", not one in the cache dir,
// so every process saving that file takes the same lock whatever its cache
// dir (TACHO_CACHE_DIR). unlock and err as for LockSnapshot.
func LockConfig(path string) (unlock func(), err error) {
	if path == "" {
		return nil, errors.New("config: no home directory")
	}
	return lockPath(path+".lock", configLockWait)
}
