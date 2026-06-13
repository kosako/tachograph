// Package swiftbar is the R4 renderer: it emits the SwiftBar/xbar plugin
// format (https://github.com/swiftbar/SwiftBar#plugin-api) so tachograph
// can live in the macOS menu bar. The first line is the menu bar title;
// lines after "---" form the dropdown.
package swiftbar

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/menubar"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// Dropdown attention colors (SwiftBar `color=` parameter). Normal-pressure
// rows are left uncolored to follow the menu theme — see lineColor.
const (
	colorYellow = "#FFCC00"
	colorRed    = "#FF3B30"
	colorGray   = "#8E8E93"
)

// Render produces the full plugin output for one status document.
func Render(s schema.Status, now time.Time) string {
	var b strings.Builder

	b.WriteString(titleLine(s))
	b.WriteString("\n---\n")
	for i, t := range s.Tools {
		if i > 0 {
			b.WriteString("---\n")
		}
		section(&b, t, now)
	}
	b.WriteString("---\n")
	b.WriteString("Refresh | refresh=true\n")
	return b.String()
}

// titleLine is the menu bar representation: a tachometer gauge image per
// tool (logo ringed by a progress ring tracking 5h usage). Falls back to
// the moon-dial text when image generation is unavailable or when
// TACHO_SWIFTBAR_TEXT is set.
func titleLine(s schema.Status) string {
	if os.Getenv("TACHO_SWIFTBAR_TEXT") == "" {
		if b64, ok := menubar.PNGBase64(s); ok {
			return "| templateImage=" + b64
		}
	}
	return title(s)
}

// title is the menu bar text fallback: tool initial + 5h moon dial, "C🌒 X🌑".
func title(s schema.Status) string {
	var parts []string
	for _, t := range s.Tools {
		initial := "X"
		if t.Tool == schema.ToolClaudeCode {
			initial = "C"
		}
		if !t.Available || t.Error != nil {
			continue
		}
		if pct := fiveHourPct(t); pct != nil {
			parts = append(parts, initial+render.Moon(*pct))
		} else {
			parts = append(parts, initial+render.DialMissing)
		}
	}
	if len(parts) == 0 {
		return "tacho " + render.DialMissing
	}
	return strings.Join(parts, " ")
}

func fiveHourPct(t schema.Tool) *float64 {
	for _, l := range t.Limits {
		if l.Window == schema.WindowFiveHour {
			return l.UsedPct
		}
	}
	return nil
}

func section(b *strings.Builder, t schema.Tool, now time.Time) {
	name := "Codex"
	if t.Tool == schema.ToolClaudeCode {
		name = "Claude"
	}
	if !t.Available {
		fmt.Fprintf(b, "%s — not found | color=%s\n", name, colorGray)
		return
	}
	if t.Error != nil {
		fmt.Fprintf(b, "%s — error: %s | color=%s\n", name, t.Error.Code, colorGray)
		return
	}

	header := name
	if t.Model != nil {
		header += " — " + render.ModelShort(t.Model)
	}
	if t.Plan != nil {
		header += " (" + *t.Plan + ")"
	}
	if t.Stale && t.CollectedAt != nil {
		header += " ⚠" + render.Age(*t.CollectedAt, now)
	}
	fmt.Fprintln(b, header)

	if t.Session != nil && t.Session.ContextUsedPct != nil {
		pct := *t.Session.ContextUsedPct
		writeLine(b, fmt.Sprintf("ctx %.0f%%", pct), lineColor(t, pct))
	}
	if t.Limits != nil {
		for _, l := range t.Limits {
			if l.UsedPct == nil {
				continue
			}
			label := l.Window
			if label == schema.WindowWeekly {
				label = "wk"
			}
			line := fmt.Sprintf("%s %s %.0f%%", label, render.Moon(*l.UsedPct), *l.UsedPct)
			if l.ResetsAt != nil {
				line += " " + render.ResetShort(*l.ResetsAt, now)
			}
			writeLine(b, line, lineColor(t, *l.UsedPct))
		}
	} else if t.Fallback != nil && t.Fallback.SessionTokens != nil {
		line := "tokens " + render.FormatTokens(*t.Fallback.SessionTokens)
		if t.Fallback.EstimatedCostUSD != nil {
			line += fmt.Sprintf(" $%.2f", *t.Fallback.EstimatedCostUSD)
		}
		writeLine(b, line, lineColor(t, 0))
	}
}

// writeLine emits a dropdown row, attaching SwiftBar's color parameter only
// when one is set. Normal-pressure rows stay uncolored so they follow the
// menu's theme color and read on both light and dark backgrounds.
func writeLine(b *strings.Builder, text, color string) {
	if color == "" {
		fmt.Fprintln(b, text)
		return
	}
	fmt.Fprintf(b, "%s | color=%s\n", text, color)
}

// lineColor only colors rows that need attention: yellow/red by pressure,
// gray when stale. Normal usage returns "" (no color, theme default).
func lineColor(t schema.Tool, pct float64) string {
	if t.Stale {
		return colorGray
	}
	switch {
	case pct >= 80:
		return colorRed
	case pct >= 50:
		return colorYellow
	default:
		return ""
	}
}
