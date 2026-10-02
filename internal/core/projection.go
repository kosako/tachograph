package core

import (
	"time"

	"github.com/kosako/tachograph/internal/collector/codex"
	"github.com/kosako/tachograph/internal/schema"
)

// AddProjections fills each limit's projection from its latest observation
// alone (#295): the window's average pace since it began, extrapolated to
// its reset. Nothing is stored and no history is read — the figures follow
// from used_pct, resets_at, window_minutes, and observed_at, so they are a
// reference for "if the average so far holds", not a guarantee of what can
// still be spent. The assumptions and the null rules are in docs/schema.md.
func AddProjections(t *schema.Tool, now time.Time) {
	for i := range t.Limits {
		t.Limits[i].Projection = project(t.Limits[i], now, staleAfter(t.Tool))
	}
}

// staleAfter is how old a limit's observation may be before its projection
// is withheld: the tool's own stale threshold, since Codex's windows stay
// trustworthy for hours after its last turn (see codex.StaleAfterMinutes).
func staleAfter(tool string) time.Duration {
	if tool == schema.ToolCodex {
		return codex.StaleAfterMinutes * time.Minute
	}
	return schema.StaleAfterMinutes * time.Minute
}

// project is the window-average extrapolation of one limit. With D the
// window length, R its reset time, O the observation time, and U the use
// observed, the window is taken to have begun at R − D with nothing used; E =
// O − (R − D) is how far into it the observation falls. The pace is U / E
// per hour, the use at reset U × D / E (not capped: more than 100 means the
// average would run past the limit), the headroom 100 minus that.
//
// Checks run from the structural to the temporal, so the reason names the
// first thing that rules the figures out. The elapsed share is kept whenever
// the window itself is sound, even if the use is unknown.
func project(l schema.Limit, now time.Time, staleAfter time.Duration) schema.Projection {
	p := schema.Projection{Method: schema.ProjectionWindowAverage}
	unavailable := func(reason string) schema.Projection {
		p.UnavailableReason = &reason
		return p
	}
	if l.WindowMinutes == nil || l.ResetsAt == nil || l.ObservedAt == nil {
		return unavailable(schema.ProjectionMissingInput)
	}
	resets, err := time.Parse(time.RFC3339, *l.ResetsAt)
	if err != nil {
		return unavailable(schema.ProjectionMissingInput)
	}
	observed, err := time.Parse(time.RFC3339, *l.ObservedAt)
	if err != nil {
		return unavailable(schema.ProjectionMissingInput)
	}
	minutes := *l.WindowMinutes
	if minutes <= 0 {
		return unavailable(schema.ProjectionInvalidWindow)
	}
	window := time.Duration(minutes) * time.Minute
	elapsed := observed.Sub(resets.Add(-window))
	if elapsed <= 0 || elapsed > window {
		return unavailable(schema.ProjectionInvalidWindow)
	}
	elapsedPct := 100 * elapsed.Minutes() / float64(minutes)
	p.ElapsedPctAtObservation = &elapsedPct
	if l.SavedResets != nil {
		return unavailable(schema.ProjectionUnsupportedWindow)
	}
	if l.UsedPct == nil {
		return unavailable(schema.ProjectionMissingInput)
	}
	if !resets.After(now) {
		return unavailable(schema.ProjectionResetPassed)
	}
	if now.Sub(observed) > staleAfter {
		return unavailable(schema.ProjectionStale)
	}
	used := *l.UsedPct
	pace := 60 * used / elapsed.Minutes()
	atReset := used * float64(minutes) / elapsed.Minutes()
	headroom := 100 - atReset
	p.PacePctPerHour, p.UsedPctAtReset, p.HeadroomPctAtReset = &pace, &atReset, &headroom
	return p
}
