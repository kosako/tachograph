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

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/menubar"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// Dropdown attention colors. Yellow/red are appearance-aware: a bright yellow
// is unreadable on the light menu, so light mode uses a darker amber/red.
const colorGray = "#8E8E93"

func attnYellow() string {
	if MenuDark {
		return "#FFD60A"
	}
	return "#B45309" // dark amber, readable on white
}

func attnRed() string {
	if MenuDark {
		return "#FF453A"
	}
	return "#D11507"
}

// Default text inks. Non-clickable info rows are auto-disabled (rendered
// gray) by macOS, so we set an explicit color to make them legible. The
// dropdown menu follows the system appearance, so MenuDark picks white/black.
const (
	inkLight = "#1A1A1A"
	inkDark  = "#F0F0F0"
)

// MenuDark is true when macOS is in dark mode (set by cmd from
// AppleInterfaceStyle), so dropdown text uses a light ink.
var MenuDark = false

func ink() string {
	if MenuDark {
		return inkDark
	}
	return inkLight
}

// BinPath is the tacho executable invoked by the clickable dropdown settings.
// cmd sets it to the running binary; tests keep the default.
var BinPath = "tacho"

// Render produces the full plugin output for one status document. dark
// selects the menu bar appearance; cfg selects which tools, metric, and
// display style to show.
func Render(s schema.Status, now time.Time, dark bool, cfg config.Config) string {
	shown := cfg.FilterStatus(s)

	var b strings.Builder
	b.WriteString(titleLine(shown, dark, cfg))
	b.WriteString("\n---\n")
	for i, t := range shown.Tools {
		if i > 0 {
			b.WriteString("---\n")
		}
		section(&b, t, now)
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "/d = 当日合計(全セッション) | color=%s size=11 %s\n", colorGray, enableParams)
	settings(&b, cfg)
	b.WriteString("Refresh | refresh=true\n")
	return b.String()
}

// settings renders the "Settings" menu with nested submenus. Each option is
// listed with the current selection check-marked; clicking an option sets it
// directly (radio for style/metric, checkbox toggle for tools) and refreshes.
func settings(b *strings.Builder, cfg config.Config) {
	b.WriteString("Settings\n")

	// Display style (radio).
	b.WriteString("--表示形式\n")
	for _, o := range []struct{ value, label string }{
		{config.StyleMeter, "メーター"},
		{config.StyleNumber, "数字"},
	} {
		clickOption(b, 2, mark(cfg.Menubar.Style == o.value)+o.label,
			"config", "set", "menubar.style", o.value)
	}

	// Metric (radio) — menu-bar-appropriate metrics (context excluded).
	b.WriteString("--指標\n")
	for _, m := range render.MenubarMetrics {
		clickOption(b, 2, mark(cfg.Menubar.Metric == m)+render.MetricLabel(m),
			"config", "set", "menubar.metric", m)
	}

	// Tools (checkbox).
	b.WriteString("--表示するツール\n")
	for _, tl := range []struct{ name, label string }{
		{schema.ToolClaudeCode, "Claude"},
		{schema.ToolCodex, "Codex"},
	} {
		clickOption(b, 2, checkbox(cfg.ToolEnabled(tl.name))+tl.label,
			"config", "toggle-tool", tl.name)
	}
}

// mark prefixes the selected radio option with a check.
func mark(selected bool) string {
	if selected {
		return "✓ "
	}
	return "    "
}

// checkbox prefixes an enabled tool with a filled box.
func checkbox(on bool) string {
	if on {
		return "☑ "
	}
	return "☐ "
}

// clickOption writes a SwiftBar submenu item at the given nesting depth that
// runs `BinPath params...` on click and refreshes.
func clickOption(b *strings.Builder, depth int, label string, params ...string) {
	b.WriteString(strings.Repeat("--", depth))
	fmt.Fprintf(b, "%s | bash=%q terminal=false refresh=true", label, BinPath)
	for i, p := range params {
		fmt.Fprintf(b, " param%d=%q", i+1, p)
	}
	b.WriteByte('\n')
}

// titleLine is the menu bar representation. With the meter style it is a
// tachometer gauge image (colored, so `image=` not the tinted
// `templateImage=`) driven by cfg.Menubar.Metric; with the number style it
// is the metric value as text. cost/tokens have no gauge fraction, so the
// meter style falls back to the number text for them. For gauge metrics,
// TACHO_SWIFTBAR_TEXT forces the moon-dial text instead of the image.
func titleLine(s schema.Status, dark bool, cfg config.Config) string {
	metric := cfg.Menubar.Metric
	// The meter (gauge) ring can only fill for percentage metrics; cost/tokens
	// have no fraction, so the ring would always be empty — fall back to the
	// number style for them.
	if cfg.Menubar.Style == config.StyleNumber || !render.MetricIsGauge(metric) {
		return numberTitle(s, metric)
	}
	if os.Getenv("TACHO_SWIFTBAR_TEXT") == "" {
		if b64, ok := menubar.PNGBase64(s, dark, metric); ok {
			return "| image=" + b64
		}
	}
	return title(s)
}

// numberTitle renders the chosen metric per tool as text, e.g. "C 24% X 7%".
func numberTitle(s schema.Status, metric string) string {
	var parts []string
	for _, t := range s.Tools {
		if !t.Available || t.Error != nil {
			continue
		}
		initial := "X"
		if t.Tool == schema.ToolClaudeCode {
			initial = "C"
		}
		_, text := render.MenubarMetric(t, metric)
		parts = append(parts, initial+" "+text)
	}
	if len(parts) == 0 {
		return "tacho " + render.Missing
	}
	return strings.Join(parts, "  ")
}

// title is the menu bar text fallback: tool initial + moon dial, "C🌒 X🌑".
// The moon tracks 5h usage, or the tool's first reported limit window when
// no 5h window exists (same fallback as the ring and the number style).
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
		if frac, _ := render.MenubarMetric(t, render.MetricLimit5h); frac != nil {
			parts = append(parts, initial+render.Moon(*frac*100))
		} else {
			parts = append(parts, initial+render.DialMissing)
		}
	}
	if len(parts) == 0 {
		return "tacho " + render.DialMissing
	}
	return strings.Join(parts, " ")
}

// enableParams attach a harmless no-op action so the menu item is "enabled":
// macOS dims action-less items (rendering even an explicit color as gray), so
// this keeps the info rows at full opacity. Clicking runs /usr/bin/true.
const enableParams = "bash=/usr/bin/true terminal=false refresh=false"

func section(b *strings.Builder, t schema.Tool, now time.Time) {
	name := "Codex"
	if t.Tool == schema.ToolClaudeCode {
		name = "Claude"
	}
	if !t.Available {
		fmt.Fprintf(b, "%s — not found | color=%s %s\n", name, colorGray, enableParams)
		return
	}
	if t.Error != nil {
		fmt.Fprintf(b, "%s — error: %s | color=%s %s\n", name, t.Error.Code, colorGray, enableParams)
		return
	}

	header := name
	if t.Model != nil {
		header += " — " + render.ModelShort(t.Model)
	}
	if t.Plan != nil {
		header += " (" + render.DisplayText(*t.Plan) + ")"
	}
	headerColor := ink()
	if t.Stale && t.CollectedAt != nil {
		header += " ⚠" + render.Age(*t.CollectedAt, now)
		headerColor = colorGray
	}
	fmt.Fprintf(b, "%s | color=%s %s\n", header, headerColor, enableParams)

	// Show every metric in the dropdown — the menu bar shows one, the
	// dropdown is the full readout. Limits carry a moon + reset time.
	limitRow(b, t, schema.WindowFiveHour, "5h", now)
	limitRow(b, t, schema.WindowWeekly, "weekly", now)
	metricRow(b, t, render.MetricContext, "context")
	metricRow(b, t, render.MetricCost, "cost")
	metricRow(b, t, render.MetricTokens, "tokens")
}

// barWidth is the gauge width for dropdown rows (space is not constrained
// here, so a bar reads better than the compact moon dial).
const barWidth = 10

// dataFont renders the per-tool rows in a monospace font so the bars and
// columns line up — the dropdown otherwise uses a proportional menu font.
const dataFont = "Menlo"

// lineBar is a thin-line gauge (━ filled, ─ empty) that looks cleaner than
// block characters in the proportional-then-monospaced menu.
func lineBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	return strings.Repeat("━", filled) + strings.Repeat("─", width-filled)
}

// labelW pads metric labels so the bars line up in the monospace font.
const labelW = 7

// limitRow renders a rate-limit window with a usage bar, reset time, and
// pressure color (or "--" when the window is absent).
func limitRow(b *strings.Builder, t schema.Tool, window, label string, now time.Time) {
	for _, l := range t.Limits {
		if l.Window == window && l.UsedPct != nil {
			line := fmt.Sprintf("%-*s %s %.0f%%", labelW, label, lineBar(*l.UsedPct, barWidth), *l.UsedPct)
			if l.ResetsAt != nil {
				line += " " + render.ResetShort(*l.ResetsAt, now)
			}
			dataRow(b, line, lineColor(t, *l.UsedPct))
			return
		}
	}
	dataRow(b, fmt.Sprintf("%-*s %s", labelW, label, render.Missing), staleOnly(t))
}

// metricRow renders context/cost/tokens. Percentage metrics get a usage bar;
// non-percentage ones (cost/tokens) are shown as plain text.
func metricRow(b *strings.Builder, t schema.Tool, metric, label string) {
	frac, text := render.Metric(t, metric)
	if frac != nil { // percentage metric: bar + color by pressure
		line := fmt.Sprintf("%-*s %s %s", labelW, label, lineBar(*frac*100, barWidth), text)
		dataRow(b, line, lineColor(t, *frac*100))
		return
	}
	dataRow(b, fmt.Sprintf("%-*s %s", labelW, label, text), staleOnly(t))
}

// dataRow writes a per-tool metric row in the monospace data font. An
// explicit color is always set: non-clickable rows are otherwise rendered
// gray (disabled) by macOS.
func dataRow(b *strings.Builder, text, color string) {
	if color == "" {
		color = ink()
	}
	fmt.Fprintf(b, "%s | font=%s color=%s %s\n", text, dataFont, color, enableParams)
}

// staleOnly returns gray for stale tools, else the normal ink.
func staleOnly(t schema.Tool) string {
	if t.Stale {
		return colorGray
	}
	return ink()
}

// lineColor colors rows that need attention (yellow/red by pressure), gray
// when stale, otherwise the normal ink.
func lineColor(t schema.Tool, pct float64) string {
	if t.Stale {
		return colorGray
	}
	switch render.PressureFor(pct) {
	case render.PressureDanger:
		return attnRed()
	case render.PressureWarn:
		return attnYellow()
	default:
		return ink()
	}
}
