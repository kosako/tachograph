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

func pctOf(v float64) *float64 { return &v }

// toLimit must keep an absent/zero resets_at as null, not 1970-01-01.
func TestToLimitNullResetsAt(t *testing.T) {
	l := toLimit(schema.WindowFiveHour, 300, &slWindow{UsedPercentage: pctOf(5), ResetsAt: 0}, "2026-06-13T10:00:00+09:00")
	if l.ResetsAt != nil {
		t.Errorf("ResetsAt = %v, want nil for zero epoch", *l.ResetsAt)
	}
	l = toLimit(schema.WindowFiveHour, 300, &slWindow{UsedPercentage: pctOf(5), ResetsAt: 1779646858}, "2026-06-13T10:00:00+09:00")
	if l.ResetsAt == nil {
		t.Error("ResetsAt = nil, want set for a real epoch")
	}
}

// A window without used_percentage reports an unknown use, not 0% (#322);
// a real 0 is still 0.
func TestToLimitMissingUsedPercentage(t *testing.T) {
	if l := toLimit(schema.WindowFiveHour, 300, &slWindow{ResetsAt: 1779646858}, "2026-06-13T10:00:00+09:00"); l.UsedPct != nil {
		t.Errorf("UsedPct = %v, want nil without used_percentage", *l.UsedPct)
	}
	if l := toLimit(schema.WindowFiveHour, 300, &slWindow{UsedPercentage: pctOf(0)}, "2026-06-13T10:00:00+09:00"); l.UsedPct == nil || *l.UsedPct != 0 {
		t.Errorf("UsedPct = %v, want 0 for an explicit 0", l.UsedPct)
	}
}

// End to end: a statusline payload whose window lacks used_percentage keeps
// the window (and its reset time) with used_pct null (#322).
func TestFromStatuslineMissingUsedPercentage(t *testing.T) {
	input := []byte(`{"model":{"id":"claude-x","display_name":"X"},"rate_limits":{"five_hour":{"resets_at":1779646858},"seven_day":{"used_percentage":null,"resets_at":1779646858}}}`)
	got := Collect(Options{Root: t.TempDir(), StatuslineInput: input, Getenv: noEnv})
	if got.Error != nil || len(got.Limits) != 2 {
		t.Fatalf("Error = %+v, Limits = %+v, want two windows", got.Error, got.Limits)
	}
	for _, l := range got.Limits {
		if l.UsedPct != nil {
			t.Errorf("%s UsedPct = %v, want nil when the payload has no used_percentage", l.Window, *l.UsedPct)
		}
		if l.ResetsAt == nil {
			t.Errorf("%s ResetsAt = nil, want the payload's reset time kept", l.Window)
		}
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
	// Cumulative usage is core.AddSessionTree's to count from the transcripts
	// (#375), never context_window.total_* — those mean "currently in the
	// context window" since Claude Code v2.1.132 (#185).
	if s.Tokens != nil {
		t.Errorf("Tokens = %+v, want nil (left to core.AddSessionTree)", s.Tokens)
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
	if got.Fallback.SessionTokens != nil {
		t.Errorf("Fallback.SessionTokens = %v, want nil (left to core.AddSessionTree)", *got.Fallback.SessionTokens)
	}
}

// statuslineInputWithTranscript returns the statusline fixture with its
// transcript_path swapped for path.
func statuslineInputWithTranscript(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/statusline_input.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload["transcript_path"] = path
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// When the transcript can't be read there is no cumulative source, and tokens
// must stay null (unknown) rather than carry context_window.total_* — those
// mean "currently in the context window" since Claude Code v2.1.132 (#185).
func TestFromStatuslineTranscriptUnreadable(t *testing.T) {
	input := statuslineInputWithTranscript(t, filepath.Join(t.TempDir(), "missing.jsonl"))
	got := Collect(Options{Root: "testdata/clauderoot", StatuslineInput: input, Getenv: noEnv})
	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Session == nil || got.Session.Tokens != nil {
		t.Errorf("Session.Tokens = %+v, want nil for an unreadable transcript", got.Session)
	}
	if got.Fallback == nil || got.Fallback.SessionTokens != nil {
		t.Errorf("Fallback.SessionTokens = %+v, want nil for an unreadable transcript", got.Fallback)
	}
	// The rest of the statusline payload still renders.
	if got.Session.ContextWindow == nil || *got.Session.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %v, want 200000", got.Session.ContextWindow)
	}
	if got.Fallback.EstimatedCostUSD == nil || *got.Fallback.EstimatedCostUSD != 0.01234 {
		t.Errorf("EstimatedCostUSD = %v, want 0.01234", got.Fallback.EstimatedCostUSD)
	}
}

// A transcript with no assistant usage yet (session just opened) has no
// cumulative evidence either: tokens stay null, not zero.
func TestFromStatuslineTranscriptNoUsage(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, "s.jsonl", `{"timestamp":"2026-06-12T12:00:00Z","type":"user"}`, time.Unix(1000, 0))
	input := statuslineInputWithTranscript(t, filepath.Join(dir, "s.jsonl"))
	got := Collect(Options{Root: "testdata/clauderoot", StatuslineInput: input, Getenv: noEnv})
	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Session == nil || got.Session.Tokens != nil {
		t.Errorf("Session.Tokens = %+v, want nil for a usage-less transcript", got.Session)
	}
	if got.Fallback == nil || got.Fallback.SessionTokens != nil {
		t.Errorf("Fallback.SessionTokens = %+v, want nil for a usage-less transcript", got.Fallback)
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
	// The degraded display's session tokens are core.AddSessionTree's (#375).
	if got.Fallback == nil || got.Fallback.EstimatedCostUSD == nil {
		t.Errorf("Fallback = %+v, want the payload's cost for degraded display", got.Fallback)
	}
}

// Vertex has no subscription windows either: its env wins over an ambient
// ANTHROPIC_API_KEY and over a payload that carries rate_limits, and the
// limits are dropped as on Bedrock (#445).
func TestFromStatuslineVertexDegradesLimits(t *testing.T) {
	input, _ := os.ReadFile("testdata/statusline_input.json")
	env := func(k string) string {
		switch k {
		case "CLAUDE_CODE_USE_VERTEX":
			return "1"
		case "ANTHROPIC_API_KEY":
			return "sk-test"
		}
		return ""
	}
	got := Collect(Options{Root: "testdata/clauderoot", StatuslineInput: input, Getenv: env})
	if got.Backend != schema.BackendVertex {
		t.Errorf("Backend = %q, want vertex", got.Backend)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want null on vertex", got.Limits)
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
	if s == nil || s.ID == nil || *s.ID != "abc12345-1234-5678-9abc-def012345678" || s.TranscriptPath == nil {
		t.Fatalf("Session = %+v", s)
	}
	// Tokens are core.AddSessionTree's to count (#375); fallback stays an
	// object, as it reads in the JSON when that count is unknown.
	if s.Tokens != nil {
		t.Errorf("Tokens = %+v, want nil (left to core.AddSessionTree)", s.Tokens)
	}
	if got.Fallback == nil || got.Fallback.SessionTokens != nil {
		t.Errorf("Fallback = %+v, want an empty fallback", got.Fallback)
	}
	// Dated by the newest response (12:01:30), not the first.
	if want := time.Date(2026, 6, 12, 12, 1, 30, 0, time.UTC).Local().Format(time.RFC3339); got.CollectedAt == nil || *got.CollectedAt != want {
		t.Errorf("CollectedAt = %v, want %s", got.CollectedAt, want)
	}
	if got.Stale {
		t.Error("Stale = true, want false (last entry 3.5min before now)")
	}
}

func TestCollectUsesClaudeConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)

	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-06-12T12:00:00Z","sessionId":"env-root","cwd":"/x","message":{"model":"claude-x","usage":{"input_tokens":10,"output_tokens":5}}}`
	writeTranscript(t, dir, "env.jsonl", line, time.Unix(1000, 0))

	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	got := Collect(Options{Now: now, Getenv: noEnv})
	if !got.Available || got.Error != nil {
		t.Fatalf("Available=%v Error=%+v", got.Available, got.Error)
	}
	if got.Session == nil || got.Session.ID == nil || *got.Session.ID != "env-root" {
		t.Fatalf("Session = %+v, want CLAUDE_CONFIG_DIR transcript", got.Session)
	}
}

func TestNoTranscripts(t *testing.T) {
	got := Collect(Options{Root: t.TempDir(), Getenv: noEnv})
	if got.Available {
		t.Errorf("Available = true, want false: %+v", got)
	}
}

// A live statusline rate_limits payload is stronger evidence than an ambient
// ANTHROPIC_API_KEY exported for other tools.
func TestFromStatuslineAPIKeyDoesNotDiscardRateLimits(t *testing.T) {
	input, _ := os.ReadFile("testdata/statusline_input.json")
	env := func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk-ant-xxx"
		}
		return ""
	}
	got := Collect(Options{Root: "testdata/clauderoot", StatuslineInput: input, Getenv: env})
	if got.Backend != schema.BackendSubscription {
		t.Errorf("Backend = %q, want subscription", got.Backend)
	}
	if len(got.Limits) != 2 {
		t.Fatalf("Limits = %+v, want statusline rate limits", got.Limits)
	}
	if got.Limits[0].Window != schema.WindowFiveHour || got.Limits[0].UsedPct == nil || *got.Limits[0].UsedPct != 23.5 {
		t.Errorf("5h limit = %+v", got.Limits[0])
	}
}

func TestFromStatuslineAPIBackendWithoutRateLimits(t *testing.T) {
	input, _ := os.ReadFile("testdata/statusline_input.json")
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "rate_limits")
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("Limits = %+v, want null without observed rate limits", got.Limits)
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
	if got.Session == nil || got.Session.TranscriptPath == nil || *got.Session.TranscriptPath != filepath.Join(dir, "old.jsonl") {
		t.Fatalf("Session = %+v, want fallback to older transcript", got.Session)
	}
	if got.Session.ID == nil || *got.Session.ID != "old" {
		t.Errorf("Session.ID = %v, want \"old\" after fallback", got.Session.ID)
	}
}

// An older session that only got a user line appended sorts first by mtime,
// but the transcript with the newest response wins (#437). A transcript last
// written before that response is skipped without reading: it can't hold a
// newer line, so the stray future-dated line planted in one here is never
// seen.
func TestTranscriptNewestUsageWinsOverNewestMtime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	usage := func(ts, session string) string {
		return `{"timestamp":"` + ts + `","sessionId":"` + session + `","message":{"model":"claude-x","usage":{"input_tokens":10,"output_tokens":5}}}`
	}
	writeTranscript(t, dir, "a.jsonl", usage("2026-06-12T10:00:00Z", "a")+"\n"+
		`{"timestamp":"2026-06-12T12:01:00Z","sessionId":"a","type":"user"}`, at("2026-06-12T12:01:00Z"))
	writeTranscript(t, dir, "b.jsonl", usage("2026-06-12T12:00:00Z", "b"), at("2026-06-12T12:00:00Z"))
	writeTranscript(t, dir, "c.jsonl", usage("2026-06-12T12:30:00Z", "c"), at("2026-06-12T11:00:00Z"))

	got := Collect(Options{Root: root, Now: at("2026-06-12T12:05:00Z"), Getenv: noEnv})
	if got.Session == nil || got.Session.ID == nil || *got.Session.ID != "b" {
		t.Fatalf("Session = %+v, want b (the newest response)", got.Session)
	}
	if got.Session.TranscriptPath == nil || *got.Session.TranscriptPath != filepath.Join(dir, "b.jsonl") {
		t.Errorf("TranscriptPath = %v, want b.jsonl", got.Session.TranscriptPath)
	}
	if got.CollectedAt == nil {
		t.Fatal("CollectedAt = nil")
	}
	if ct, _ := time.Parse(time.RFC3339, *got.CollectedAt); !ct.Equal(at("2026-06-12T12:00:00Z")) {
		t.Errorf("CollectedAt = %v, want b's response at 12:00", ct)
	}
	if got.Stale {
		t.Error("Stale = true, want false (b's response is 5 minutes old)")
	}
}

// A file system that keeps whole-second mtimes can stamp a file before the
// sub-second timestamp of its last line; that file must still be read, not
// skipped as older than the response already found (#437).
func TestTranscriptCoarseMtimeStillRead(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	usage := func(ts, session string) string {
		return `{"timestamp":"` + ts + `","sessionId":"` + session + `","message":{"model":"claude-x","usage":{"input_tokens":10,"output_tokens":5}}}`
	}
	writeTranscript(t, dir, "a.jsonl", usage("2026-06-12T12:00:00.100Z", "a")+"\n"+
		`{"timestamp":"2026-06-12T12:01:00Z","sessionId":"a","type":"user"}`, at("2026-06-12T12:01:00Z"))
	writeTranscript(t, dir, "b.jsonl", usage("2026-06-12T12:00:00.900Z", "b"), at("2026-06-12T12:00:00Z"))

	got := Collect(Options{Root: root, Now: at("2026-06-12T13:00:00.500Z"), Getenv: noEnv})
	if got.Session == nil || got.Session.ID == nil || *got.Session.ID != "b" {
		t.Fatalf("Session = %+v, want b (its response is newer though its mtime is the whole second before)", got.Session)
	}
	if got.Stale {
		t.Error("Stale = true, want false (b's response is under 60 minutes old)")
	}
}

// Equal mtimes must resolve deterministically (by path), not by directory
// read order, so the same session is picked across runs.
func TestTranscriptTieBreakByPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := time.Unix(1000, 0)
	writeTranscript(t, dir, "a.jsonl", `{"sessionId":"a","message":{"usage":{"input_tokens":1,"output_tokens":1}}}`, mod)
	writeTranscript(t, dir, "b.jsonl", `{"sessionId":"b","message":{"usage":{"input_tokens":2,"output_tokens":2}}}`, mod)

	got := Collect(Options{Root: root, Getenv: noEnv})
	// Higher path ("b.jsonl") wins the tie.
	if got.Session == nil || got.Session.ID == nil || *got.Session.ID != "b" {
		t.Fatalf("Session.ID = %v, want \"b\" (deterministic tie-break)", got.Session.ID)
	}
}

// Claude Code writes one line per content block, each repeating the same
// usage; the transcript route is dated by the newest block, not the first.
// Counting each response once is core.AddSessionTree's (#375; see
// internal/daily's TestClaudeSessionTreeCountsContentBlocksOnce).
func TestTranscriptNewestBlock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One response across 3 content-block lines: same id+requestId and usage,
	// but increasing timestamps (Claude Code stamps each block separately).
	msg := func(ts string) string {
		return `{"timestamp":"` + ts + `","sessionId":"s","requestId":"req_a","message":{"id":"msg_a","model":"claude-x","usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":100,"output_tokens":5}}}`
	}
	content := msg("2026-06-12T12:00:00Z") + "\n" + msg("2026-06-12T12:00:03Z") + "\n" + msg("2026-06-12T12:00:06Z")
	writeTranscript(t, dir, "a.jsonl", content, time.Unix(1000, 0))

	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	got := Collect(Options{Root: root, Now: now, Getenv: noEnv})
	if got.Session == nil {
		t.Fatalf("Session = %+v", got.Session)
	}
	if got.CollectedAt == nil {
		t.Fatal("CollectedAt = nil")
	}
	ct, _ := time.Parse(time.RFC3339, *got.CollectedAt)
	if want, _ := time.Parse(time.RFC3339, "2026-06-12T12:00:06Z"); !ct.Equal(want) {
		t.Errorf("CollectedAt = %v, want newest block %v", ct, want)
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

func TestUsageKey(t *testing.T) {
	keyed := TranscriptLine{RequestID: "req_a", Message: &TranscriptMessage{ID: "msg_a"}}
	if id, req, ok := keyed.UsageKey(); !ok || id != "msg_a" || req != "req_a" {
		t.Errorf("UsageKey() = %q, %q, %v, want msg_a, req_a, true", id, req, ok)
	}
	// A line without a message id has no identity to dedup on, even with a
	// request id; it is counted every time it appears.
	unkeyed := TranscriptLine{RequestID: "req_b", Message: &TranscriptMessage{}}
	if _, _, ok := unkeyed.UsageKey(); ok {
		t.Error("UsageKey() ok = true for a line without a message id, want false")
	}
	seen := UsageSet{}
	if seen.Dup(unkeyed) || seen.Dup(unkeyed) {
		t.Error("Dup() = true for an unkeyed line, want never deduplicated")
	}
	if seen.Dup(keyed) || !seen.Dup(keyed) {
		t.Error("Dup() should be false on first sight of a keyed line and true after")
	}
}

// A payload that names neither a transcript nor the session's duration is
// dated at receipt: each window's observed_at is the tool's collected_at
// (#295).
func TestFromStatuslineLimitsObservedAt(t *testing.T) {
	input := []byte(`{"model":{"id":"claude-x"},"rate_limits":{"five_hour":{"used_percentage":12,"resets_at":1779646858},"seven_day":{"used_percentage":3,"resets_at":1779646858}}}`)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := Collect(Options{Root: t.TempDir(), Now: now, StatuslineInput: input, Getenv: noEnv})
	if got.Error != nil || len(got.Limits) != 2 || got.CollectedAt == nil {
		t.Fatalf("Error = %+v, Limits = %+v", got.Error, got.Limits)
	}
	for _, l := range got.Limits {
		if l.ObservedAt == nil || *l.ObservedAt != *got.CollectedAt {
			t.Errorf("%s ObservedAt = %v, want collected_at %s", l.Window, l.ObservedAt, *got.CollectedAt)
		}
	}
}

// The rate limits a payload carries are dated by when Claude Code could last
// have refreshed them — the later of the session's last recorded API
// response and its start (the quota probe) — not by the redraw that pushed
// them, which for an idle session can be hours later (#330). A transcript
// that can't be read, or whose last response has no readable timestamp,
// leaves the session's start; with neither, the payload is dated at receipt.
func TestFromStatuslineLimitsObservedAtFromSession(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	ms := func(v float64) *float64 { return &v }
	const (
		noTranscript = iota
		responded    // last response at lastResponse before now
		laterTurns   // an older response, the last one, then a user turn
		unreadable   // transcript_path names no file
		noTimestamp  // the usage line carries no timestamp
		badTimestamp // the usage line's timestamp doesn't parse
	)
	cases := []struct {
		name         string
		transcript   int
		lastResponse time.Duration // before now
		running      *float64      // cost.total_duration_ms; nil = not given
		want         time.Duration // observed_at, before now
	}{
		{name: "last response", transcript: responded, lastResponse: 3 * time.Hour, want: 3 * time.Hour},
		{name: "last of several responses", transcript: laterTurns, lastResponse: 3 * time.Hour, want: 3 * time.Hour},
		{name: "sub-second last response", transcript: responded, lastResponse: 2750 * time.Millisecond, want: 2750 * time.Millisecond},
		{name: "session start", running: ms(600000), want: 10 * time.Minute},
		{name: "start after the last response (a resumed session's probe)", transcript: responded, lastResponse: 3 * time.Hour, running: ms(600000), want: 10 * time.Minute},
		{name: "last response after the start", transcript: responded, lastResponse: time.Minute, running: ms(600000), want: time.Minute},
		{name: "dated ahead of receipt", transcript: responded, lastResponse: -time.Hour, want: 0},
		{name: "fractional duration", running: ms(1500.5), want: 1500500 * time.Microsecond},
		{name: "unreadable transcript, start given", transcript: unreadable, running: ms(600000), want: 10 * time.Minute},
		{name: "unreadable transcript, no start", transcript: unreadable, want: 0},
		{name: "no timestamp, start given", transcript: noTimestamp, running: ms(600000), want: 10 * time.Minute},
		{name: "unparsable timestamp, no start", transcript: badTimestamp, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := map[string]any{
				"model":       map[string]any{"id": "claude-x"},
				"rate_limits": map[string]any{"five_hour": map[string]any{"used_percentage": 12, "resets_at": now.Add(2 * time.Hour).Unix()}},
			}
			path := filepath.Join(t.TempDir(), "session.jsonl")
			usage := `"message":{"model":"claude-x","usage":{"input_tokens":1,"output_tokens":1}}`
			var line string
			switch c.transcript {
			case responded:
				line = `{"type":"assistant","timestamp":"` + now.Add(-c.lastResponse).UTC().Format(time.RFC3339Nano) + `",` + usage + `}`
			case laterTurns:
				line = `{"type":"assistant","timestamp":"` + now.Add(-c.lastResponse-time.Hour).UTC().Format(time.RFC3339Nano) + `",` + usage + "}\n" +
					`{"type":"assistant","timestamp":"` + now.Add(-c.lastResponse).UTC().Format(time.RFC3339Nano) + `",` + usage + "}\n" +
					`{"type":"user","timestamp":"` + now.Add(-time.Minute).UTC().Format(time.RFC3339Nano) + `"}`
			case noTimestamp:
				line = `{"type":"assistant",` + usage + `}`
			case badTimestamp:
				line = `{"type":"assistant","timestamp":"not-a-time",` + usage + `}`
			}
			if line != "" {
				if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if c.transcript != noTranscript {
				payload["transcript_path"] = path
			}
			if c.running != nil {
				payload["cost"] = map[string]any{"total_cost_usd": 0.5, "total_duration_ms": *c.running}
			}
			input, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			got := Collect(Options{Root: t.TempDir(), Now: now, StatuslineInput: input, Getenv: noEnv})
			if got.Error != nil || len(got.Limits) != 1 {
				t.Fatalf("Error = %+v, Limits = %+v", got.Error, got.Limits)
			}
			want := now.Add(-c.want).Local().Format(time.RFC3339Nano)
			if l := got.Limits[0]; l.ObservedAt == nil || *l.ObservedAt != want {
				t.Errorf("ObservedAt = %v, want %s", l.ObservedAt, want)
			}
		})
	}
}
