// Package render turns schema values into compact terminal strings.
// It is shared by the R2 CLI renderer and the R1 statusline templates.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// ANSI colors keyed by limit pressure.
const (
	cReset  = "\x1b[0m"
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
	cRed    = "\x1b[31m"
	cDim    = "\x1b[2m"
)

type Style struct {
	Color bool
}

func (st Style) paintPct(pct float64, s string) string {
	if !st.Color {
		return s
	}
	switch {
	case pct >= 80:
		return cRed + s + cReset
	case pct >= 50:
		return cYellow + s + cReset
	default:
		return cGreen + s + cReset
	}
}

func (st Style) dim(s string) string {
	if !st.Color {
		return s
	}
	return cDim + s + cReset
}

// Bar renders pct (0-100, used) as a fixed-width gauge, e.g. "██░░░░░░".
func Bar(pct float64, width int) string {
	if width <= 0 {
		width = 8
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// Dial renders pct (0-100, used) as a single-character gauge: ○◔◑◕●.
func Dial(pct float64) string {
	switch {
	case pct < 12.5:
		return "○"
	case pct < 37.5:
		return "◔"
	case pct < 62.5:
		return "◑"
	case pct < 87.5:
		return "◕"
	default:
		return "●"
	}
}

// DialMissing keeps single-character alignment when a dial has no data.
const DialMissing = "◌"

// FormatTokens compacts a token count: 989120 → "989k", 12504028 → "12.5M".
func FormatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1_000_000), ".0") + "M"
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// Age renders the elapsed time since an RFC 3339 timestamp compactly:
// "42s", "5m", "1h", "3d". Empty on parse failure or future timestamps.
func Age(iso string, now time.Time) string {
	ts, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	d := now.Sub(ts)
	switch {
	case d < 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// staleMark renders "⚠1h" for stale data (age omitted when unknown).
func staleMark(t schema.Tool, now time.Time) string {
	if !t.Stale {
		return ""
	}
	if t.CollectedAt != nil {
		if a := Age(*t.CollectedAt, now); a != "" {
			return "⚠" + a
		}
	}
	return "⚠"
}

// ResetShort renders an RFC 3339 reset time as "↻HH:MM" if it falls within
// the next 24h, otherwise "↻MM/DD".
func ResetShort(iso string, now time.Time) string {
	ts, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "↻--"
	}
	ts = ts.Local()
	if d := ts.Sub(now); d >= 0 && d < 24*time.Hour {
		return ts.Format("↻15:04")
	}
	return ts.Format("↻01/02")
}

// ModelShort shortens a model id for display: prefers display_name, strips
// the redundant "claude-" prefix.
func ModelShort(m *schema.Model) string {
	if m == nil {
		return "--"
	}
	if m.DisplayName != nil && *m.DisplayName != "" {
		return *m.DisplayName
	}
	return strings.TrimPrefix(m.ID, "claude-")
}

// ToolLine renders one tool as a single compact line.
func ToolLine(t schema.Tool, now time.Time, st Style) string {
	name := t.Tool
	if name == schema.ToolClaudeCode {
		name = "claude"
	}
	head := fmt.Sprintf("%-6s %-14s %-4s", name, ModelShort(t.Model), staleMark(t, now))

	if !t.Available {
		return head + st.dim(" (not found)")
	}
	if t.Error != nil {
		return head + st.dim(" (error: "+t.Error.Code+")")
	}

	// Stale lines are dimmed as a whole; per-part colors would reset the
	// dim attribute mid-line, so suppress them.
	inner := st
	if t.Stale {
		inner = Style{}
	}
	parts := []string{head, "ctx " + ctxPct(t.Session, inner)}
	if t.Limits != nil {
		for _, l := range t.Limits {
			parts = append(parts, limitPart(l, now, inner))
		}
	} else if fb := t.Fallback; fb != nil && fb.SessionTokens != nil {
		s := "tokens " + FormatTokens(*fb.SessionTokens)
		if fb.EstimatedCostUSD != nil {
			s += fmt.Sprintf(" $%.2f", *fb.EstimatedCostUSD)
		}
		parts = append(parts, s)
	}
	line := strings.Join(parts, "  ")
	if t.Stale {
		line = st.dim(line)
	}
	return line
}

func ctxPct(s *schema.Session, st Style) string {
	if s == nil || s.ContextUsedPct == nil {
		return "--%"
	}
	pct := *s.ContextUsedPct
	return st.paintPct(pct, fmt.Sprintf("%.0f%%", pct))
}

func limitPart(l schema.Limit, now time.Time, st Style) string {
	label := l.Window
	if label == schema.WindowWeekly {
		label = "wk"
	}
	if l.UsedPct == nil {
		return label + " --%"
	}
	pct := *l.UsedPct
	s := fmt.Sprintf("%s %s %s", label, st.paintPct(pct, Bar(pct, 8)), st.paintPct(pct, fmt.Sprintf("%2.0f%%", pct)))
	if l.ResetsAt != nil {
		s += " " + st.dim(ResetShort(*l.ResetsAt, now))
	}
	return s
}

// StatusLines renders the whole document, one line per tool.
func StatusLines(s schema.Status, now time.Time, st Style) string {
	var b strings.Builder
	for i, t := range s.Tools {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ToolLine(t, now, st))
	}
	return b.String()
}
