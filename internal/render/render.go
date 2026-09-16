// Package render turns schema values into compact terminal strings.
// It is shared by the R2 CLI renderer and the R1 statusline templates.
package render

import (
	"fmt"
	"strings"
	"time"
	"unicode"

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

// Shared pressure thresholds for used percentages.
const (
	WarnPct   = 50.0
	DangerPct = 80.0
)

// PressureLevel classifies a used percentage for renderer-specific coloring.
type PressureLevel int

const (
	// PressureOK is below WarnPct.
	PressureOK PressureLevel = iota
	// PressureWarn is at or above WarnPct and below DangerPct.
	PressureWarn
	// PressureDanger is at or above DangerPct.
	PressureDanger
)

// PressureFor classifies pct using the shared pressure thresholds.
func PressureFor(pct float64) PressureLevel {
	switch {
	case pct >= DangerPct:
		return PressureDanger
	case pct >= WarnPct:
		return PressureWarn
	default:
		return PressureOK
	}
}

// LimitDisplay selects what a rate-limit percentage shows on every surface
// (statusline, one-shot, cmux, SwiftBar, menu bar): the headroom left in the
// 5h / weekly window, or the share already used. It is the config key
// limits.display; any other value (including an unset one) reads as
// LimitRemaining, the default since #223.
type LimitDisplay string

const (
	LimitRemaining LimitDisplay = "remaining" // what's left (drains as you use it)
	LimitUsed      LimitDisplay = "used"      // what's consumed (fills as you use it)
)

// ValidLimitDisplay reports whether v is an accepted limits.display value.
func ValidLimitDisplay(v string) bool {
	switch LimitDisplay(v) {
	case LimitRemaining, LimitUsed:
		return true
	}
	return false
}

// LimitPct converts a used percentage into the value a rate-limit gauge
// shows under d, clamped to 0..100. Every surface goes through here so
// Claude and Codex read the same way whatever wording each tool's own UI
// uses (#223, #228). The schema keeps used_pct as reported, and coloring
// stays keyed on the used value via PressureFor.
func LimitPct(used float64, d LimitDisplay) float64 {
	if d == LimitUsed {
		return clampPct(used)
	}
	return RemainingPct(used)
}

// RemainingPct converts a used percentage into the headroom left in a rate
// limit window. A window reported past 100% clamps to 0 left.
func RemainingPct(used float64) float64 {
	return clampPct(100 - used)
}

func clampPct(pct float64) float64 {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

type Style struct {
	Color  bool
	Limits LimitDisplay // what 5h / weekly percentages show
}

// paintPct colors s by the pressure of a used percentage (not by what s
// displays: a limit may show its headroom but is always colored by its use).
func (st Style) paintPct(pct float64, s string) string {
	if !st.Color {
		return s
	}
	switch PressureFor(pct) {
	case PressureDanger:
		return cRed + s + cReset
	case PressureWarn:
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

// Bar renders pct (0-100) as a fixed-width gauge, e.g. "██░░░░░░".
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

// Dial renders pct (0-100) as a single-character gauge: ○◔◑◕●.
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

// Moon renders pct (0-100) as a moon-phase gauge: 🌑🌒🌓🌔🌕.
// Emoji render larger than the ○◔◑◕● glyphs, at the cost of ANSI colors
// (terminals draw emoji in their own colors).
func Moon(pct float64) string {
	switch {
	case pct < 12.5:
		return "🌑"
	case pct < 37.5:
		return "🌒"
	case pct < 62.5:
		return "🌓"
	case pct < 87.5:
		return "🌔"
	default:
		return "🌕"
	}
}

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
		return DisplayText(*m.DisplayName)
	}
	return DisplayText(strings.TrimPrefix(m.ID, "claude-"))
}

// DisplayText removes characters that can break line-oriented renderers or
// inject renderer parameters.
func DisplayText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '|' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
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
	// dim attribute mid-line, so suppress them (only the color: the limit
	// display setting still applies).
	inner := st
	if t.Stale {
		inner.Color = false
	}
	parts := []string{head, "ctx " + ctxPct(t.Session, inner)}
	if t.Limits != nil {
		for _, l := range t.Limits {
			parts = append(parts, limitPart(l, now, inner))
		}
	} else if fb := t.Fallback; fb != nil && (fb.SessionTokens != nil || fb.EstimatedCostUSD != nil) {
		// Tokens and cost are independent: the statusline route can have a
		// cost but null tokens when the session transcript is unreadable.
		var seg []string
		if fb.SessionTokens != nil {
			seg = append(seg, "tokens "+FormatTokens(*fb.SessionTokens))
		}
		if fb.EstimatedCostUSD != nil {
			seg = append(seg, fmt.Sprintf("$%.2f", *fb.EstimatedCostUSD))
		}
		parts = append(parts, strings.Join(seg, " "))
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
	used := *l.UsedPct
	shown := LimitPct(used, st.Limits)
	s := fmt.Sprintf("%s %s %s", label, st.paintPct(used, Bar(shown, 8)), st.paintPct(used, fmt.Sprintf("%2.0f%%", shown)))
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
