package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	// statusline has no session-total cache field, so the per-turn
	// current_usage.cache_read (2000 in testdata) must not leak in.
	if s.Tokens.CachedInput != 0 {
		t.Errorf("CachedInput = %d, want 0 (no session-total cache via statusline)", s.Tokens.CachedInput)
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

// ANTHROPIC_API_KEY marks pay-as-you-go API usage: backend=api and the
// subscription rate-limit windows must be suppressed (they don't apply).
func TestFromStatuslineAPIBackend(t *testing.T) {
	input, _ := os.ReadFile("testdata/statusline_input.json")
	env := func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk-ant-xxx"
		}
		return ""
	}
	got := Collect(Options{Root: "testdata/clauderoot", StatuslineInput: input, Getenv: env})
	if got.Backend != schema.BackendAPI {
		t.Errorf("Backend = %q, want api", got.Backend)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want null for api backend", got.Limits)
	}
}

// writeTranscript writes a one-line transcript and stamps its mtime so tests
// can control recency ordering deterministically.
func writeTranscript(t *testing.T, dir, name, line string, mod time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// The newest transcript may have no assistant usage yet; collection must fall
// back to the next most recent transcript that does.
func TestTranscriptFallbackPastEmpty(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := `{"timestamp":"2026-06-12T12:00:00Z","sessionId":"old","cwd":"/x","message":{"model":"claude-x","usage":{"input_tokens":10,"output_tokens":5}}}`
	writeTranscript(t, dir, "old.jsonl", older, time.Unix(1000, 0))
	// Newest entry carries no usage (e.g. a fresh user turn).
	writeTranscript(t, dir, "new.jsonl", `{"timestamp":"2026-06-12T12:01:00Z","sessionId":"new","type":"user"}`, time.Unix(2000, 0))

	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	got := Collect(Options{Root: root, Now: now, Getenv: noEnv})
	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Session == nil || got.Session.Tokens == nil ||
		got.Session.Tokens.Input != 10 || got.Session.Tokens.Output != 5 {
		t.Fatalf("Tokens = %+v, want fallback to older transcript", got.Session.Tokens)
	}
	if got.Session.ID == nil || *got.Session.ID != "old" {
		t.Errorf("Session.ID = %v, want \"old\" after fallback", got.Session.ID)
	}
}

// When no transcript has any usage, surface a no_usage error (not a panic or
// a false-available zero result).
func TestTranscriptNoUsage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, dir, "a.jsonl", `{"timestamp":"2026-06-12T12:00:00Z","type":"user"}`, time.Unix(1000, 0))

	got := Collect(Options{Root: root, Getenv: noEnv})
	if got.Error == nil || got.Error.Code != "no_usage" {
		t.Fatalf("Error = %+v, want code no_usage", got.Error)
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
