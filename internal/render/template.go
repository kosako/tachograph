package render

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// DefaultTemplate is the statusline rendering used when the user has no
// statusline.tmpl. Kept to one line and modest width.
const DefaultTemplate = "{claude.model} {claude.effort}{claude.stale}ctx {claude.ctx} · 5h {claude.5h.bar:6} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.pct} · codex {codex.stale}5h {codex.5h.pct} wk {codex.wk.pct}"

// Missing is rendered for placeholders whose value is absent.
const Missing = "--"

// maxWidth caps placeholder width modifiers: bars are statusline furniture,
// and a typo like {tool.5h.bar:100000} must not flood the terminal with
// hundreds of kilobytes (#194 L-05).
const maxWidth = 120

// FirstTemplateLine returns the first usable line of a statusline.tmpl file:
// the first line that is non-empty after trimming and does not start with '#'.
// This lets the shipped example file carry several commented presets so users
// pick one by uncommenting it. Returns "" when there's no usable line.
func FirstTemplateLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return t
	}
	return ""
}

var placeholderRe = regexp.MustCompile(`\{([a-z0-9.]+)(?::([0-9]+))?\}`)

// Template expands the simple {tool.field[:width]} placeholder syntax
// (docs in README) against a status document.
//
//	{claude.model} {claude.ctx} {claude.tokens} {claude.cost} {claude.plan}
//	{claude.cwd} {claude.stale} {claude.5h.pct} {claude.5h.bar:8}
//	{claude.5h.resets} {claude.wk...} — same fields under codex.*
//
// tokens/cost take an optional scope: bare or .session = current session,
// .session.today = current session's today-only portion (Claude only),
// .all = today's all-session total (rendered with a /d marker).
func Template(tmpl string, s schema.Status, now time.Time, st Style) string {
	tools := map[string]*schema.Tool{}
	for i := range s.Tools {
		switch s.Tools[i].Tool {
		case schema.ToolClaudeCode:
			tools["claude"] = &s.Tools[i]
		case schema.ToolCodex:
			tools["codex"] = &s.Tools[i]
		}
	}
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		sub := placeholderRe.FindStringSubmatch(m)
		key, widthStr := sub[1], sub[2]
		width := 0
		if widthStr != "" {
			width, _ = strconv.Atoi(widthStr)
			if width > maxWidth {
				width = maxWidth
			}
		}
		parts := strings.Split(key, ".")
		tool, ok := tools[parts[0]]
		if !ok || len(parts) < 2 {
			return Missing
		}
		return resolve(tool, parts[1:], width, now, st)
	})
}

func resolve(t *schema.Tool, path []string, width int, now time.Time, st Style) string {
	switch path[0] {
	case "stale":
		if m := staleMark(*t, now); m != "" {
			return m + " "
		}
		return ""
	case "age":
		if t.CollectedAt == nil {
			return Missing
		}
		return Age(*t.CollectedAt, now)
	case "model":
		if !t.Available || t.Model == nil {
			return Missing
		}
		return ModelShort(t.Model)
	case "effort":
		// An optional model attribute: render "⚡<short> " (trailing space,
		// like {stale}) when present, empty otherwise — so it slots in front of
		// the next token without leaving a gap when the model has no effort.
		if !t.Available || t.Model == nil || t.Model.Effort == nil || *t.Model.Effort == "" {
			return ""
		}
		return "⚡" + DisplayText(effortShort(*t.Model.Effort)) + " "
	case "ctx":
		if t.Session == nil || t.Session.ContextUsedPct == nil {
			return Missing
		}
		return st.paintPct(*t.Session.ContextUsedPct, fmt.Sprintf("%.0f%%", *t.Session.ContextUsedPct))
	case "tokens":
		return resolveTokens(t, path[1:])
	case "cost":
		return resolveCost(t, path[1:])
	case "plan":
		if t.Plan == nil {
			return Missing
		}
		return DisplayText(*t.Plan)
	case "credits":
		if t.Credits == nil {
			return Missing
		}
		return formatCredits(*t.Credits)
	case "cwd":
		if t.Session == nil || t.Session.CWD == nil {
			return Missing
		}
		// The statusline goes straight to a terminal: strip control
		// characters from log-sourced text (#194 L-05).
		return DisplayText(filepath.Base(*t.Session.CWD))
	case "5h", "wk":
		return resolveLimit(t, path, width, now, st)
	}
	return Missing
}

// tokensScope / costScope choose between the current session and today's
// all-session total. The bare {tool.tokens} / {tool.cost} and the explicit
// .session form both mean the current session (back-compat); .all means
// today's total across every session, rendered with a "/d" marker so it reads
// as a daily figure. .session.today means the current session's today-only
// portion when the collector can derive it.
func scopeName(scope []string) string {
	switch {
	case len(scope) == 0, len(scope) == 1 && scope[0] == "session":
		return "session"
	case len(scope) == 1 && scope[0] == "all":
		return "all"
	case len(scope) == 2 && scope[0] == "session" && scope[1] == "today":
		return "session.today"
	}
	return ""
}

func resolveTokens(t *schema.Tool, scope []string) string {
	switch scopeName(scope) {
	case "session":
		if t.Session == nil || t.Session.Tokens == nil {
			return Missing
		}
		return FormatTokens(t.Session.Tokens.Total)
	case "session.today":
		if t.SessionToday == nil {
			return Missing
		}
		return FormatTokens(t.SessionToday.Tokens)
	case "all":
		if t.Daily == nil {
			return Missing
		}
		return FormatTokens(t.Daily.Tokens) + "/d"
	}
	return Missing
}

func resolveCost(t *schema.Tool, scope []string) string {
	switch scopeName(scope) {
	case "session":
		if t.Fallback == nil || t.Fallback.EstimatedCostUSD == nil {
			return Missing
		}
		return fmt.Sprintf("$%.2f", *t.Fallback.EstimatedCostUSD)
	case "session.today":
		if t.SessionToday == nil || t.SessionToday.CostUSD == nil {
			return Missing
		}
		return fmt.Sprintf("$%.2f", *t.SessionToday.CostUSD)
	case "all":
		if t.Daily == nil || t.Daily.CostUSD == nil {
			return Missing
		}
		return fmt.Sprintf("$%.2f/d", *t.Daily.CostUSD)
	}
	return Missing
}

// formatCredits renders a credit balance compactly, trimming trailing
// zeros: 23.50 → "23.5", 1234.00 → "1234".
func formatCredits(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// effortShort compacts a reasoning-effort level for the status line. Unknown
// (future) levels are passed through verbatim rather than dropped.
func effortShort(level string) string {
	switch level {
	case "medium":
		return "med"
	case "xhigh":
		return "xhi"
	default: // low, high, max already short
		return level
	}
}

func resolveLimit(t *schema.Tool, path []string, width int, now time.Time, st Style) string {
	window := schema.WindowFiveHour
	if path[0] == "wk" {
		window = schema.WindowWeekly
	}
	var limit *schema.Limit
	for i := range t.Limits {
		if t.Limits[i].Window == window {
			limit = &t.Limits[i]
			break
		}
	}
	field := "pct"
	if len(path) > 1 {
		field = path[1]
	}
	if limit == nil || limit.UsedPct == nil {
		switch field {
		case "bar":
			return Bar(0, width) // keep alignment even when absent
		case "dial", "moon":
			return DialMissing
		}
		return Missing
	}
	pct := *limit.UsedPct
	switch field {
	case "pct":
		return st.paintPct(pct, fmt.Sprintf("%.0f%%", pct))
	case "bar":
		return st.paintPct(pct, Bar(pct, width))
	case "dial":
		return st.paintPct(pct, Dial(pct))
	case "moon":
		return Moon(pct) // emoji ignore ANSI colors; no paint
	case "resets":
		if limit.ResetsAt == nil {
			return Missing
		}
		return st.dim(ResetShort(*limit.ResetsAt, now))
	}
	return Missing
}
