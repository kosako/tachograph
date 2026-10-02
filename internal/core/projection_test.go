package core

import (
	"math"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/collector/codex"
	"github.com/kosako/tachograph/internal/schema"
)

func rfc(t time.Time) *string {
	s := t.Format(time.RFC3339)
	return &s
}

func fptr(v float64) *float64 { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// A 5h window observed 60 minutes in (it resets 240 minutes after the
// observation) at 20% used: the average pace is 20 points an hour, which
// reaches exactly 100% at the reset.
func TestProjectWindowAverage(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	observed := now.Add(-time.Minute)
	mins := 300
	l := schema.Limit{
		Window: schema.WindowFiveHour, WindowMinutes: &mins, UsedPct: fptr(20),
		ResetsAt: rfc(observed.Add(240 * time.Minute)), ObservedAt: rfc(observed),
	}
	p := project(l, now, time.Hour)
	if p.Method != schema.ProjectionWindowAverage || p.UnavailableReason != nil {
		t.Fatalf("projection = %+v, want available window_average", p)
	}
	if p.ElapsedPctAtObservation == nil || !near(*p.ElapsedPctAtObservation, 20) {
		t.Errorf("elapsed = %v, want 20", p.ElapsedPctAtObservation)
	}
	if p.PacePctPerHour == nil || !near(*p.PacePctPerHour, 20) {
		t.Errorf("pace = %v, want 20 points/hour", p.PacePctPerHour)
	}
	if p.UsedPctAtReset == nil || !near(*p.UsedPctAtReset, 100) {
		t.Errorf("used at reset = %v, want 100", p.UsedPctAtReset)
	}
	if p.HeadroomPctAtReset == nil || !near(*p.HeadroomPctAtReset, 0) {
		t.Errorf("headroom = %v, want 0", p.HeadroomPctAtReset)
	}
}

// Zero use is a valid result (zero pace, full headroom), and an average that
// would overrun the window is reported as more than 100% used and negative
// headroom rather than capped: it says how much the pace would need.
func TestProjectZeroAndOverrun(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	observed := now.Add(-time.Minute)
	mins := 300
	base := schema.Limit{
		Window: schema.WindowFiveHour, WindowMinutes: &mins,
		ResetsAt: rfc(observed.Add(270 * time.Minute)), ObservedAt: rfc(observed), // 30 min in = 10%
	}
	zero := base
	zero.UsedPct = fptr(0)
	if p := project(zero, now, time.Hour); p.UnavailableReason != nil || *p.PacePctPerHour != 0 || *p.UsedPctAtReset != 0 || *p.HeadroomPctAtReset != 100 {
		t.Errorf("zero use: %+v, want pace 0 / used 0 / headroom 100", p)
	}
	heavy := base
	heavy.UsedPct = fptr(20) // 20% in a tenth of the window → 200% at reset
	if p := project(heavy, now, time.Hour); p.UnavailableReason != nil || !near(*p.UsedPctAtReset, 200) || !near(*p.HeadroomPctAtReset, -100) {
		t.Errorf("overrun: %+v, want used 200 / headroom -100 (not capped)", p)
	}
	if p := project(heavy, now, time.Hour); !near(*p.ElapsedPctAtObservation, 10) {
		t.Errorf("elapsed = %v, want 10 (a shallow window is reported, not withheld)", *p.ElapsedPctAtObservation)
	}
}

func TestProjectUnavailableReasons(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	observed := now.Add(-time.Minute)
	mins, zeroMins := 300, 0
	sound := schema.Limit{
		Window: schema.WindowFiveHour, WindowMinutes: &mins, UsedPct: fptr(20),
		ResetsAt: rfc(observed.Add(240 * time.Minute)), ObservedAt: rfc(observed),
	}
	cases := []struct {
		name     string
		mutate   func(l *schema.Limit)
		stale    time.Duration
		reason   string
		elapsed  bool // the elapsed share survives when only the use is at fault
		observed time.Time
	}{
		{name: "no window length", mutate: func(l *schema.Limit) { l.WindowMinutes = nil }, stale: time.Hour, reason: schema.ProjectionMissingInput},
		{name: "no reset time", mutate: func(l *schema.Limit) { l.ResetsAt = nil }, stale: time.Hour, reason: schema.ProjectionMissingInput},
		{name: "no observation time", mutate: func(l *schema.Limit) { l.ObservedAt = nil }, stale: time.Hour, reason: schema.ProjectionMissingInput},
		{name: "unparsable reset", mutate: func(l *schema.Limit) { s := "soon"; l.ResetsAt = &s }, stale: time.Hour, reason: schema.ProjectionMissingInput},
		{name: "no use", mutate: func(l *schema.Limit) { l.UsedPct = nil }, stale: time.Hour, reason: schema.ProjectionMissingInput, elapsed: true},
		{name: "zero-length window", mutate: func(l *schema.Limit) { l.WindowMinutes = &zeroMins }, stale: time.Hour, reason: schema.ProjectionInvalidWindow},
		{name: "observed before the window began", mutate: func(l *schema.Limit) { l.ObservedAt = rfc(observed.Add(-61 * time.Minute)) }, stale: time.Hour, reason: schema.ProjectionInvalidWindow},
		{name: "observed after the reset", mutate: func(l *schema.Limit) { l.ResetsAt = rfc(observed.Add(-time.Minute)) }, stale: time.Hour, reason: schema.ProjectionInvalidWindow},
		{name: "saved resets", mutate: func(l *schema.Limit) { l.SavedResets = map[string]any{"x": 1} }, stale: time.Hour, reason: schema.ProjectionUnsupportedWindow, elapsed: true},
		{name: "reset passed", mutate: func(l *schema.Limit) {
			l.ObservedAt = rfc(now.Add(-200 * time.Minute))
			l.ResetsAt = rfc(now.Add(-10 * time.Minute)) // observed 90 min into a window that has since reset
		}, stale: 24 * time.Hour, reason: schema.ProjectionResetPassed, elapsed: true},
		{name: "observation older than the stale threshold", mutate: func(l *schema.Limit) {
			l.ObservedAt = rfc(now.Add(-61 * time.Minute))
			l.ResetsAt = rfc(now.Add(179 * time.Minute)) // still 60 min into the window at observation
		}, stale: time.Hour, reason: schema.ProjectionStale, elapsed: true},
	}
	for _, c := range cases {
		l := sound
		c.mutate(&l)
		p := project(l, now, c.stale)
		if p.UnavailableReason == nil || *p.UnavailableReason != c.reason {
			t.Errorf("%s: reason = %v, want %s (%+v)", c.name, p.UnavailableReason, c.reason, p)
			continue
		}
		if p.PacePctPerHour != nil || p.UsedPctAtReset != nil || p.HeadroomPctAtReset != nil {
			t.Errorf("%s: figures present on an unavailable projection: %+v", c.name, p)
		}
		if (p.ElapsedPctAtObservation != nil) != c.elapsed {
			t.Errorf("%s: elapsed present = %v, want %v", c.name, p.ElapsedPctAtObservation != nil, c.elapsed)
		}
		if p.Method != schema.ProjectionWindowAverage {
			t.Errorf("%s: method = %q, want window_average even when unavailable", c.name, p.Method)
		}
	}
}

// The stale threshold follows the tool: Codex's observations stay usable for
// five hours (its windows outlive its last turn), Claude's for one.
func TestStaleAfterFollowsTool(t *testing.T) {
	if staleAfter(schema.ToolCodex) != codex.StaleAfterMinutes*time.Minute {
		t.Errorf("codex staleAfter = %v", staleAfter(schema.ToolCodex))
	}
	if staleAfter(schema.ToolClaudeCode) != schema.StaleAfterMinutes*time.Minute {
		t.Errorf("claude staleAfter = %v", staleAfter(schema.ToolClaudeCode))
	}
}

// End to end through Status: a snapshot's limits come out projected, and a
// tool without limits is left alone.
func TestStatusProjectsSnapshotLimits(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnv(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	observed := now.Add(-time.Minute)
	mins := 300
	collected := observed.Format(time.RFC3339)
	snap := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits: []schema.Limit{{
			Window: schema.WindowFiveHour, WindowMinutes: &mins, UsedPct: fptr(20),
			ResetsAt: rfc(observed.Add(240 * time.Minute)), ObservedAt: rfc(observed),
		}},
	}
	if err := cache.WriteSnapshot(snap, observed, claudeRoot); err != nil {
		t.Fatal(err)
	}
	s := Status(Options{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, Now: now, NoCache: true})
	got := s.Tools[0]
	if len(got.Limits) != 1 {
		t.Fatalf("Limits = %+v, want the snapshot's window", got.Limits)
	}
	p := got.Limits[0].Projection
	if p.UnavailableReason != nil || p.UsedPctAtReset == nil || !near(*p.UsedPctAtReset, 100) {
		t.Errorf("projection = %+v, want 100%% at reset", p)
	}
	for _, l := range s.Tools[1].Limits { // the codex fixture's windows reset in 2026-05, long before now
		if l.Projection.Method != schema.ProjectionWindowAverage || l.Projection.UnavailableReason == nil {
			t.Errorf("codex %s projection = %+v, want window_average with a reason", l.Window, l.Projection)
		}
	}
}
