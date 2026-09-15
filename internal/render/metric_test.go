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

// Rate-limit metrics fill with headroom but carry the pressure of their use,
// so a nearly exhausted window shows a small number in the danger color (#223).
func TestMetricLimitPressureFollowsUse(t *testing.T) {
	used := 85.0
	tool := schema.Tool{Tool: schema.ToolClaudeCode, Available: true,
		Limits: []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &used}}}
	frac, text, pressure := Metric(tool, MetricLimit5h)
	if text != "15%" || frac == nil || *frac < 0.14 || *frac > 0.16 {
		t.Errorf("Metric(5h, 85%% used) = %v %q, want ~0.15 \"15%%\"", frac, text)
	}
	if pressure != PressureDanger {
		t.Errorf("pressure = %v, want PressureDanger (colored by use, not headroom)", pressure)
	}
	if _, text, p := MenubarMetric(tool, MetricLimitWeekly); text != "5h15%" || p != PressureDanger {
		t.Errorf("MenubarMetric fallback = %q %v, want \"5h15%%\" PressureDanger", text, p)
	}
	if _, _, p := Metric(metricTool(), MetricLimit5h); p != PressureOK {
		t.Errorf("pressure at 24%% used = %v, want PressureOK", p)
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
		{MetricLimit5h, "76%", true}, // 24% used → 76% left
		{MetricContext, "8%", true},  // context stays usage
		{MetricCost, "$0.05", false},
		{MetricTokens, "989k", false},
		{MetricLimitWeekly, "--", false}, // no weekly limit on this tool
	}
	for _, c := range cases {
		frac, text, _ := Metric(tool, c.metric)
		if text != c.wantText {
			t.Errorf("Metric(%s) text = %q, want %q", c.metric, text, c.wantText)
		}
		if (frac != nil) != c.wantFrac {
			t.Errorf("Metric(%s) frac present = %v, want %v", c.metric, frac != nil, c.wantFrac)
		}
	}
	if frac, _, _ := Metric(tool, MetricLimit5h); frac == nil || *frac < 0.75 || *frac > 0.77 {
		t.Errorf("5h frac = %v, want ~0.76 (headroom)", frac)
	}
}

func TestMetricUnavailable(t *testing.T) {
	if _, text, _ := Metric(schema.Unavailable(schema.ToolCodex), MetricLimit5h); text != Missing {
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
	frac, text, _ := MenubarMetric(weeklyOnly, MetricLimit5h)
	if text != "wk85%" { // 15% used → 85% left
		t.Errorf("MenubarMetric(5h, weekly-only) text = %q, want \"wk85%%\"", text)
	}
	if frac == nil || *frac < 0.84 || *frac > 0.86 {
		t.Errorf("MenubarMetric(5h, weekly-only) frac = %v, want ~0.85", frac)
	}

	// The configured window wins when present: no tag, identical to Metric.
	if _, text, _ := MenubarMetric(metricTool(), MetricLimit5h); text != "76%" {
		t.Errorf("MenubarMetric(5h present) text = %q, want \"76%%\"", text)
	}
	// The fallback works in both directions (weekly configured, only 5h).
	if _, text, _ := MenubarMetric(metricTool(), MetricLimitWeekly); text != "5h76%" {
		t.Errorf("MenubarMetric(weekly, 5h-only) text = %q, want \"5h76%%\"", text)
	}
	// No reported limits at all stays "--".
	if _, text, _ := MenubarMetric(schema.Tool{Tool: schema.ToolCodex, Available: true}, MetricLimit5h); text != Missing {
		t.Errorf("MenubarMetric(no limits) text = %q, want %q", text, Missing)
	}
	// Non-limit metrics never fall back to a limit window.
	if _, text, _ := MenubarMetric(weeklyOnly, MetricCost); text != Missing {
		t.Errorf("MenubarMetric(cost) text = %q, want %q", text, Missing)
	}
	// Unavailable tools stay "--" even if limits linger in the struct.
	unavailable := schema.Tool{Tool: schema.ToolCodex, Limits: weeklyOnly.Limits}
	if _, text, _ := MenubarMetric(unavailable, MetricLimit5h); text != Missing {
		t.Errorf("MenubarMetric(unavailable) text = %q, want %q", text, Missing)
	}
	// Error'd tools stay "--" even if limits linger in the struct.
	errored := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Error:     &schema.Error{Code: "parse_error"},
		Limits:    weeklyOnly.Limits,
	}
	if _, text, _ := MenubarMetric(errored, MetricLimit5h); text != Missing {
		t.Errorf("MenubarMetric(errored) text = %q, want %q", text, Missing)
	}
	// A window reported without a value is skipped both as the configured
	// window and inside the fallback scan: 5h present but valueless falls
	// through to the weekly value.
	nilFirst := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Limits: []schema.Limit{
			{Window: schema.WindowFiveHour},
			{Window: schema.WindowWeekly, UsedPct: &wk},
		},
	}
	if _, text, _ := MenubarMetric(nilFirst, MetricLimit5h); text != "wk85%" {
		t.Errorf("MenubarMetric(nil-first) text = %q, want \"wk85%%\"", text)
	}
	// All-valueless limits stay "--".
	nilOnly := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Limits:    []schema.Limit{{Window: schema.WindowWeekly}},
	}
	if _, text, _ := MenubarMetric(nilOnly, MetricLimit5h); text != Missing {
		t.Errorf("MenubarMetric(nil-only) text = %q, want %q", text, Missing)
	}
}
