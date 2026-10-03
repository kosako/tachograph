// Package swiftbar is the R4 renderer: it emits the SwiftBar/xbar plugin
// format (https://github.com/swiftbar/SwiftBar#plugin-api) so tachograph
// can live in the macOS menu bar. The first line is the menu bar title;
// lines after "---" form the dropdown.
package swiftbar

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/menubar"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// Dropdown attention colors. Yellow/red are appearance-aware: a bright yellow
// is unreadable on the light menu, so light mode uses a darker amber/red.
const colorGray = "#8E8E93"

func (r Renderer) attnYellow() string {
	if r.MenuDark {
		return "#FFD60A"
	}
	return "#B45309" // dark amber, readable on white
}

func (r Renderer) attnRed() string {
	if r.MenuDark {
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

// Renderer is the dropdown's environment, which the status document and the
// config don't carry: the appearance its inks follow and the executable its
// clickable settings run. cmd fills it from the system appearance and the
// running binary; tests use the zero value unless they are about either.
type Renderer struct {
	// MenuDark is true when macOS is in dark mode (AppleInterfaceStyle), so
	// dropdown text uses a light ink. The menu bar title is drawn by SwiftBar
	// separately and takes its own appearance (Render's dark).
	MenuDark bool
	// BinPath is the tacho executable invoked by the clickable dropdown
	// settings.
	BinPath string
}

func (r Renderer) ink() string {
	if r.MenuDark {
		return inkDark
	}
	return inkLight
}

// HistoryDays is how many days the dropdown history covers, today included
// (#243). The closed days come from core.RecentHistory's rolling cache.
const HistoryDays = 7

// Render produces the full plugin output for one status document. dark
// selects the menu bar appearance; cfg selects which tools, metric, and
// display style to show; hist supplies the per-day rows (none when empty).
func (r Renderer) Render(s schema.Status, now time.Time, dark bool, cfg config.Config, hist core.DailyHistory) string {
	shown := cfg.FilterStatus(s)
	limits := render.LimitDisplay(cfg.Limits.Display)
	hideLimits := cfg.LimitsHidden()
	hideHistory := cfg.HistoryHidden()
	metric := effectiveMetric(cfg, hideLimits)

	var b strings.Builder
	b.WriteString(titleLine(shown, now, dark, cfg, limits, metric))
	b.WriteString("\n---\n")
	for i, t := range shown.Tools {
		if i > 0 {
			b.WriteString("---\n")
		}
		r.section(&b, t, now, limits, !hideLimits)
	}
	if !hideHistory {
		r.history(&b, hist, cfg)
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "/d = 当日合計(全セッション) | color=%s size=11 %s\n", colorGray, enableParams)
	r.settings(&b, cfg, hideLimits, hideHistory, metric)
	b.WriteString("Refresh | refresh=true\n")
	return b.String()
}

// effectiveMetric is the metric the menu bar shows: the configured one,
// except that with the limits hidden a limit window is replaced by cost (the
// next metric that still means something there). The configured value is
// left as is, so showing the limits again restores it (#301).
func effectiveMetric(cfg config.Config, hideLimits bool) string {
	if hideLimits && render.IsLimitMetric(cfg.Menubar.Metric) {
		return render.MetricCost
	}
	return cfg.Menubar.Metric
}

// settings renders the "Settings" menu with nested submenus. Each option is
// listed with the current selection check-marked; clicking an option sets it
// directly (radio for style/metric, checkbox toggle for tools and sections)
// and refreshes. With the limits hidden, the limit windows and the limit
// display drop out of the menu, and the metric check follows what the menu
// bar shows (metric) rather than the stored choice.
func (r Renderer) settings(b *strings.Builder, cfg config.Config, hideLimits, hideHistory bool, metric string) {
	b.WriteString("Settings\n")

	// Display style (radio).
	b.WriteString("--表示形式\n")
	for _, o := range []struct{ value, label string }{
		{config.StyleMeter, "メーター"},
		{config.StyleNumber, "数字"},
	} {
		r.clickOption(b, 2, mark(cfg.Menubar.Style == o.value)+o.label,
			"config", "set", "menubar.style", o.value)
	}

	// Metric (radio) — menu-bar-appropriate metrics (context excluded).
	b.WriteString("--指標\n")
	for _, m := range render.MenubarMetrics {
		if hideLimits && render.IsLimitMetric(m) {
			continue
		}
		r.clickOption(b, 2, mark(metric == m)+render.MetricLabel(m),
			"config", "set", "menubar.metric", m)
	}

	// Limit display (radio): what the 5h / weekly percentages show.
	if !hideLimits {
		b.WriteString("--リミット表示\n")
		for _, o := range []struct {
			value render.LimitDisplay
			label string
		}{
			{render.LimitRemaining, "残量"},
			{render.LimitUsed, "使用率"},
		} {
			r.clickOption(b, 2, mark(cfg.Limits.Display == string(o.value))+o.label,
				"config", "set", "limits.display", string(o.value))
		}
	}

	// Tools (checkbox).
	b.WriteString("--表示するツール\n")
	for _, tl := range []struct{ name, label string }{
		{schema.ToolClaudeCode, "Claude"},
		{schema.ToolCodex, "Codex"},
	} {
		r.clickOption(b, 2, checkbox(cfg.ToolEnabled(tl.name))+tl.label,
			"config", "toggle-tool", tl.name)
	}

	// Sections (checkbox): the limit rows and controls can be switched off
	// for backends without subscription windows — Bedrock / Vertex / an API
	// key — where they would only ever read "--" (#301), and the last-7-days
	// rows for anyone who doesn't want them (#302). A click sets the
	// opposite visibility.
	b.WriteString("--表示する項目\n")
	r.clickOption(b, 2, checkbox(!hideLimits)+"リミット(5h / weekly)",
		"config", "set", "menubar.limits", flipped(hideLimits))
	r.clickOption(b, 2, checkbox(!hideHistory)+"直近 7 日",
		"config", "set", "menubar.history", flipped(hideHistory))
}

// flipped is the visibility a section's checkbox click sets: the opposite
// of its current state.
func flipped(hidden bool) string {
	if hidden {
		return config.VisibilityShow
	}
	return config.VisibilityHide
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
func (r Renderer) clickOption(b *strings.Builder, depth int, label string, params ...string) {
	b.WriteString(strings.Repeat("--", depth))
	fmt.Fprintf(b, "%s | bash=%q terminal=false refresh=true", label, r.BinPath)
	for i, p := range params {
		fmt.Fprintf(b, " param%d=%q", i+1, p)
	}
	b.WriteByte('\n')
}

// titleLine is the menu bar representation. With the meter style it is a
// tachometer gauge image (colored, so `image=` not the tinted
// `templateImage=`) driven by metric (see effectiveMetric); with the number
// style it is the metric value as text. cost/tokens have no gauge fraction,
// so the meter style falls back to the number text for them. For gauge
// metrics, TACHO_SWIFTBAR_TEXT forces the moon-dial text instead of the
// image. A value that is an old reading is marked in every style (see
// shownStale).
func titleLine(s schema.Status, now time.Time, dark bool, cfg config.Config, limits render.LimitDisplay, metric string) string {
	// The meter (gauge) ring can only fill for percentage metrics; cost/tokens
	// have no fraction, so the ring would always be empty — fall back to the
	// number style for them.
	if cfg.Menubar.Style == config.StyleNumber || !render.MetricIsGauge(metric) {
		return numberTitle(s, now, metric, limits)
	}
	if os.Getenv("TACHO_SWIFTBAR_TEXT") == "" {
		if b64, ok := GaugePNG(s, now, dark, metric, limits); ok {
			return "| image=" + b64
		}
	}
	return title(s, now, metric, limits)
}

// shownStale reports whether the limit the menu bar shows for t is an old
// reading: the tool's row is stale, or the window was observed longer ago
// than the tool's stale threshold — use and resets since then aren't in it
// (#331). cost / tokens are recomputed from the logs on every run and are
// never shown as old.
func shownStale(t schema.Tool, now time.Time, metric string) bool {
	l, ok := render.MenubarLimit(t, metric)
	return ok && (t.Stale || core.ObservedStale(t.Tool, l, now))
}

// staleMark is appended to a menu bar value that is an old reading.
const staleMark = "⚠"

// GaugePNG is the meter style's gauge image (base64 PNG) for s, with the
// ring of each tool whose shown limit is an old reading grayed: the image the
// menu bar shows and `tacho swiftbar --png` previews (#331).
func GaugePNG(s schema.Status, now time.Time, dark bool, metric string, limits render.LimitDisplay) (string, bool) {
	return menubar.PNGBase64(gaugeStatus(s, now, metric), dark, metric, limits)
}

// gaugeStatus is s with each tool whose shown limit is an old reading marked
// stale, which grays its ring; s itself is left as is.
func gaugeStatus(s schema.Status, now time.Time, metric string) schema.Status {
	tools := make([]schema.Tool, len(s.Tools))
	for i, t := range s.Tools {
		t.Stale = t.Stale || shownStale(t, now, metric)
		tools[i] = t
	}
	s.Tools = tools
	return s
}

// numberTitle renders the chosen metric per tool as text, e.g. "C 24% X 7%",
// with an old reading marked ("C 24%⚠").
func numberTitle(s schema.Status, now time.Time, metric string, limits render.LimitDisplay) string {
	var parts []string
	for _, t := range s.Tools {
		if !t.Available || t.Error != nil {
			continue
		}
		initial := "X"
		if t.Tool == schema.ToolClaudeCode {
			initial = "C"
		}
		_, text, _ := render.MenubarMetric(t, metric, limits)
		if shownStale(t, now, metric) {
			text += staleMark
		}
		parts = append(parts, initial+" "+text)
	}
	if len(parts) == 0 {
		return "tacho " + render.Missing
	}
	return strings.Join(parts, "  ")
}

// title is the menu bar text fallback: tool initial + moon dial, "C🌔 X🌑".
// The moon shows the chosen gauge metric — for a limit window its headroom
// (full = nothing used yet) or use per limits, or the tool's first reported
// window when the chosen one is missing (same fallback as the ring and the
// number style). An old reading is marked as in the number style.
func title(s schema.Status, now time.Time, metric string, limits render.LimitDisplay) string {
	var parts []string
	for _, t := range s.Tools {
		initial := "X"
		if t.Tool == schema.ToolClaudeCode {
			initial = "C"
		}
		if !t.Available || t.Error != nil {
			continue
		}
		if frac, _, _ := render.MenubarMetric(t, metric, limits); frac != nil {
			dial := initial + render.Moon(*frac*100)
			if shownStale(t, now, metric) {
				dial += staleMark
			}
			parts = append(parts, dial)
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

// section renders one tool's dropdown block. showLimits adds the 5h / weekly
// rows; they're left out for backends without subscription windows (#301).
func (r Renderer) section(b *strings.Builder, t schema.Tool, now time.Time, limits render.LimitDisplay, showLimits bool) {
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
	headerColor := r.ink()
	if t.Stale && t.CollectedAt != nil {
		header += " ⚠" + render.Age(*t.CollectedAt, now)
		headerColor = colorGray
	}
	fmt.Fprintf(b, "%s | color=%s %s\n", header, headerColor, enableParams)

	// Show every metric in the dropdown — the menu bar shows one, the
	// dropdown is the full readout. Limits carry a moon + reset time.
	if showLimits {
		r.limitRow(b, t, schema.WindowFiveHour, "5h", now, limits)
		r.limitRow(b, t, schema.WindowWeekly, "weekly", now, limits)
	}
	r.metricRow(b, t, render.MetricContext, "context", limits)
	r.metricRow(b, t, render.MetricCost, "cost", limits)
	r.metricRow(b, t, render.MetricTokens, "tokens", limits)
}

// historyTop is how many of a tool's costliest days the history rows
// highlight (#247).
const historyTop = 3

// ANSI SGR codes for the highlight. SwiftBar maps 34 to NSColor.systemBlue,
// which adapts to the menu appearance, and 0 restores the row's own font —
// bold (SGR 1) is not used because SwiftBar renders it in the system font,
// which would knock the monospace columns out of line.
const (
	ansiBlue  = "\x1b[34m"
	ansiReset = "\x1b[0m"
)

// history renders one row per day, oldest first, with each shown tool's
// cost/tokens: the same figures as the cost / tokens rows above, for the
// last HistoryDays days. Tools follow the config order; a tool without a
// column is skipped, an unknown day reads "--". Each tool's historyTop
// costliest days are shown in blue so the heavy days stand out. A last row
// totals the window's cost per tool and, with more than one tool, across
// them (#298).
func (r Renderer) history(b *strings.Builder, h core.DailyHistory, cfg config.Config) {
	if len(h.Days) == 0 {
		return
	}
	b.WriteString("---\n")
	fmt.Fprintf(b, "直近 %d 日の cost/tokens(青 = cost 上位 %d 日) | color=%s size=11 %s\n", len(h.Days), historyTop, colorGray, enableParams)
	top := map[string]map[int]bool{}
	for _, name := range cfg.Tools {
		if col, ok := h.Tools[name]; ok {
			top[name] = topCostDays(col, historyTop)
		}
	}
	for i, day := range h.Days {
		line := historyDay(day)
		for _, name := range cfg.Tools {
			col, ok := h.Tools[name]
			if !ok {
				continue
			}
			cell := historyCell(col[i])
			if top[name][i] {
				cell = ansiBlue + cell + ansiReset
			}
			line += "  " + toolInitial(name) + " " + cell
		}
		fmt.Fprintf(b, "%s | font=%s color=%s ansi=true %s\n", line, dataFont, r.ink(), enableParams)
	}
	r.historyTotal(b, h, cfg)
}

// historyTotal renders the window's cost per shown tool and, when more than
// one tool is shown, across them ("計"). It follows the `tacho daily` total
// row: a sum reads "--" when any of its days is unknown or has tokens but no
// price, rather than silently leaving that day out. Tokens aren't totalled.
func (r Renderer) historyTotal(b *strings.Builder, h core.DailyHistory, cfg config.Config) {
	line := fmt.Sprintf("%d日計", len(h.Days))
	var all []*schema.Daily
	shown := 0
	for _, name := range cfg.Tools {
		col, ok := h.Tools[name]
		if !ok {
			continue
		}
		line += "  " + toolInitial(name) + " " + render.DailyCostSum(col)
		all = append(all, col...)
		shown++
	}
	if shown == 0 {
		return
	}
	if shown > 1 {
		line += "  計 " + render.DailyCostSum(all)
	}
	fmt.Fprintf(b, "%s | font=%s color=%s %s\n", line, dataFont, r.ink(), enableParams)
}

// topCostDays picks the indexes of the n costliest days in col. Only days
// with a known, non-zero cost qualify; ties keep the earlier day.
func topCostDays(col []*schema.Daily, n int) map[int]bool {
	var idx []int
	for i, d := range col {
		if d != nil && d.CostUSD != nil && *d.CostUSD > 0 {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return *col[idx[a]].CostUSD > *col[idx[b]].CostUSD })
	out := map[int]bool{}
	for _, i := range idx[:min(n, len(idx))] {
		out[i] = true
	}
	return out
}

// historyDay shortens a "2006-01-02" day key to MM/DD, as the reset times
// are shown; a key in another shape is printed as is.
func historyDay(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.Format("01/02")
}

// historyCell is one tool's cost/tokens for a day, padded so the columns
// line up in the data font; nil (unknown) reads "--".
func historyCell(d *schema.Daily) string {
	if d == nil {
		return fmt.Sprintf("%-14s", render.Missing)
	}
	return fmt.Sprintf("%7s/%-6s", render.DailyCost(d), render.FormatTokens(d.Tokens))
}

func toolInitial(name string) string {
	if name == schema.ToolClaudeCode {
		return "C"
	}
	return "X"
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

// limitRow renders a rate-limit window with a bar and figure showing what
// is left or what is used per limits, reset time, and pressure color by use
// (or "--" when the window is absent). A window observed longer ago than the
// tool's stale threshold is grayed and marked with its age, "⚠3h" (#331).
func (r Renderer) limitRow(b *strings.Builder, t schema.Tool, window, label string, now time.Time, limits render.LimitDisplay) {
	for _, l := range t.Limits {
		if l.Window == window && l.UsedPct != nil {
			used := *l.UsedPct
			shown := render.LimitPct(used, limits)
			line := fmt.Sprintf("%-*s %s %.0f%%", labelW, label, lineBar(shown, barWidth), shown)
			if l.ResetsAt != nil {
				line += " " + render.ResetShort(*l.ResetsAt, now)
			}
			color := r.lineColor(t, render.PressureFor(used))
			if core.ObservedStale(t.Tool, l, now) {
				line += " " + staleMark + render.Age(*l.ObservedAt, now)
				color = colorGray
			}
			r.dataRow(b, line, color)
			return
		}
	}
	r.dataRow(b, fmt.Sprintf("%-*s %s", labelW, label, render.Missing), r.staleOnly(t))
}

// metricRow renders context/cost/tokens. Percentage metrics get a usage bar;
// non-percentage ones (cost/tokens) are shown as plain text.
func (r Renderer) metricRow(b *strings.Builder, t schema.Tool, metric, label string, limits render.LimitDisplay) {
	frac, text, pressure := render.Metric(t, metric, limits)
	if frac != nil { // percentage metric: bar + color by pressure
		line := fmt.Sprintf("%-*s %s %s", labelW, label, lineBar(*frac*100, barWidth), text)
		r.dataRow(b, line, r.lineColor(t, pressure))
		return
	}
	r.dataRow(b, fmt.Sprintf("%-*s %s", labelW, label, text), r.staleOnly(t))
}

// dataRow writes a per-tool metric row in the monospace data font. An
// explicit color is always set: non-clickable rows are otherwise rendered
// gray (disabled) by macOS.
func (r Renderer) dataRow(b *strings.Builder, text, color string) {
	if color == "" {
		color = r.ink()
	}
	fmt.Fprintf(b, "%s | font=%s color=%s %s\n", text, dataFont, color, enableParams)
}

// staleOnly returns gray for stale tools, else the normal ink.
func (r Renderer) staleOnly(t schema.Tool) string {
	if t.Stale {
		return colorGray
	}
	return r.ink()
}

// lineColor colors rows that need attention (yellow/red by pressure), gray
// when stale, otherwise the normal ink.
func (r Renderer) lineColor(t schema.Tool, pressure render.PressureLevel) string {
	if t.Stale {
		return colorGray
	}
	switch pressure {
	case render.PressureDanger:
		return r.attnRed()
	case render.PressureWarn:
		return r.attnYellow()
	default:
		return r.ink()
	}
}
