package render

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// A known daily total wins even when it is zero: a day with no usage yet is
// an exact $0.00/d, never the current session's cost carried over from an
// earlier day (#261). Only an unknown daily total — no daily at all, or usage
// without any priced model — falls back to the session value, as documented.
func TestMetricCostZeroDay(t *testing.T) {
	session := 45.67
	priced := 1.23
	cases := []struct {
		name  string
		daily *schema.Daily
		fb    *float64
		want  string
	}{
		{"zero usage today, stale session cost present", &schema.Daily{Tokens: 0}, &session, "$0.00/d"},
		{"zero usage today, no session cost", &schema.Daily{Tokens: 0}, nil, "$0.00/d"},
		{"priced usage today", &schema.Daily{Tokens: 10, CostUSD: &priced}, &session, "$1.23/d"},
		{"usage without a priced model: unknown → session", &schema.Daily{Tokens: 10}, &session, "$45.67"},
		{"no daily total: unknown → session", nil, &session, "$45.67"},
		{"nothing known", nil, nil, Missing},
	}
	for _, c := range cases {
		tool := schema.Tool{Tool: schema.ToolCodex, Available: true, Daily: c.daily}
		if c.fb != nil {
			tool.Fallback = &schema.Fallback{EstimatedCostUSD: c.fb}
		}
		if _, got, _ := Metric(tool, MetricCost, LimitRemaining); got != c.want {
			t.Errorf("%s: Metric(cost) = %q, want %q", c.name, got, c.want)
		}
	}
}

// {tool.cost.all} is the explicit daily scope: a zero-usage day reads $0.00/d
// like {tool.tokens.all} reads 0/d, and an unknown daily cost stays --.
func TestTemplateCostAllZeroDay(t *testing.T) {
	now := time.Now()
	priced := 1.2
	for _, c := range []struct {
		daily *schema.Daily
		want  string
	}{
		{&schema.Daily{Tokens: 0}, "$0.00/d"},
		{&schema.Daily{Tokens: 5}, Missing},
		{&schema.Daily{Tokens: 5, CostUSD: &priced}, "$1.20/d"},
		{nil, Missing},
	} {
		s := schema.Status{Tools: []schema.Tool{{Tool: schema.ToolCodex, Available: true, Daily: c.daily}}}
		if got := Template("{codex.cost.all}", s, now, Style{}); got != c.want {
			t.Errorf("daily=%+v: {codex.cost.all} = %q, want %q", c.daily, got, c.want)
		}
	}
}
