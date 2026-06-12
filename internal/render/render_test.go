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

func TestFormatTokens(t *testing.T) {
	cases := map[int64]string{42: "42", 989120: "989k", 12504028: "12.5M", 3000000: "3M"}
	for n, want := range cases {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestResetShort(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	soon := "2026-06-13T02:00:58+09:00"
	if got := ResetShort(soon, now); got != "↻02:00" {
		t.Errorf("ResetShort(soon) = %q", got)
	}
	far := "2026-06-15T10:30:00+09:00"
	if got := ResetShort(far, now); got != "↻06/15" {
		t.Errorf("ResetShort(far) = %q", got)
	}
	if got := ResetShort("garbage", now); got != "↻--" {
		t.Errorf("ResetShort(garbage) = %q", got)
	}
	past := "2026-06-08T10:30:00+09:00"
	if got := ResetShort(past, now); got != "↻06/08" {
		t.Errorf("ResetShort(past) = %q, want date form for expired resets", got)
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
	for _, want := range []string{"claude", "Fable 5", "ctx 8%", "5h", "24%", "↻02:00", "wk", "41%", "↻06/15"} {
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
}

func TestStatusLines(t *testing.T) {
	s := schema.Status{Tools: []schema.Tool{limitsTool(), schema.Unavailable(schema.ToolCodex)}}
	got := StatusLines(s, time.Now(), plain)
	if len(strings.Split(got, "\n")) != 2 {
		t.Errorf("StatusLines = %q, want 2 lines", got)
	}
}
