package cache

import "time"

// snapshotLockWait bounds the wait for another process's merge of the same
// snapshot, which takes milliseconds; past it the caller goes on without the
// lock rather than leave the snapshot unwritten.
const snapshotLockWait = time.Second

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
