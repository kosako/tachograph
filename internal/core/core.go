// Package core assembles collector output into the unified status document.
package core

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/collector/codex"
	"github.com/kosako/tachograph/internal/daily"
	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/schema"
)

type Options struct {
	ClaudeRoot string // for tests; default ~/.claude
	CodexRoot  string // for tests; default ~/.codex
	Now        time.Time
	NoCache    bool
}

// Status returns the unified document, served from the TTL cache when fresh
// and assembled from the same config roots (#321).
func Status(opts Options) schema.Status {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	roots := Roots(opts)
	if !opts.NoCache {
		if s, ok := cache.ReadStatus(cache.StatusTTL, opts.Now, roots); ok {
			return *s
		}
	}
	s := assemble(opts)
	if !opts.NoCache {
		_ = cache.WriteStatus(&s, roots) // serving the live result matters more than caching it
	}
	return s
}

// Roots are the config roots a Status for opts reads, by tool — the same
// resolution the collectors use (CLAUDE_CONFIG_DIR / CODEX_HOME, else the
// defaults under the home directory). The TTL cache, the Claude snapshot,
// and the daily history are keyed by them, so another profile's data is
// never served as this one's (#321, #337). A root that can't be resolved is
// left out.
func Roots(opts Options) map[string]string {
	roots := map[string]string{}
	if r, ok := agentpath.ClaudeRoot(opts.ClaudeRoot); ok {
		roots[schema.ToolClaudeCode] = r
	}
	if r, ok := agentpath.CodexRoot(opts.CodexRoot); ok {
		roots[schema.ToolCodex] = r
	}
	return roots
}

func assemble(opts Options) schema.Status {
	prices := pricing.Load()
	claudeT := claudeTool(opts)
	codexT := codex.Collect(codex.Options{Root: opts.CodexRoot, Now: opts.Now})
	addCodexSessionCost(&codexT, prices)
	claudeDaily, claudeDailyErr := daily.ClaudeTotals(opts.ClaudeRoot, opts.Now, prices)
	addDaily(&claudeT, claudeDaily, claudeDailyErr)
	codexDaily, codexDailyErr := daily.CodexTotals(opts.CodexRoot, opts.Now, prices)
	addDaily(&codexT, codexDaily, codexDailyErr)
	AddSessionTree(&claudeT, opts.Now, prices)
	AddProjections(&claudeT, opts.Now)
	AddProjections(&codexT, opts.Now)
	return schema.Status{
		SchemaVersion: schema.Version,
		GeneratedAt:   opts.Now.Local().Format(time.RFC3339),
		Tools:         []schema.Tool{claudeT, codexT},
	}
}

// addDaily attaches today's aggregate to an available tool. Cost is set only
// when non-zero (a priced model was seen). A scan error means the total is
// unknown, so Daily stays null instead of reading as zero usage.
func addDaily(t *schema.Tool, tot daily.Totals, err error) {
	if !t.Available || t.Error != nil || err != nil {
		return
	}
	t.Daily = tot.Schema()
}

// addCodexSessionCost estimates the session cost as current model x whole
// cumulative usage. token_count only carries cumulative totals, so a
// mid-session model switch is approximated at the current model's rate —
// daily.cost_usd is the per-event precise figure (#191).
func addCodexSessionCost(t *schema.Tool, prices pricing.Table) {
	if t.Tool != schema.ToolCodex || !t.Available || t.Error != nil ||
		t.Model == nil || t.Session == nil || t.Session.Tokens == nil {
		return
	}
	if t.Fallback != nil && t.Fallback.EstimatedCostUSD != nil {
		return
	}
	r, ok := prices.For(t.Model.ID)
	if !ok {
		return
	}
	u := t.Session.Tokens
	nonCachedInput := u.Input - u.CachedInput
	if nonCachedInput < 0 {
		nonCachedInput = 0
	}
	cost := r.Cost(nonCachedInput, 0, u.CachedInput, u.Output)
	if t.Fallback == nil {
		t.Fallback = &schema.Fallback{}
	}
	t.Fallback.EstimatedCostUSD = &cost
}

// AddSessionTree widens the current Claude session to its whole transcript
// tree — the main transcript plus the subagent / workflow transcripts nested
// under it — and attaches today's portion. session.tokens and
// fallback.session_tokens then share the scope of session_today, daily, and
// the cost Claude Code reports (#262); one pass over the tree serves both.
// Claude only — Codex's cumulative token_count can't be sliced to a single
// day. No-op when there's no transcript path. When the tree total is unknown
// (the main transcript or a nested one can't be read, or the main one has no
// usage yet) session.tokens and fallback.session_tokens become null: the
// collector's main-transcript figure would be a different measure, and
// unknown is never served as a wrong-semantics value (#185).
func AddSessionTree(t *schema.Tool, now time.Time, prices pricing.Table) {
	if !t.Available || t.Error != nil || t.Session == nil || t.Session.TranscriptPath == nil {
		return
	}
	path := *t.Session.TranscriptPath
	fc := cache.OpenSessionTree(strings.TrimSuffix(path, ".jsonl"))
	cum, today, ok := daily.ClaudeSessionTree(path, now, prices, fc)
	_ = fc.Save(now)
	if ok {
		t.Session.Tokens = &cum
		if t.Fallback == nil {
			t.Fallback = &schema.Fallback{}
		}
		total := cum.Total
		t.Fallback.SessionTokens = &total
	} else {
		t.Session.Tokens = nil
		if t.Fallback != nil {
			t.Fallback.SessionTokens = nil
		}
	}
	t.SessionToday = today.Schema()
}

// claudeTool prefers a fresh statusline snapshot (which carries rate limits)
// over the transcript route (which cannot see them). Once the snapshot is
// stale, the transcript route is asked too: Claude used outside the
// statusline (IDE, desktop, `claude -p`) keeps writing transcripts, so when a
// transcript is fresher its session, model, and collected_at win, and the
// snapshot's still-running rate-limit windows are carried over (#263).
//
// Outside the statusline "session" can only mean the most recently observed
// session, and that reading holds only while it is fresh. Whichever route
// serves a stale row, its session-scoped values (session, fallback,
// session_today) are dropped as unknown instead of being served next to a
// daily total that is recomputed on every call (#235). The account-level rate
// limits and model keep the snapshot's 30-day retention.
//
// The snapshot counts only when it was observed from this run's config root:
// another root is another profile, whose session and limits are not this
// one's (#321).
func claudeTool(opts Options) schema.Tool {
	root := Roots(opts)[schema.ToolClaudeCode]
	snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, opts.Now, root)
	if ok && !snap.Stale {
		return *snap
	}
	tr := claude.Collect(claude.Options{Root: opts.ClaudeRoot, Now: opts.Now})
	if ok && !fresher(tr, *snap) {
		dropSessionScope(snap)
		return *snap
	}
	if ok {
		carryLimits(&tr, *snap, opts.Now)
	}
	if tr.Stale {
		dropSessionScope(&tr)
	}
	return tr
}

// fresher reports whether the transcript route observed Claude more recently
// than the snapshot did.
func fresher(tr, snap schema.Tool) bool {
	if !tr.Available || tr.Error != nil || tr.CollectedAt == nil || snap.CollectedAt == nil {
		return false
	}
	a, errA := time.Parse(time.RFC3339, *tr.CollectedAt)
	b, errB := time.Parse(time.RFC3339, *snap.CollectedAt)
	return errA == nil && errB == nil && a.After(b)
}

// carryLimits moves the snapshot's rate limits onto the transcript route's
// tool, which cannot see them. Only between subscription sources (like the
// statusline's mergeSnapshotLimits), and only the windows RunningLimits
// keeps.
func carryLimits(tr *schema.Tool, snap schema.Tool, now time.Time) {
	if tr.Backend != schema.BackendSubscription || snap.Backend != schema.BackendSubscription {
		return
	}
	tr.Limits = RunningLimits(snap.Limits, now)
}

// RunningLimits is the subset of limits known to still be running at now:
// the windows whose reset time lies ahead. A window past its reset says
// nothing about the current one, and one without a reset time can't be shown
// to be current, so neither is carried into a fresh row — by the transcript
// route (#263) or by a statusline payload that arrived without rate limits
// (#318).
func RunningLimits(limits []schema.Limit, now time.Time) []schema.Limit {
	var kept []schema.Limit
	for _, l := range limits {
		if l.ResetsAt == nil {
			continue
		}
		if r, err := time.Parse(time.RFC3339, *l.ResetsAt); err != nil || !r.After(now) {
			continue
		}
		kept = append(kept, l)
	}
	return kept
}

// MergeLimits combines a statusline payload's windows with the ones carried
// from the snapshot (already cut to RunningLimits), one window at a time. A
// window only carried is kept, and one both have is taken from whichever was
// observed later: an idle session redrawing its statusline pushes a reading
// as old as its last API response, which must not roll back a newer one
// another session saved (#330). The payload's reading wins a tie, and
// whenever either observation time can't be read. The result keeps the
// schema's ascending window_minutes order whatever side each window came from.
func MergeLimits(live, carried []schema.Limit) []schema.Limit {
	var merged []schema.Limit
	inLive := map[string]bool{}
	for _, l := range live {
		inLive[l.Window] = true
		for _, c := range carried {
			if c.Window == l.Window && observedAfter(c, l) {
				l = c
				break
			}
		}
		merged = append(merged, l)
	}
	for _, c := range carried {
		if !inLive[c.Window] {
			merged = append(merged, c)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return windowLength(merged[i]) < windowLength(merged[j])
	})
	return merged
}

// windowLength is a limit's window_minutes for ordering; a window of unknown
// length sorts last.
func windowLength(l schema.Limit) int {
	if l.WindowMinutes == nil {
		return math.MaxInt
	}
	return *l.WindowMinutes
}

// ObservationTime is when l was observed; ok is false when its observed_at is
// missing or unreadable.
func ObservationTime(l schema.Limit) (time.Time, bool) {
	if l.ObservedAt == nil {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, *l.ObservedAt)
	return ts, err == nil
}

// observedAfter reports whether a was observed strictly later than b.
func observedAfter(a, b schema.Limit) bool {
	ta, okA := ObservationTime(a)
	tb, okB := ObservationTime(b)
	return okA && okB && ta.After(tb)
}

// dropSessionScope clears the values that describe "the current session",
// which a stale row can no longer vouch for (#235).
func dropSessionScope(t *schema.Tool) {
	t.Session = nil
	t.Fallback = nil
	t.SessionToday = nil
}

// DailyHistory is each tool's usage per local calendar day over a window,
// oldest day first. Tools maps a tool name to one entry per day, aligned
// with Days: a day without usage is a zero Daily, and a nil entry means the
// tool's logs could not be read (unknown, not zero — the same contract as
// Status's daily, #187).
type DailyHistory struct {
	Days  []string // "2006-01-02", oldest first
	Tools map[string][]*schema.Daily
}

// History aggregates the days in [from, to) (local midnights, see
// daily.DayStart) for both tools with the same accounting as Status's daily,
// so today's row equals `tacho status` and yesterday's row is what today's
// was. Nothing is cached: every call re-reads the logs (#242).
func History(opts Options, from, to time.Time) DailyHistory {
	prices := pricing.Load()
	h := DailyHistory{Tools: map[string][]*schema.Daily{}}
	for d := from; d.Before(to); d = daily.DayStartFrom(d, 1) {
		h.Days = append(h.Days, daily.DayKey(d))
	}
	for _, tool := range historyTools {
		totals, err := toolDays(tool, opts, from, to, prices)
		h.Tools[tool] = historyColumn(h.Days, totals, err)
	}
	return h
}

// historyTools are the tools History and RecentHistory aggregate, in the
// order Status lists them.
var historyTools = []string{schema.ToolClaudeCode, schema.ToolCodex}

// toolDays runs one tool's per-day aggregation over [from, to).
func toolDays(tool string, opts Options, from, to time.Time, prices pricing.Table) (map[string]daily.Totals, error) {
	if tool == schema.ToolCodex {
		return daily.CodexDays(opts.CodexRoot, from, to, prices)
	}
	return daily.ClaudeDays(opts.ClaudeRoot, from, to, prices)
}

// historyColumn lays one tool's per-day totals out along days. A scan error
// leaves every entry nil: the whole column is unknown, since a single
// unreadable file can hold any of the days.
func historyColumn(days []string, totals map[string]daily.Totals, err error) []*schema.Daily {
	col := make([]*schema.Daily, len(days))
	if err != nil {
		return col
	}
	for i, d := range days {
		col[i] = totals[d].Schema()
	}
	return col
}

// closedDayGrace is how long after local midnight a day is still treated as
// open: a line stamped just before midnight can be flushed a moment after
// it, and a day is only cached once it can no longer change.
const closedDayGrace = 10 * time.Minute

// unknownRetry is how long a closed day whose logs could not be read stays
// cached as unknown before it is recomputed, so a broken log directory costs
// one scan per hour rather than one per tick, while a transient failure
// (a transcript deleted between listing and reading) still heals.
const unknownRetry = time.Hour

// RecentHistory returns the last n local days ending today, for the SwiftBar
// dropdown (#243). Today's entries come from s (the live status) so the row
// matches the rest of the dropdown; closed days come from a rolling cache in
// the cache dir that is filled, per tool, with the days it lacks — normally
// just yesterday, once a day — and pruned to the window, so tacho never holds
// more than n-1 days of history. build identifies the binary and keys the
// cache together with the schema version and the pricing override, so a new
// build or a price change recomputes instead of serving figures `tacho daily`
// would no longer produce. Likewise the cache is tied to the config roots the
// days were read from (Roots), and each day is cached under the instants it
// spans (daySpan): after switching profiles or timezones the closed days are
// recomputed, not served from another profile or another timezone's midnights
// (#337).
func RecentHistory(opts Options, s schema.Status, n int, build string) DailyHistory {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	today := daily.DayStart(opts.Now)
	start := daily.DayStartFrom(today, -(n - 1))
	yesterday := daily.DayKey(daily.DayStartFrom(today, -1))
	inGrace := opts.Now.Sub(today) < closedDayGrace // yesterday may still get late lines
	key := build + "|" + pricing.OverrideStamp()
	roots := Roots(opts)

	// Cached closed days inside the window; older entries fall off here.
	cached, _ := cache.ReadDailyHistory(key, roots)
	closed := map[string]map[string]cache.DailyHistoryEntry{}
	for d := start; d.Before(today); d = daily.DayStartFrom(d, 1) {
		if c := cached[daySpan(d)]; c != nil {
			closed[daily.DayKey(d)] = c
		}
	}

	// Per tool, recompute from the oldest closed day it lacks — or was last
	// found unknown for long enough ago — up to today, in one pass. A day
	// still inside the grace window is computed but not stored.
	var prices pricing.Table
	store := false
	for _, tool := range historyTools {
		var missing time.Time
		for d := start; d.Before(today); d = daily.DayStartFrom(d, 1) {
			e, ok := closed[daily.DayKey(d)][tool]
			if !ok || (e.Daily == nil && retryDue(e.CheckedAt, opts.Now)) {
				missing = d
				break
			}
		}
		if missing.IsZero() {
			continue
		}
		if prices == nil {
			prices = pricing.Load()
		}
		totals, err := toolDays(tool, opts, missing, today, prices)
		for d := missing; d.Before(today); d = daily.DayStartFrom(d, 1) {
			day := daily.DayKey(d)
			e := cache.DailyHistoryEntry{CheckedAt: opts.Now.Format(time.RFC3339)}
			if err == nil {
				e.Daily = totals[day].Schema()
			}
			if closed[day] == nil {
				closed[day] = map[string]cache.DailyHistoryEntry{}
			}
			closed[day][tool] = e
			store = store || !(inGrace && day == yesterday)
		}
	}
	if store {
		keep := map[string]map[string]cache.DailyHistoryEntry{}
		for d := start; d.Before(today); d = daily.DayStartFrom(d, 1) {
			day := daily.DayKey(d)
			if c := closed[day]; c != nil && !(inGrace && day == yesterday) {
				keep[daySpan(d)] = c
			}
		}
		_ = cache.WriteDailyHistory(key, roots, keep) // serving the live result matters more than caching it
	}

	out := DailyHistory{Tools: map[string][]*schema.Daily{}}
	for d := start; !d.After(today); d = daily.DayStartFrom(d, 1) {
		out.Days = append(out.Days, daily.DayKey(d))
	}
	for _, tool := range historyTools {
		col := make([]*schema.Daily, len(out.Days))
		for i, day := range out.Days[:len(out.Days)-1] {
			col[i] = closed[day][tool].Daily
		}
		col[len(col)-1] = todayDaily(s, tool)
		out.Tools[tool] = col
	}
	return out
}

// daySpan is the history cache's key for the local day d falls on: the
// instants the day starts and ends, offsets included. The days are cut at
// local midnights (daily.DayKey), so a day cached under another timezone
// covers other instants and is recomputed rather than served under the same
// date (#337).
func daySpan(d time.Time) string {
	start := daily.DayStart(d)
	end := daily.DayStartFrom(start, 1)
	return start.Format(time.RFC3339) + "/" + end.Format(time.RFC3339)
}

// retryDue reports whether an unknown entry checked at checkedAt is old
// enough to recompute; an unparsable time is retried right away.
func retryDue(checkedAt string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, checkedAt)
	return err != nil || now.Sub(t) >= unknownRetry
}

// todayDaily is the tool's daily total as the status carries it: nil when
// the tool is absent, errored, or its total is unknown.
func todayDaily(s schema.Status, tool string) *schema.Daily {
	for _, t := range s.Tools {
		if t.Tool == tool && t.Available && t.Error == nil {
			return t.Daily
		}
	}
	return nil
}
