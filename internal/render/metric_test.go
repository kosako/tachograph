package render

import (
	"testing"

	"github.com/kosako/tachograph/internal/schema"
)

// The menu bar's single metric excludes context.
func TestValidMenubarMetric(t *testing.T) {
	if !ValidMenubarMetric(MetricLimit5h) || !ValidMenubarMetric(MetricCost) {
		t.Error("limit_5h / cost should be valid menu bar metrics")
	}
	if ValidMenubarMetric(MetricContext) {
		t.Error("context must not be a valid menu bar metric")
	}
}

// Percentage metrics drive a gauge; cost/tokens are text-only.
func TestMetricIsGauge(t *testing.T) {
	for _, m := range []string{MetricLimit5h, MetricLimitWeekly, MetricContext} {
		if !MetricIsGauge(m) {
			t.Errorf("%s should be gauge-able", m)
		}
	}
	for _, m := range []string{MetricCost, MetricTokens} {
		if MetricIsGauge(m) {
			t.Errorf("%s should not be gauge-able (no fraction)", m)
		}
	}
}

func metricTool() schema.Tool {
	p5, ctx := 24.0, 8.0
	tokens := int64(989120)
	cost := 0.05
	return schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Session:   &schema.Session{ContextUsedPct: &ctx},
		Limits:    []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &p5}},
		Fallback:  &schema.Fallback{SessionTokens: &tokens, EstimatedCostUSD: &cost},
	}
}

func TestMetric(t *testing.T) {
	tool := metricTool()
	cases := []struct {
		metric   string
		wantText string
		wantFrac bool // whether frac is non-nil
	}{
		{MetricLimit5h, "24%", true},
		{MetricContext, "8%", true},
		{MetricCost, "$0.05", false},
		{MetricTokens, "989k", false},
		{MetricLimitWeekly, "--", false}, // no weekly limit on this tool
	}
	for _, c := range cases {
		frac, text := Metric(tool, c.metric)
		if text != c.wantText {
			t.Errorf("Metric(%s) text = %q, want %q", c.metric, text, c.wantText)
		}
		if (frac != nil) != c.wantFrac {
			t.Errorf("Metric(%s) frac present = %v, want %v", c.metric, frac != nil, c.wantFrac)
		}
	}
	if frac, _ := Metric(tool, MetricLimit5h); frac == nil || *frac < 0.23 || *frac > 0.25 {
		t.Errorf("5h frac = %v, want ~0.24", frac)
	}
}

func TestMetricUnavailable(t *testing.T) {
	if _, text := Metric(schema.Unavailable(schema.ToolCodex), MetricLimit5h); text != Missing {
		t.Errorf("unavailable text = %q, want %q", text, Missing)
	}
}

// A limit metric whose window the tool no longer reports falls back to the
// first reported limit, tagged with its window — OpenAI dropped Codex's 5h
// window in 2026-07, and the menu bar should show weekly pressure, not "--".
func TestMenubarMetricFallback(t *testing.T) {
	wk := 15.0
	weeklyOnly := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Limits:    []schema.Limit{{Window: schema.WindowWeekly, UsedPct: &wk}},
	}
	frac, text := MenubarMetric(weeklyOnly, MetricLimit5h)
	if text != "wk15%" {
		t.Errorf("MenubarMetric(5h, weekly-only) text = %q, want \"wk15%%\"", text)
	}
	if frac == nil || *frac < 0.14 || *frac > 0.16 {
		t.Errorf("MenubarMetric(5h, weekly-only) frac = %v, want ~0.15", frac)
	}

	// The configured window wins when present: no tag, identical to Metric.
	if _, text := MenubarMetric(metricTool(), MetricLimit5h); text != "24%" {
		t.Errorf("MenubarMetric(5h present) text = %q, want \"24%%\"", text)
	}
	// The fallback works in both directions (weekly configured, only 5h).
	if _, text := MenubarMetric(metricTool(), MetricLimitWeekly); text != "5h24%" {
		t.Errorf("MenubarMetric(weekly, 5h-only) text = %q, want \"5h24%%\"", text)
	}
	// No reported limits at all stays "--".
	if _, text := MenubarMetric(schema.Tool{Tool: schema.ToolCodex, Available: true}, MetricLimit5h); text != Missing {
		t.Errorf("MenubarMetric(no limits) text = %q, want %q", text, Missing)
	}
	// Non-limit metrics never fall back to a limit window.
	if _, text := MenubarMetric(weeklyOnly, MetricCost); text != Missing {
		t.Errorf("MenubarMetric(cost) text = %q, want %q", text, Missing)
	}
	// Unavailable tools stay "--" even if limits linger in the struct.
	unavailable := schema.Tool{Tool: schema.ToolCodex, Limits: weeklyOnly.Limits}
	if _, text := MenubarMetric(unavailable, MetricLimit5h); text != Missing {
		t.Errorf("MenubarMetric(unavailable) text = %q, want %q", text, Missing)
	}
}
