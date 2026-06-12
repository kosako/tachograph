package render

import (
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

var plain = Style{}

func TestBar(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, "░░░░░░░░"},
		{5, "░░░░░░░░"},
		{25, "██░░░░░░"},
		{50, "████░░░░"},
		{100, "████████"},
		{130, "████████"},
		{-5, "░░░░░░░░"},
	}
	for _, c := range cases {
		if got := Bar(c.pct, 8); got != c.want {
			t.Errorf("Bar(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestDial(t *testing.T) {
	cases := map[float64]string{0: "○", 5: "○", 24: "◔", 50: "◑", 75: "◕", 90: "●", 100: "●"}
	for pct, want := range cases {
		if got := Dial(pct); got != want {
			t.Errorf("Dial(%v) = %q, want %q", pct, got, want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int64]string{42: "42", 989120: "989k", 12504028: "12.5M", 3000000: "3M"}
	for n, want := range cases {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

// hhmm / mmdd render expected values in the test runner's local timezone,
// keeping assertions valid on any CI timezone.
func hhmm(t *testing.T, iso string) string { return expect(t, iso, "↻15:04") }
func mmdd(t *testing.T, iso string) string { return expect(t, iso, "↻01/02") }

func expect(t *testing.T, iso, layout string) string {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t.Fatal(err)
	}
	return ts.Local().Format(layout)
}

func TestResetShort(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	soon := "2026-06-13T02:00:58+09:00"
	if got := ResetShort(soon, now); got != hhmm(t, soon) {
		t.Errorf("ResetShort(soon) = %q, want %q", got, hhmm(t, soon))
	}
	far := "2026-06-15T10:30:00+09:00"
	if got := ResetShort(far, now); got != mmdd(t, far) {
		t.Errorf("ResetShort(far) = %q, want %q", got, mmdd(t, far))
	}
	if got := ResetShort("garbage", now); got != "↻--" {
		t.Errorf("ResetShort(garbage) = %q", got)
	}
	past := "2026-06-08T10:30:00+09:00"
	if got := ResetShort(past, now); got != mmdd(t, past) {
		t.Errorf("ResetShort(past) = %q, want date form %q for expired resets", got, mmdd(t, past))
	}
}

func limitsTool() schema.Tool {
	pct5, pctW := 23.5, 41.2
	m5, mW := 300, 10080
	r5, rW := "2026-06-13T02:00:00+09:00", "2026-06-15T10:30:00+09:00"
	ctx := 8.0
	name := "Fable 5"
	return schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Backend:   schema.BackendSubscription,
		Model:     &schema.Model{ID: "claude-fable-5", DisplayName: &name},
		Session:   &schema.Session{ContextUsedPct: &ctx},
		Limits: []schema.Limit{
			{Window: "5h", WindowMinutes: &m5, UsedPct: &pct5, ResetsAt: &r5},
			{Window: "weekly", WindowMinutes: &mW, UsedPct: &pctW, ResetsAt: &rW},
		},
	}
}

func TestToolLineWithLimits(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := ToolLine(limitsTool(), now, plain)
	for _, want := range []string{"claude", "Fable 5", "ctx 8%", "5h", "24%", hhmm(t, "2026-06-13T02:00:00+09:00"), "wk", "41%", mmdd(t, "2026-06-15T10:30:00+09:00")} {
		if !strings.Contains(got, want) {
			t.Errorf("ToolLine = %q, missing %q", got, want)
		}
	}
}

func TestToolLineFallback(t *testing.T) {
	tokens := int64(3962991)
	cost := 1.5
	tool := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Backend:   schema.BackendBedrock,
		Model:     &schema.Model{ID: "gpt-5.5"},
		Fallback:  &schema.Fallback{SessionTokens: &tokens, EstimatedCostUSD: &cost},
	}
	got := ToolLine(tool, time.Now(), plain)
	for _, want := range []string{"codex", "tokens 4M", "$1.50"} {
		if !strings.Contains(got, want) {
			t.Errorf("ToolLine = %q, missing %q", got, want)
		}
	}
}

func TestToolLineUnavailableAndStale(t *testing.T) {
	got := ToolLine(schema.Unavailable(schema.ToolCodex), time.Now(), plain)
	if !strings.Contains(got, "(not found)") {
		t.Errorf("ToolLine = %q", got)
	}

	tool := limitsTool()
	tool.Stale = true
	if got := ToolLine(tool, time.Now(), plain); !strings.Contains(got, "⚠") {
		t.Errorf("stale marker missing: %q", got)
	}

	now := time.Now()
	collected := now.Add(-2 * time.Hour).Format(time.RFC3339)
	tool.CollectedAt = &collected
	if got := ToolLine(tool, now, plain); !strings.Contains(got, "⚠2h") {
		t.Errorf("stale age missing: %q", got)
	}
	colored := ToolLine(tool, now, Style{Color: true})
	if !strings.HasPrefix(colored, "\x1b[2m") || !strings.HasSuffix(colored, "\x1b[0m") {
		t.Errorf("stale line should be dimmed as a whole: %q", colored)
	}
	if strings.Contains(strings.TrimSuffix(colored[4:], "\x1b[0m"), "\x1b[") {
		t.Errorf("stale line should not contain inner color codes: %q", colored)
	}
}

func TestAge(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	cases := map[string]string{
		"2026-06-12T20:59:30+09:00": "30s",
		"2026-06-12T20:15:00+09:00": "45m",
		"2026-06-12T16:00:00+09:00": "5h",
		"2026-06-04T21:00:00+09:00": "8d",
		"garbage":                   "",
		"2026-06-12T22:00:00+09:00": "", // future
	}
	for iso, want := range cases {
		if got := Age(iso, now); got != want {
			t.Errorf("Age(%q) = %q, want %q", iso, got, want)
		}
	}
}

func TestStatusLines(t *testing.T) {
	s := schema.Status{Tools: []schema.Tool{limitsTool(), schema.Unavailable(schema.ToolCodex)}}
	got := StatusLines(s, time.Now(), plain)
	if len(strings.Split(got, "\n")) != 2 {
		t.Errorf("StatusLines = %q, want 2 lines", got)
	}
}
