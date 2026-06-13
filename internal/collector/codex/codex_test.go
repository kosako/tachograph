package codex

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func TestCollect(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-24T13:45:00Z")
	got := Collect(Options{Root: "testdata/codexroot", Now: now})

	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v, want available with no error", got.Available, got.Error)
	}
	if got.Tool != schema.ToolCodex {
		t.Errorf("Tool = %q", got.Tool)
	}
	if got.Backend != schema.BackendSubscription {
		t.Errorf("Backend = %q, want subscription (plan_type present)", got.Backend)
	}
	if got.Plan == nil || *got.Plan != "prolite" {
		t.Errorf("Plan = %v, want prolite", got.Plan)
	}
	if got.Model == nil || got.Model.ID != "gpt-5.4-codex" {
		t.Errorf("Model = %+v, want gpt-5.4-codex", got.Model)
	}
	if got.Stale {
		t.Error("Stale = true, want false (event ~5min before now)")
	}

	s := got.Session
	if s == nil {
		t.Fatal("Session is nil")
	}
	if s.ID == nil || *s.ID != "019e5933-2289-7e72-88fd-c494201693fa" {
		t.Errorf("Session.ID = %v", s.ID)
	}
	if s.CWD == nil || *s.CWD != "/Users/example/dev/project" {
		t.Errorf("Session.CWD = %v", s.CWD)
	}
	if s.ContextWindow == nil || *s.ContextWindow != 258400 {
		t.Errorf("ContextWindow = %v", s.ContextWindow)
	}
	if s.Tokens == nil || s.Tokens.Total != 989120 || s.Tokens.CachedInput != 803712 {
		t.Errorf("Tokens = %+v", s.Tokens)
	}
	// last_token_usage.total / context_window = 68190/258400 ≈ 26.39%
	if s.ContextUsedPct == nil || *s.ContextUsedPct < 26.3 || *s.ContextUsedPct > 26.5 {
		t.Errorf("ContextUsedPct = %v, want ≈26.39", s.ContextUsedPct)
	}

	if len(got.Limits) != 2 {
		t.Fatalf("Limits = %+v, want 2 entries", got.Limits)
	}
	five, weekly := got.Limits[0], got.Limits[1]
	if five.Window != schema.WindowFiveHour || *five.UsedPct != 5.0 || *five.WindowMinutes != 300 {
		t.Errorf("5h limit = %+v", five)
	}
	if weekly.Window != schema.WindowWeekly || *weekly.UsedPct != 4.0 || *weekly.WindowMinutes != 10080 {
		t.Errorf("weekly limit = %+v", weekly)
	}
	wantReset := time.Unix(1779646858, 0).Local().Format(time.RFC3339)
	if five.ResetsAt == nil || *five.ResetsAt != wantReset {
		t.Errorf("5h ResetsAt = %v, want %s", five.ResetsAt, wantReset)
	}

	if got.Fallback == nil || got.Fallback.SessionTokens == nil || *got.Fallback.SessionTokens != 989120 {
		t.Errorf("Fallback = %+v", got.Fallback)
	}
}

func TestCollectStale(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-24T16:00:00Z") // ~2h20m after the event
	got := Collect(Options{Root: "testdata/codexroot", Now: now})
	if !got.Stale {
		t.Error("Stale = false, want true for hours-old data")
	}
}

func TestCollectNoSessions(t *testing.T) {
	got := Collect(Options{Root: "testdata/emptyroot"})
	if got.Available {
		t.Errorf("Available = true, want false: %+v", got)
	}
	if got.Error != nil {
		t.Errorf("Error = %+v, want nil (missing data source is not an error)", got.Error)
	}
}

// TestCollectRealHome runs against the real ~/.codex. Opt-in:
//
//	TACHO_E2E=1 go test ./internal/collector/codex -run RealHome -v
func TestCollectRealHome(t *testing.T) {
	if os.Getenv("TACHO_E2E") == "" {
		t.Skip("set TACHO_E2E=1 to run against the real ~/.codex")
	}
	got := Collect(Options{})
	b, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("real ~/.codex result:\n%s", b)
	if got.Error != nil {
		t.Errorf("Error = %+v", got.Error)
	}
}

func TestWindowName(t *testing.T) {
	cases := map[int]string{300: "5h", 10080: "weekly", 60: "1h", 90: "90m"}
	for mins, want := range cases {
		if got := windowName(mins); got != want {
			t.Errorf("windowName(%d) = %q, want %q", mins, got, want)
		}
	}
}
