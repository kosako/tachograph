package claude

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func noEnv(string) string { return "" }

// toLimit must keep an absent/zero resets_at as null, not 1970-01-01.
func TestToLimitNullResetsAt(t *testing.T) {
	l := toLimit(schema.WindowFiveHour, 300, &slWindow{UsedPercentage: 5, ResetsAt: 0})
	if l.ResetsAt != nil {
		t.Errorf("ResetsAt = %v, want nil for zero epoch", *l.ResetsAt)
	}
	l = toLimit(schema.WindowFiveHour, 300, &slWindow{UsedPercentage: 5, ResetsAt: 1779646858})
	if l.ResetsAt == nil {
		t.Error("ResetsAt = nil, want set for a real epoch")
	}
}

func TestFromStatusline(t *testing.T) {
	input, err := os.ReadFile("testdata/statusline_input.json")
	if err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := Collect(Options{Root: "testdata/clauderoot", Now: now, StatuslineInput: input, Getenv: noEnv})

	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Backend != schema.BackendSubscription {
		t.Errorf("Backend = %q", got.Backend)
	}
	if got.Model == nil || got.Model.ID != "claude-fable-5" || *got.Model.DisplayName != "Fable 5" {
		t.Errorf("Model = %+v", got.Model)
	}
	if got.Model.Effort == nil || *got.Model.Effort != "xhigh" {
		t.Errorf("Model.Effort = %v, want \"xhigh\"", got.Model.Effort)
	}
	if got.Stale {
		t.Error("Stale = true; statusline input is live")
	}

	s := got.Session
	if s == nil || s.ID == nil || *s.ID != "abc12345-1234-5678-9abc-def012345678" {
		t.Fatalf("Session = %+v", s)
	}
	if s.ContextWindow == nil || *s.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %v", s.ContextWindow)
	}
	if s.ContextUsedPct == nil || *s.ContextUsedPct != 8 {
		t.Errorf("ContextUsedPct = %v", s.ContextUsedPct)
	}
	if s.Tokens == nil || s.Tokens.Input != 15500 || s.Tokens.Output != 1200 {
		t.Errorf("Tokens = %+v", s.Tokens)
	}

	if len(got.Limits) != 2 {
		t.Fatalf("Limits = %+v", got.Limits)
	}
	five, weekly := got.Limits[0], got.Limits[1]
	if five.Window != schema.WindowFiveHour || *five.UsedPct != 23.5 || *five.WindowMinutes != 300 {
		t.Errorf("5h = %+v", five)
	}
	wantReset := time.Unix(1781258400, 0).Local().Format(time.RFC3339)
	if *five.ResetsAt != wantReset {
		t.Errorf("5h ResetsAt = %v, want %s", *five.ResetsAt, wantReset)
	}
	if weekly.Window != schema.WindowWeekly || *weekly.UsedPct != 41.2 || *weekly.WindowMinutes != 10080 {
		t.Errorf("weekly = %+v", weekly)
	}

	if got.Fallback == nil || got.Fallback.EstimatedCostUSD == nil || *got.Fallback.EstimatedCostUSD != 0.01234 {
		t.Errorf("Fallback = %+v", got.Fallback)
	}
}

func TestFromStatuslineBedrockDegradesLimits(t *testing.T) {
	input, _ := os.ReadFile("testdata/statusline_input.json")
	env := func(k string) string {
		if k == "CLAUDE_CODE_USE_BEDROCK" {
			return "1"
		}
		return ""
	}
	got := Collect(Options{Root: "testdata/clauderoot", StatuslineInput: input, Getenv: env})
	if got.Backend != schema.BackendBedrock {
		t.Errorf("Backend = %q, want bedrock", got.Backend)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want null on bedrock", got.Limits)
	}
	if got.Fallback == nil || got.Fallback.SessionTokens == nil {
		t.Errorf("Fallback = %+v, want session tokens for degraded display", got.Fallback)
	}
}

func TestFromTranscripts(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	got := Collect(Options{Root: "testdata/clauderoot", Now: now, Getenv: noEnv})

	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Model == nil || got.Model.ID != "claude-fable-5" {
		t.Errorf("Model = %+v", got.Model)
	}
	if got.Model.Effort != nil {
		t.Errorf("Model.Effort = %v, want nil via transcript route", *got.Model.Effort)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want null via transcript route", got.Limits)
	}
	s := got.Session
	if s == nil || s.Tokens == nil {
		t.Fatalf("Session = %+v", s)
	}
	// input = (100+5000+30000) + (2+673+39451) = 75226, output = 881
	if s.Tokens.Input != 75226 || s.Tokens.CachedInput != 69451 || s.Tokens.Output != 881 {
		t.Errorf("Tokens = %+v", s.Tokens)
	}
	if got.Stale {
		t.Error("Stale = true, want false (last entry 3.5min before now)")
	}
}

func TestNoTranscripts(t *testing.T) {
	got := Collect(Options{Root: t.TempDir(), Getenv: noEnv})
	if got.Available {
		t.Errorf("Available = true, want false: %+v", got)
	}
}

// TACHO_E2E=1 go test ./internal/collector/claude -run RealHome -v
func TestCollectRealHome(t *testing.T) {
	if os.Getenv("TACHO_E2E") == "" {
		t.Skip("set TACHO_E2E=1 to run against the real ~/.claude")
	}
	got := Collect(Options{})
	b, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("real ~/.claude result:\n%s", b)
	if got.Error != nil {
		t.Errorf("Error = %+v", got.Error)
	}
}
