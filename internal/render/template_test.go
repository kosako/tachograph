package render

import (
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func testStatus() schema.Status {
	claude := limitsTool()
	tokens := int64(989120)
	cost := 0.05
	claude.Session.Tokens = &schema.Tokens{Total: tokens}
	claude.Fallback = &schema.Fallback{SessionTokens: &tokens, EstimatedCostUSD: &cost}
	cwd := "/Users/example/dev/project"
	claude.Session.CWD = &cwd

	codex := schema.Unavailable(schema.ToolCodex)
	return schema.Status{Tools: []schema.Tool{claude, codex}}
}

func TestTemplateBasics(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	s := testStatus()

	cases := map[string]string{
		"{claude.model}":     "Fable 5",
		"{claude.ctx}":       "8%",
		"{claude.5h.pct}":    "24%",
		"{claude.5h}":        "24%", // bare window defaults to pct
		"{claude.wk.pct}":    "41%",
		"{claude.5h.bar:4}":  "█░░░",
		"{claude.5h.dial}":   "◔", // 24% used
		"{claude.wk.dial}":   "◑", // 41% used
		"{codex.5h.dial}":    DialMissing,
		"{claude.5h.resets}": hhmm(t, "2026-06-13T02:00:00+09:00"),
		"{claude.tokens}":    "989k",
		"{claude.cost}":      "$0.05",
		"{claude.cwd}":       "project",
		"{claude.stale}":     "",
		"{codex.model}":      Missing, // unavailable tool
		"{codex.5h.pct}":     Missing,
		"{codex.5h.bar:4}":   "░░░░", // bars keep their width when absent
		"{claude.plan}":      Missing,
		"{claude.nope}":      Missing,
		"{nope.model}":       Missing,
	}
	for tmpl, want := range cases {
		if got := Template(tmpl, s, now, plain); got != want {
			t.Errorf("Template(%q) = %q, want %q", tmpl, got, want)
		}
	}
}

func TestTemplateComposite(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := Template("[{claude.model}] 5h {claude.5h.bar:8} {claude.5h.pct}", testStatus(), now, plain)
	want := "[Fable 5] 5h ██░░░░░░ 24%"
	if got != want {
		t.Errorf("Template = %q, want %q", got, want)
	}
}

func TestTemplateStaleMarker(t *testing.T) {
	s := testStatus()
	s.Tools[0].Stale = true
	got := Template("{claude.stale}ctx", s, time.Now(), plain)
	if !strings.HasPrefix(got, "⚠ ") {
		t.Errorf("Template = %q, want stale marker prefix", got)
	}

	now := time.Now()
	collected := now.Add(-90 * time.Minute).Format(time.RFC3339)
	s.Tools[0].CollectedAt = &collected
	if got := Template("{claude.stale}", s, now, plain); got != "⚠1h " {
		t.Errorf("Template stale with age = %q, want \"⚠1h \"", got)
	}
	if got := Template("{claude.age}", s, now, plain); got != "1h" {
		t.Errorf("Template age = %q, want \"1h\"", got)
	}
	s.Tools[1].CollectedAt = nil
	if got := Template("{codex.age}", s, now, plain); got != Missing {
		t.Errorf("Template age (nil collected_at) = %q, want %q", got, Missing)
	}
}

func TestDefaultTemplateRenders(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := Template(DefaultTemplate, testStatus(), now, plain)
	if strings.Contains(got, "{") {
		t.Errorf("DefaultTemplate left unexpanded placeholders: %q", got)
	}
}
