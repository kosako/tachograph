package render

import (
	"testing"

	"github.com/kosako/tachograph/internal/schema"
)

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

func TestValidMetric(t *testing.T) {
	if !ValidMetric(MetricCost) || ValidMetric("nope") {
		t.Error("ValidMetric mismatch")
	}
}
