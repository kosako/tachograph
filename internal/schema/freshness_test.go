package schema

import (
	"testing"
	"time"
)

func rfc(t time.Time) *string {
	s := t.Format(time.RFC3339)
	return &s
}

// The stale threshold follows the tool: Codex's observations stay usable for
// five hours (its windows outlive its last turn), Claude's for one.
func TestStaleAfterFollowsTool(t *testing.T) {
	if got := StaleAfter(ToolCodex); got != 5*time.Hour {
		t.Errorf("codex StaleAfter = %v, want 5h", got)
	}
	if got := StaleAfter(ToolClaudeCode); got != time.Hour {
		t.Errorf("claude StaleAfter = %v, want 1h", got)
	}
}

// A limit observed longer ago than its tool's stale threshold is an old
// reading; one without a readable observation time has no known age (#331).
func TestObservedStale(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	bad := "not-a-time"
	cases := []struct {
		name     string
		tool     string
		observed *string
		want     bool
	}{
		{name: "claude within the hour", tool: ToolClaudeCode, observed: rfc(now.Add(-60 * time.Minute)), want: false},
		{name: "claude past the hour", tool: ToolClaudeCode, observed: rfc(now.Add(-61 * time.Minute)), want: true},
		{name: "codex within 5 hours", tool: ToolCodex, observed: rfc(now.Add(-300 * time.Minute)), want: false},
		{name: "codex past 5 hours", tool: ToolCodex, observed: rfc(now.Add(-301 * time.Minute)), want: true},
		{name: "no observation time", tool: ToolClaudeCode, observed: nil, want: false},
		{name: "unreadable observation time", tool: ToolClaudeCode, observed: &bad, want: false},
	}
	for _, c := range cases {
		if got := ObservedStale(c.tool, Limit{Window: WindowWeekly, ObservedAt: c.observed}, now); got != c.want {
			t.Errorf("%s: ObservedStale = %v, want %v", c.name, got, c.want)
		}
	}
}

// A window has ended once its reset time is not after now; one without a
// readable reset time can't be shown to have ended (#374).
func TestResetPassed(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	bad := "not-a-time"
	cases := []struct {
		name   string
		resets *string
		want   bool
	}{
		{name: "reset ahead", resets: rfc(now.Add(time.Second)), want: false},
		{name: "reset exactly now", resets: rfc(now), want: true},
		{name: "reset behind", resets: rfc(now.Add(-time.Minute)), want: true},
		{name: "no reset time", resets: nil, want: false},
		{name: "unreadable reset time", resets: &bad, want: false},
	}
	for _, c := range cases {
		if got := ResetPassed(Limit{Window: WindowFiveHour, ResetsAt: c.resets}, now); got != c.want {
			t.Errorf("%s: ResetPassed = %v, want %v", c.name, got, c.want)
		}
	}
}
