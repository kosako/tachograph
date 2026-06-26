package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	now, _ := time.Parse(time.RFC3339, "2026-05-24T19:00:00Z") // ~5h20m after the event, past Codex's 5h threshold
	got := Collect(Options{Root: "testdata/codexroot", Now: now})
	if !got.Stale {
		t.Error("Stale = false, want true past the 5h threshold")
	}
}

// Codex uses a longer (5h) stale threshold than the shared 60-minute default,
// because its rate-limit windows stay valid for hours after the last turn.
// Data 2h20m old is past the 60-min default but still fresh for Codex.
func TestCollectFreshWithinFiveHours(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-24T16:00:00Z") // ~2h20m after the event
	got := Collect(Options{Root: "testdata/codexroot", Now: now})
	if got.Stale {
		t.Error("Stale = true, want false within Codex's 5h threshold")
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

// latestSessionFile must backtrack past an empty newest day/month to the most
// recent day that actually holds a .jsonl (e.g. around date rollover).
func TestLatestSessionFileBacktracksEmptyBranches(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	dataDay := filepath.Join(sessions, "2026", "05", "24")
	if err := os.MkdirAll(dataDay, 0o755); err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(dataDay, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-c494201693fa.jsonl")
	if err := os.WriteFile(dataFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Newer-but-empty branches that must be skipped: a later day, a later
	// month, and a later year, all without any .jsonl.
	for _, empty := range []string{
		filepath.Join(sessions, "2026", "05", "25"),
		filepath.Join(sessions, "2026", "06"),
		filepath.Join(sessions, "2027"),
	} {
		if err := os.MkdirAll(empty, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := latestSessionFile(sessions); got != dataFile {
		t.Errorf("latestSessionFile = %q, want %q (should backtrack past empty newest branches)", got, dataFile)
	}
}

// When a single turn is longer than the tail window, turn_context (emitted at
// turn start) falls outside the tail while token_count (turn end) stays in it.
// Collect must full-scan so model/cwd don't silently go null.
func TestCollectTurnContextBeyondTail(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "05", "24")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	b = append(b, []byte(`{"timestamp":"2026-05-24T10:00:00.000Z","type":"turn_context","payload":{"cwd":"/tmp/work","model":"gpt-5.4-codex"}}`+"\n")...)
	// Filler well beyond tailBytes (512KB) between turn_context and token_count.
	filler := `{"timestamp":"2026-05-24T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + string(bytes.Repeat([]byte("x"), 1000)) + `"}]}}` + "\n"
	for total := 0; total < tailBytes+50*1024; total += len(filler) {
		b = append(b, filler...)
	}
	b = append(b, []byte(`{"timestamp":"2026-05-24T10:05:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":50,"total_tokens":150},"model_context_window":200000}}}`+"\n")...)
	file := filepath.Join(day, "rollout-2026-05-24T10-00-00-019e5933-2289-7e72-88fd-c494201693fa.jsonl")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}

	now, _ := time.Parse(time.RFC3339, "2026-05-24T10:06:00Z")
	got := Collect(Options{Root: root, Now: now})
	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Model == nil || got.Model.ID != "gpt-5.4-codex" {
		t.Errorf("Model = %+v, want gpt-5.4-codex (turn_context past the tail must be recovered by full scan)", got.Model)
	}
	if got.Session == nil || got.Session.CWD == nil || *got.Session.CWD != "/tmp/work" {
		t.Errorf("CWD = %v, want /tmp/work", got.Session.CWD)
	}
}

// toLimit must keep an absent/zero resets_at as null, not 1970-01-01.
func TestToLimitNullResetsAt(t *testing.T) {
	l := toLimit(&rlWindow{WindowMinutes: 300, UsedPercent: 5, ResetsAt: 0})
	if l.ResetsAt != nil {
		t.Errorf("ResetsAt = %v, want nil for zero epoch", *l.ResetsAt)
	}
	l = toLimit(&rlWindow{WindowMinutes: 300, UsedPercent: 5, ResetsAt: 1779646858})
	if l.ResetsAt == nil {
		t.Error("ResetsAt = nil, want set for a real epoch")
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
