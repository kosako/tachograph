package schema

import "time"

// StaleAfter is tool's stale threshold: Codex's windows stay trustworthy for
// hours after its last turn (CodexStaleAfterMinutes), the others' for
// StaleAfterMinutes.
func StaleAfter(tool string) time.Duration {
	if tool == ToolCodex {
		return CodexStaleAfterMinutes * time.Minute
	}
	return StaleAfterMinutes * time.Minute
}

// ObservedStale reports whether l was observed longer ago than tool's stale
// threshold (see StaleAfter): use and resets since then aren't in the
// reading (#331). A limit without a readable observation time isn't reported
// stale — its age is unknown.
func ObservedStale(tool string, l Limit, now time.Time) bool {
	if l.ObservedAt == nil {
		return false
	}
	observed, err := time.Parse(time.RFC3339, *l.ObservedAt)
	return err == nil && now.Sub(observed) > StaleAfter(tool)
}

// ResetPassed reports whether l's reset time, when readable, is not after
// now: the window it describes has ended, and its use says nothing about the
// current one (#363, #374). A snapshot still serves such a window while its
// observation is fresh. A limit without a readable reset time isn't reported
// passed — when it ends is unknown.
func ResetPassed(l Limit, now time.Time) bool {
	if l.ResetsAt == nil {
		return false
	}
	resets, err := time.Parse(time.RFC3339, *l.ResetsAt)
	return err == nil && !resets.After(now)
}
