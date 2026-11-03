package core

import (
	"errors"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/schema"
)

// Statusline returns the document a statusline render shows: the Claude row
// built from the session JSON Claude Code pipes in (input), the rest as
// Status serves it. The payload is snapshotted so the other renderers can
// reuse its rate limits (the piggyback design from issue #4). The live
// payload only knows the current session, so the Claude row keeps today's
// all-session aggregate that Status attached. The statusline is the only
// caller; the assembly lives here, next to Status's, so a derived value added
// to one route reaches the other (#295, #446).
func Statusline(opts Options, input []byte) schema.Status {
	return statusline(opts, input, cache.LockSnapshot)
}

// statusline is Statusline with the snapshot's lock passed in, for tests.
func statusline(opts Options, input []byte, lock func(tool string) (func(), error)) schema.Status {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	claudeT := claude.Collect(claude.Options{Root: opts.ClaudeRoot, Now: opts.Now, StatuslineInput: input})
	// The live payload carries no cumulative tokens; count them over the
	// session's main and subagent / workflow transcripts, and total today's
	// portion so {claude.*.session.today} works (#262, #375).
	AddSessionTree(&claudeT, opts.Now, pricing.Load())
	if shouldWriteStatuslineSnapshot(input, claudeT) {
		// The snapshot belongs to the config root this payload was observed
		// from (#321); the payload names none, so resolve it as Status does.
		root := Roots(opts)[schema.ToolClaudeCode]
		writeStatuslineSnapshot(&claudeT, opts.Now, root, lock)
	}
	s := Status(opts) // the codex side rides the TTL cache
	for i := range s.Tools {
		if s.Tools[i].Tool == schema.ToolClaudeCode {
			claudeT.Daily = s.Tools[i].Daily
			s.Tools[i] = claudeT
		}
	}
	return s
}

func shouldWriteStatuslineSnapshot(input []byte, t schema.Tool) bool {
	if strings.TrimSpace(string(input)) == "" || !t.Available || t.Error != nil {
		return false
	}
	if len(t.Limits) > 0 || t.Model != nil || t.Plan != nil || t.Credits != nil {
		return true
	}
	if t.Session != nil {
		if t.Session.ID != nil || t.Session.CWD != nil || t.Session.ContextWindow != nil ||
			t.Session.ContextUsedPct != nil || t.Session.Tokens != nil || t.Session.TranscriptPath != nil {
			return true
		}
	}
	if t.Fallback != nil {
		return t.Fallback.SessionTokens != nil || t.Fallback.EstimatedCostUSD != nil
	}
	return false
}

// writeStatuslineSnapshot merges the payload's limits with the snapshot's and
// saves it, holding the snapshot's lock (taken with lock, cache.LockSnapshot)
// from the read to the write: another session's statusline saving in between
// would otherwise be overwritten by this older merge (#330). When the lock
// isn't had — another session still holds it after the wait — the merge
// still serves this statusline's line but isn't saved, since saving it could
// roll back what the holder saves; the next run saves again. A system without
// a file lock saves unlocked, keeping the narrow race, or nothing would ever
// be saved there.
func writeStatuslineSnapshot(t *schema.Tool, now time.Time, root string, lock func(tool string) (func(), error)) {
	unlock, err := lock(t.Tool)
	if err == nil {
		defer unlock()
	}
	save := err == nil || errors.Is(err, errors.ErrUnsupported)
	limitsObserved := mergeSnapshotLimits(t, now, root)
	AddProjections(t, now) // the statusline row bypasses Status's assemble for Claude (#295)
	if save {
		_ = cache.WriteSnapshot(*t, limitsObserved, root)
	}
}

// mergeSnapshotLimits merges the cached snapshot's windows into the live
// payload's (MergeLimits): a window the payload lacks is carried, so a
// payload without limits or with one window doesn't erase what other
// renderers show, and one the snapshot observed later is kept over the
// payload's older reading (#330). Only the windows still running are
// carried: a window past its reset would otherwise be re-saved under a fresh
// collected_at and read (and notify) as the current one (#318). Limits are
// merged only between subscription payloads: bedrock/api/vertex keep limits
// null by the collector contract (#186). It returns when the tool's limits
// were originally observed — the oldest of their windows' observations, zero
// when the tool ends up without limits — so the snapshot rewrite can't
// re-stamp old limits as fresh.
func mergeSnapshotLimits(t *schema.Tool, now time.Time, root string) time.Time {
	if t.Backend == schema.BackendSubscription {
		carried, backend, observed, ok := cache.ReadSnapshotLimits(t.Tool, cache.SnapshotMaxAge, now, root)
		if ok && backend == schema.BackendSubscription {
			t.Limits = MergeLimits(t.Limits, datedLimits(RunningLimits(carried, now), observed))
		}
	}
	return earliestObservation(t.Limits)
}

// datedLimits gives each carried window whose observed_at is missing or
// unreadable the snapshot's own observation time (limits_collected_at), so
// it keeps aging out from when it was seen (#186) and can be weighed against
// the payload's reading (#330).
func datedLimits(limits []schema.Limit, observed time.Time) []schema.Limit {
	stamp := observed.Local().Format(time.RFC3339)
	for i := range limits {
		if _, ok := ObservationTime(limits[i]); !ok {
			limits[i].ObservedAt = &stamp
		}
	}
	return limits
}

// earliestObservation is the oldest observed_at among limits; zero when none
// can be read.
func earliestObservation(limits []schema.Limit) time.Time {
	var earliest time.Time
	for _, l := range limits {
		if ts, ok := ObservationTime(l); ok && (earliest.IsZero() || ts.Before(earliest)) {
			earliest = ts
		}
	}
	return earliest
}
