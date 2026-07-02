package cmuxbar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func limitsTool(stale bool, pct5, pctW float64) schema.Tool {
	m5, mW := 300, 10080
	ctx := 24.0
	collected := "2026-06-12T20:00:00+09:00"
	return schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Stale:       stale,
		CollectedAt: &collected,
		Backend:     schema.BackendSubscription,
		Session:     &schema.Session{ContextUsedPct: &ctx},
		Limits: []schema.Limit{
			{Window: "5h", WindowMinutes: &m5, UsedPct: &pct5},
			{Window: "weekly", WindowMinutes: &mW, UsedPct: &pctW},
		},
	}
}

func codexTokenTool() schema.Tool {
	tokens := int64(3962991)
	return schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Backend:   schema.BackendBedrock,
		Fallback:  &schema.Fallback{SessionTokens: &tokens},
	}
}

// A tacho-managed sidebar key with no pill this push (unavailable, errored, or
// filtered out before Push) must be cleared, not left frozen.
func TestAbsentToolKeys(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{
		limitsTool(false, 24, 41),            // claude available → pill present
		schema.Unavailable(schema.ToolCodex), // codex unavailable → no pill
	}}
	pills := Pills(s, now)
	keys := absentToolKeys(pills)
	if len(keys) != 1 || keys[0] != "codex" {
		t.Errorf("absentToolKeys = %v, want [codex] (cleared because it has no pill)", keys)
	}

	// Both available → nothing to clear.
	s2 := schema.Status{Tools: []schema.Tool{limitsTool(false, 24, 41), codexTokenTool()}}
	if got := absentToolKeys(Pills(s2, now)); len(got) != 0 {
		t.Errorf("absentToolKeys = %v, want none when every tool has a pill", got)
	}
}

func TestPills(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{
		limitsTool(false, 24, 41),
		codexTokenTool(),
		schema.Unavailable(schema.ToolCodex), // ignored
	}}

	pills := Pills(s, now)
	if len(pills) != 2 {
		t.Fatalf("Pills = %+v, want 2", pills)
	}
	claude := pills[0]
	if claude.Key != "claude" || claude.Value != "claude ctx24% 5h24% wk41%" {
		t.Errorf("claude pill = %+v", claude)
	}
	if claude.Color != colorGreen {
		t.Errorf("claude color = %s, want green", claude.Color)
	}
	if pills[1].Key != "codex" || pills[1].Value != "codex 4Mtok" {
		t.Errorf("codex fallback pill = %+v", pills[1])
	}
}

func TestPillPressureColors(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	cases := []struct {
		pct5, pctW float64
		stale      bool
		want       string
	}{
		{10, 40, false, colorGreen},
		{10, 55, false, colorYellow}, // max wins
		{85, 10, false, colorRed},
		{85, 10, true, colorGray}, // stale wins over pressure
	}
	for _, c := range cases {
		s := schema.Status{Tools: []schema.Tool{limitsTool(c.stale, c.pct5, c.pctW)}}
		got := Pills(s, now)[0]
		if got.Color != c.want {
			t.Errorf("color(%v) = %s, want %s", c, got.Color, c.want)
		}
	}
}

func TestPillStaleAge(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00") // 1h after collected
	s := schema.Status{Tools: []schema.Tool{limitsTool(true, 24, 41)}}
	got := Pills(s, now)[0]
	if !strings.HasPrefix(got.Value, "claude ⚠1h ") {
		t.Errorf("stale pill = %q, want \"⚠1h\" prefix", got.Value)
	}
}

// fakeCLI writes each invocation's args to a log file so the exec path can
// be asserted without a running cmux.
func fakeCLI(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	bin = filepath.Join(dir, "cmux")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func TestPushAndClearExec(t *testing.T) {
	bin, log := fakeCLI(t)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{limitsTool(false, 24, 41), codexTokenTool()}}

	if err := Push(bin, s, now, true); err != nil {
		t.Fatal(err)
	}
	if err := Clear(bin, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(calls) != 4 {
		t.Fatalf("calls = %q, want 4 (2 set + 2 clear)", calls)
	}
	if calls[0] != "set-status claude claude ctx24% 5h24% wk41% --color "+colorGreen {
		t.Errorf("set call = %q", calls[0])
	}
	if calls[1] != "set-status codex codex 4Mtok --color "+colorGreen {
		t.Errorf("set call = %q", calls[1])
	}
	if calls[2] != "clear-status claude" || calls[3] != "clear-status codex" {
		t.Errorf("clear calls = %q", calls[2:])
	}
}

// Push must actually emit clear-status for a configured tool that has no pill
// (e.g. codex unavailable), and emit no clear when every tool is present.
func TestPushClearsAbsentTool(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")

	bin, log := fakeCLI(t)
	s := schema.Status{Tools: []schema.Tool{
		limitsTool(false, 24, 41),            // claude available → set
		schema.Unavailable(schema.ToolCodex), // codex unavailable → clear
	}}
	if err := Push(bin, s, now, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(calls) != 2 {
		t.Fatalf("calls = %q, want 2 (set claude + clear codex)", calls)
	}
	if !strings.HasPrefix(calls[0], "set-status claude ") || calls[1] != "clear-status codex" {
		t.Errorf("calls = %q, want set-status claude then clear-status codex", calls)
	}

	// If status was filtered to only claude, codex must still be cleared so an
	// old codex pill is not left frozen in the sidebar.
	bin2, log2 := fakeCLI(t)
	s2 := schema.Status{Tools: []schema.Tool{limitsTool(false, 24, 41)}}
	if err := Push(bin2, s2, now, true); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(log2)
	calls2 := strings.Split(strings.TrimSpace(string(b2)), "\n")
	if len(calls2) != 2 {
		t.Fatalf("calls = %q, want 2 (set claude + clear codex)", calls2)
	}
	if !strings.HasPrefix(calls2[0], "set-status claude ") || calls2[1] != "clear-status codex" {
		t.Errorf("calls = %q, want set-status claude then clear-status codex", calls2)
	}
}

func TestFindCLIEnvOverride(t *testing.T) {
	t.Setenv("TACHO_CMUX_BIN", "/tmp/custom-cmux")
	if got := FindCLI(); got != "/tmp/custom-cmux" {
		t.Errorf("FindCLI = %q", got)
	}
}

func TestDetect(t *testing.T) {
	t.Setenv("CMUX_WORKSPACE_ID", "")
	if Detect() {
		t.Error("Detect = true without CMUX_WORKSPACE_ID")
	}
	t.Setenv("CMUX_WORKSPACE_ID", "ws-1")
	if !Detect() {
		t.Error("Detect = false with CMUX_WORKSPACE_ID")
	}
}
