package render

import (
	"fmt"

	"github.com/kosako/tachograph/internal/schema"
)

// Metric identifiers selectable for display.
const (
	MetricLimit5h     = "limit_5h"     // 5-hour rate-limit usage %
	MetricLimitWeekly = "limit_weekly" // weekly rate-limit usage %
	MetricContext     = "context"      // context window usage %
	MetricCost        = "cost"         // estimated session cost
	MetricTokens      = "tokens"       // session token count
)

// Metrics lists every metric (used for the dropdown's full readout and for
// validation).
var Metrics = []string{MetricLimit5h, MetricLimitWeekly, MetricContext, MetricCost, MetricTokens}

// MenubarMetrics is the subset offered as the menu bar's single metric.
// Context is excluded: it churns per session and isn't a useful at-a-glance
// menu bar figure.
var MenubarMetrics = []string{MetricLimit5h, MetricLimitWeekly, MetricCost, MetricTokens}

// MetricLabel is a short human label for a metric id.
func MetricLabel(metric string) string {
	switch metric {
	case MetricLimit5h:
		return "5h limit"
	case MetricLimitWeekly:
		return "weekly limit"
	case MetricContext:
		return "context"
	case MetricCost:
		return "cost"
	case MetricTokens:
		return "tokens"
	}
	return metric
}

// ValidMetric reports whether metric is a known id.
func ValidMetric(metric string) bool {
	for _, m := range Metrics {
		if m == metric {
			return true
		}
	}
	return false
}

// Metric extracts a metric from a tool: frac is a 0..1 gauge fraction (nil
// when the metric is not a percentage or has no data), and text is the
// compact display string ("34%", "$0.05", "989k", or "--").
func Metric(t schema.Tool, metric string) (frac *float64, text string) {
	if !t.Available || t.Error != nil {
		return nil, Missing
	}
	switch metric {
	case MetricLimit5h:
		return limitMetric(t, schema.WindowFiveHour)
	case MetricLimitWeekly:
		return limitMetric(t, schema.WindowWeekly)
	case MetricContext:
		if t.Session != nil && t.Session.ContextUsedPct != nil {
			return pctMetric(*t.Session.ContextUsedPct)
		}
	case MetricCost:
		// Today's total across all sessions (pricing-based); the "/d" suffix
		// marks it as a daily figure. Falls back to the current session's cost.
		if t.Daily != nil && t.Daily.CostUSD != nil {
			return nil, fmt.Sprintf("$%.2f/d", *t.Daily.CostUSD)
		}
		if t.Fallback != nil && t.Fallback.EstimatedCostUSD != nil {
			return nil, fmt.Sprintf("$%.2f", *t.Fallback.EstimatedCostUSD)
		}
	case MetricTokens:
		// Today's total across all sessions; "/d" marks it daily.
		if t.Daily != nil {
			return nil, FormatTokens(t.Daily.Tokens) + "/d"
		}
		if t.Fallback != nil && t.Fallback.SessionTokens != nil {
			return nil, FormatTokens(*t.Fallback.SessionTokens)
		}
	}
	return nil, Missing
}

func limitMetric(t schema.Tool, window string) (*float64, string) {
	for _, l := range t.Limits {
		if l.Window == window && l.UsedPct != nil {
			return pctMetric(*l.UsedPct)
		}
	}
	return nil, Missing
}

func pctMetric(pct float64) (*float64, string) {
	f := pct / 100
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return &f, fmt.Sprintf("%.0f%%", pct)
}
