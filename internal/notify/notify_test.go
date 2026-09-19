package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

func limitsTool(name string, stale bool, used5, usedW float64, resets5 string) schema.Tool {
	t := schema.Tool{Tool: name, Available: true, Stale: stale, Limits: []schema.Limit{
		{Window: schema.WindowFiveHour, UsedPct: &used5},
		{Window: schema.WindowWeekly, UsedPct: &usedW},
	}}
	if resets5 != "" {
		t.Limits[0].ResetsAt = &resets5
	}
	return t
}

func status(tools ...schema.Tool) schema.Status { return schema.Status{Tools: tools} }

// A window fires once per threshold per cycle: crossing 50% announces once,
// staying below it stays quiet, crossing 30% announces again, rising back
// above 30% re-arms it, and a new reset time re-arms everything.
func TestEvaluateFiresOncePerThresholdPerCycle(t *testing.T) {
	th := []int{50, 30, 10}
	r1 := "2026-09-20T03:00:00+09:00"

	ev, st := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 55, 10, r1)), th, State{})
	if len(ev) != 1 || ev[0].Key != "claude-code/5h" || ev[0].Threshold != 50 || ev[0].Remaining != 45 {
		t.Fatalf("first crossing: events = %+v", ev)
	}
	if ev, _ = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 58, 10, r1)), th, st); len(ev) != 0 {
		t.Errorf("still below 50%%: events = %+v, want none", ev)
	}
	ev, st = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 72, 10, r1)), th, st)
	if len(ev) != 1 || ev[0].Threshold != 30 {
		t.Fatalf("crossing 30%%: events = %+v", ev)
	}
	// Back above 30% (but below 50%): 30 re-arms, 50 stays announced.
	_, st = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 60, 10, r1)), th, st)
	ev, st = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 75, 10, r1)), th, st)
	if len(ev) != 1 || ev[0].Threshold != 30 {
		t.Fatalf("re-crossing 30%% after re-arm: events = %+v", ev)
	}
	// New cycle: the reset time changed, so 50 fires again.
	ev, _ = Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 55, 10, "2026-09-20T08:00:00+09:00")), th, st)
	if len(ev) != 1 || ev[0].Threshold != 50 {
		t.Errorf("new cycle: events = %+v, want 50 again", ev)
	}
}

// Dropping past several thresholds at once announces the deepest one only,
// and marks all of them so none fires later in the same cycle.
func TestEvaluateCollapsesMultipleCrossings(t *testing.T) {
	th := []int{50, 30, 10}
	ev, st := Evaluate(status(limitsTool(schema.ToolCodex, false, 95, 0, "")), th, State{})
	if len(ev) != 1 || ev[0].Threshold != 10 || ev[0].Remaining != 5 {
		t.Fatalf("events = %+v, want one event at 10", ev)
	}
	if got := st["codex/5h"].Notified; len(got) != 3 {
		t.Errorf("Notified = %v, want all three thresholds", got)
	}
	if ev, _ = Evaluate(status(limitsTool(schema.ToolCodex, false, 96, 0, "")), th, st); len(ev) != 0 {
		t.Errorf("events = %+v after collapse, want none", ev)
	}
}

// Stale, unavailable, and errored tools are skipped and keep their record;
// no thresholds means nothing fires; the input state is never mutated.
func TestEvaluateSkipsAndPreservesState(t *testing.T) {
	th := []int{50}
	_, st := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 60, 0, "")), th, State{})
	before := append([]int(nil), st["claude-code/5h"].Notified...)

	ev, next := Evaluate(status(limitsTool(schema.ToolClaudeCode, true, 99, 99, "")), th, st)
	if len(ev) != 0 || len(next["claude-code/5h"].Notified) != 1 {
		t.Errorf("stale tool: events = %+v, state = %+v, want none and the record kept", ev, next)
	}
	errTool := schema.Unavailable(schema.ToolCodex)
	errTool.Available = true
	errTool.Error = &schema.Error{Code: "x"}
	if ev, _ := Evaluate(status(schema.Unavailable(schema.ToolClaudeCode), errTool), th, State{}); len(ev) != 0 {
		t.Errorf("unavailable / errored tools: events = %+v, want none", ev)
	}
	if ev, _ := Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 99, 99, "")), nil, State{}); len(ev) != 0 {
		t.Errorf("no thresholds: events = %+v, want none", ev)
	}
	// Only the 5h and weekly windows are watched: a collector-reported odd
	// window size (e.g. Codex with an unexpected window_minutes) is ignored.
	used := 99.0
	odd := schema.Tool{Tool: schema.ToolCodex, Available: true, Limits: []schema.Limit{{Window: "6h", UsedPct: &used}}}
	if ev, st := Evaluate(status(odd), th, State{}); len(ev) != 0 || len(st) != 0 {
		t.Errorf("odd window: events = %+v, state = %+v, want none", ev, st)
	}
	// Re-arm through the same state must not have touched the caller's copy.
	Evaluate(status(limitsTool(schema.ToolClaudeCode, false, 10, 0, "")), th, st)
	if got := st["claude-code/5h"].Notified; len(got) != len(before) || got[0] != before[0] {
		t.Errorf("input State mutated: %v, want %v", got, before)
	}
}

func TestBodyAndURL(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-09-19T10:00:00+09:00")
	resets := "2026-09-19T14:30:00+09:00"
	ev := Event{Tool: schema.ToolClaudeCode, Window: schema.WindowWeekly, Remaining: 28.4, Threshold: 30, ResetsAt: &resets}
	// The reset time renders in the runner's local zone (render.ResetShort);
	// build the expectation the same way so this holds on any CI timezone.
	if got, want := Body(ev, now), "Claude weekly: 28% left · resets "+render.ResetShort(resets, now); got != want {
		t.Errorf("Body = %q, want %q", got, want)
	}
	ev.ResetsAt = nil
	ev.Tool = schema.ToolCodex
	if got := Body(ev, now); got != "Codex weekly: 28% left" {
		t.Errorf("Body without reset = %q", got)
	}
	u := URL(ev, "tacho.30s.sh", now)
	if !strings.HasPrefix(u, "swiftbar://notify?") || !strings.Contains(u, "plugin=tacho.30s.sh") ||
		!strings.Contains(u, "body=Codex%20weekly%3A%2028%25%20left") || strings.Contains(u, "+") {
		t.Errorf("URL = %q", u)
	}
}

// Run delivers each event and keeps a failed delivery's window un-announced
// so it is retried next tick.
func TestRunRetriesFailedDelivery(t *testing.T) {
	th := []int{50}
	s := status(limitsTool(schema.ToolClaudeCode, false, 60, 0, ""), limitsTool(schema.ToolCodex, false, 70, 0, ""))
	var sent []string
	fail := func(u string) error {
		sent = append(sent, u)
		if strings.Contains(u, "Codex") {
			return errors.New("open failed")
		}
		return nil
	}
	st := Run(s, th, State{}, "tacho.30s.sh", time.Now(), fail)
	if len(sent) != 2 {
		t.Fatalf("sent %d notifications, want 2", len(sent))
	}
	if _, ok := st["codex/5h"]; ok {
		t.Errorf("failed delivery recorded: %+v", st["codex/5h"])
	}
	if len(st["claude-code/5h"].Notified) != 1 {
		t.Errorf("successful delivery not recorded: %+v", st)
	}
	sent = nil
	Run(s, th, st, "tacho.30s.sh", time.Now(), fail)
	if len(sent) != 1 || !strings.Contains(sent[0], "Codex") {
		t.Errorf("retry sent %v, want only the Codex window again", sent)
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	if got := LoadState(); len(got) != 0 {
		t.Fatalf("LoadState on empty cache = %v", got)
	}
	st := State{"claude-code/5h": {ResetsAt: "r", Notified: []int{50, 30}}}
	if err := SaveState(st); err != nil {
		t.Fatal(err)
	}
	got := LoadState()
	if w := got["claude-code/5h"]; w.ResetsAt != "r" || len(w.Notified) != 2 {
		t.Errorf("LoadState = %+v", got)
	}
}
