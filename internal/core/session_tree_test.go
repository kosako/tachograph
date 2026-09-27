package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// fixtureSessionTokens is the claudeRoot fixture's main transcript total:
// (100+5000+30000+400) + (2+673+39451+481).
const fixtureSessionTokens = int64(76107)

// claudeRootWithSubagent copies the fixture session into a temp Claude root
// and adds a subagent transcript under the session's directory carrying
// `subTokens` input tokens at 12:02Z on the fixture day (#262).
func claudeRootWithSubagent(t *testing.T, subTokens int64) (root, mainPath string) {
	t.Helper()
	root = t.TempDir()
	const id = "abc12345-1234-5678-9abc-def012345678"
	proj := filepath.Join(root, "projects", "-Users-example-dev-project")
	b, err := os.ReadFile(filepath.Join(claudeRoot, "projects", "-Users-example-dev-project", id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proj, id, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath = filepath.Join(proj, id+".jsonl")
	if err := os.WriteFile(mainPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	sub := `{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-06-12T12:02:00.000Z","message":{"id":"msg_sub","model":"claude-fable-5","role":"assistant","usage":{"input_tokens":` +
		strconv.FormatInt(subTokens, 10) + `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}},"requestId":"req_sub"}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, id, "subagents", "agent-a.jsonl"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}
	// The main transcript stays the newest so the transcript route picks it.
	later := time.Date(2026, 6, 12, 12, 3, 0, 0, time.UTC)
	if err := os.Chtimes(mainPath, later, later); err != nil {
		t.Fatal(err)
	}
	return root, mainPath
}

// session.tokens and fallback.session_tokens cover the whole session tree —
// the main transcript plus its subagent transcripts — the same scope as
// session_today (#262).
func TestStatusWidensSessionTokensToTree(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	clearClaudeBackendEnvCore(t)
	root, _ := claudeRootWithSubagent(t, 100000)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")

	got := Status(Options{ClaudeRoot: root, CodexRoot: codexRoot, Now: now, NoCache: true}).Tools[0]
	want := fixtureSessionTokens + 100000
	if got.Session == nil || got.Session.Tokens == nil || got.Session.Tokens.Total != want {
		t.Fatalf("session.tokens = %+v, want total %d (main + subagent)", got.Session, want)
	}
	if got.Fallback == nil || got.Fallback.SessionTokens == nil || *got.Fallback.SessionTokens != want {
		t.Errorf("fallback.session_tokens = %+v, want %d", got.Fallback, want)
	}
	if got.SessionToday == nil || got.SessionToday.Tokens != want {
		t.Errorf("session_today = %+v, want %d (all of it is today)", got.SessionToday, want)
	}
	if got.SessionToday.Tokens > got.Session.Tokens.Total {
		t.Errorf("session_today (%d) exceeds session.tokens (%d)", got.SessionToday.Tokens, got.Session.Tokens.Total)
	}
	if schema.Version != "3.0" {
		t.Errorf("schema.Version = %q, want 3.0 (session.tokens changed meaning)", schema.Version)
	}
}

func clearClaudeBackendEnvCore(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "ANTHROPIC_API_KEY"} {
		t.Setenv(k, "")
	}
}

// Codex review (#262): when the tree total is unknown (here a nested
// transcript can't be read), session.tokens and fallback.session_tokens are
// null — not the collector's main-transcript figure, a different measure.
func TestAddSessionTreeUnknownTreeNullsTokens(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions enforced for the current user")
	}
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	_, mainPath := claudeRootWithSubagent(t, 100000)
	child := filepath.Join(strings.TrimSuffix(mainPath, ".jsonl"), "subagents", "agent-a.jsonl")
	if err := os.Chmod(child, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(child, 0o644) })

	mainOnly := int64(76107)
	tool := schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Session:   &schema.Session{TranscriptPath: &mainPath, Tokens: &schema.Tokens{Total: mainOnly}},
		Fallback:  &schema.Fallback{SessionTokens: &mainOnly},
	}
	now, _ := time.Parse(time.RFC3339, "2026-06-12T12:05:00Z")
	AddSessionTree(&tool, now, nil)
	if tool.Session.Tokens != nil || tool.Fallback.SessionTokens != nil {
		t.Errorf("tokens = %+v / %v, want null when the tree total is unknown", tool.Session.Tokens, tool.Fallback.SessionTokens)
	}
	if tool.SessionToday == nil {
		t.Error("session_today should still be attached (its own contract)")
	}
}
