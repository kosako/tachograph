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
const DefaultTemplate = "{claude.model} {claude.stale}ctx {claude.ctx} · 5h {claude.5h.bar:6} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.pct} · codex {codex.stale}5h {codex.5h.pct} wk {codex.wk.pct}"

// Missing is rendered for placeholders whose value is absent.
const Missing = "--"

var placeholderRe = regexp.MustCompile(`\{([a-z0-9.]+)(?::([0-9]+))?\}`)

// Template expands the simple {tool.field[:width]} placeholder syntax
// (docs in README) against a status document.
//
//	{claude.model} {claude.ctx} {claude.tokens} {claude.cost} {claude.plan}
//	{claude.cwd} {claude.stale} {claude.5h.pct} {claude.5h.bar:8}
//	{claude.5h.resets} {claude.wk...} — same fields under codex.*
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
	case "ctx":
		if t.Session == nil || t.Session.ContextUsedPct == nil {
			return Missing
		}
		return st.paintPct(*t.Session.ContextUsedPct, fmt.Sprintf("%.0f%%", *t.Session.ContextUsedPct))
	case "tokens":
		if t.Session == nil || t.Session.Tokens == nil {
			return Missing
		}
		return FormatTokens(t.Session.Tokens.Total)
	case "cost":
		if t.Fallback == nil || t.Fallback.EstimatedCostUSD == nil {
			return Missing
		}
		return fmt.Sprintf("$%.2f", *t.Fallback.EstimatedCostUSD)
	case "plan":
		if t.Plan == nil {
			return Missing
		}
		return *t.Plan
	case "cwd":
		if t.Session == nil || t.Session.CWD == nil {
			return Missing
		}
		return filepath.Base(*t.Session.CWD)
	case "5h", "wk":
		return resolveLimit(t, path, width, now, st)
	}
	return Missing
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
