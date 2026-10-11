package render

import (
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func testStatus() schema.Status {
	claude := limitsTool()
	tokens := int64(989120)
	cost := 0.05
	claude.Session.Tokens = &schema.Tokens{Total: tokens}
	claude.Fallback = &schema.Fallback{SessionTokens: &tokens, EstimatedCostUSD: &cost}
	dailyTokens := int64(12_700_000)
	dailyCost := 1.2
	claude.Daily = &schema.Daily{Tokens: dailyTokens, CostUSD: &dailyCost}
	todayTokens := int64(120_000)
	todayCost := 0.03
	claude.SessionToday = &schema.Daily{Tokens: todayTokens, CostUSD: &todayCost}
	cwd := "/Users/example/dev/project"
	claude.Session.CWD = &cwd
	credits := 23.50
	claude.Credits = &credits

	codex := schema.Unavailable(schema.ToolCodex)
	return schema.Status{Tools: []schema.Tool{claude, codex}}
}

// Limits render their headroom but are colored by their use (#223): 59%
// used shows "41%" in the warning color, not the green a 41% usage would get.
func TestTemplateLimitHeadroomColoredByUse(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00") // before limitsTool()'s 5h reset
	used := 59.0
	tool := limitsTool()
	tool.Limits[0].UsedPct = &used // 5h window
	s := schema.Status{Tools: []schema.Tool{tool}}
	color := Style{Color: true}
	if got, want := Template("{claude.5h.pct}", s, now, color), cYellow+"41%"+cReset; got != want {
		t.Errorf("pct = %q, want %q (headroom text, warn color)", got, want)
	}
	if got, want := Template("{claude.5h.bar:4}", s, now, color), cYellow+"██░░"+cReset; got != want {
		t.Errorf("bar = %q, want %q (bar fills with headroom)", got, want)
	}
	if got, want := Template("{claude.5h.dial}", s, now, plain), "◑"; got != want {
		t.Errorf("dial = %q, want %q (41%% left)", got, want)
	}
	if got, want := Template("{claude.5h.moon}", s, now, plain), "🌓"; got != want {
		t.Errorf("moon = %q, want %q (41%% left)", got, want)
	}
}

// With the used display the same limit reads as its use, still colored by
// use (#228): 59% used shows "59%" in the warning color, and the gauges fill
// with use instead of draining.
func TestTemplateLimitUsedDisplayColoredByUse(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00") // before limitsTool()'s 5h reset
	used := 59.0
	tool := limitsTool()
	tool.Limits[0].UsedPct = &used // 5h window
	s := schema.Status{Tools: []schema.Tool{tool}}
	color := Style{Color: true, Limits: LimitUsed}
	usedPlain := Style{Limits: LimitUsed}
	if got, want := Template("{claude.5h.pct}", s, now, color), cYellow+"59%"+cReset; got != want {
		t.Errorf("pct = %q, want %q (used text, warn color)", got, want)
	}
	if got, want := Template("{claude.5h.bar:4}", s, now, color), cYellow+"██░░"+cReset; got != want {
		t.Errorf("bar = %q, want %q (bar fills with use)", got, want)
	}
	if got, want := Template("{claude.5h.dial}", s, now, usedPlain), "◑"; got != want {
		t.Errorf("dial = %q, want %q (59%% used)", got, want)
	}
	if got, want := Template("{claude.5h.moon}", s, now, usedPlain), "🌓"; got != want {
		t.Errorf("moon = %q, want %q (59%% used)", got, want)
	}
	// A value where headroom and use fall in different dial buckets.
	used = 85
	if got, want := Template("{claude.5h.dial} {claude.5h.moon} {claude.5h.pct}", s, now, usedPlain), "◕ 🌔 85%"; got != want {
		t.Errorf("used 85%% = %q, want %q", got, want)
	}
	if got, want := Template("{claude.5h.dial} {claude.5h.moon} {claude.5h.pct}", s, now, plain), "◔ 🌒 15%"; got != want {
		t.Errorf("remaining 85%% used = %q, want %q", got, want)
	}
}

func TestTemplateBasics(t *testing.T) {
	setLocal(t, resetZone)
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	s := testStatus()

	cases := map[string]string{
		"{claude.model}":                "Fable 5",
		"{claude.effort}":               "⚡xhi ", // xhigh, trailing space like {stale}
		"{codex.effort}":                "",      // unavailable tool → no effort
		"{claude.ctx}":                  "8%",
		"{claude.5h.pct}":               "76%",  // 23.5% used → 76% left (limits show headroom)
		"{claude.5h}":                   "76%",  // bare window defaults to pct
		"{claude.wk.pct}":               "59%",  // 41.2% used → 59% left
		"{claude.5h.bar:4}":             "███░", // bar fills with headroom
		"{claude.5h.dial}":              "◕",    // 76% left
		"{claude.wk.dial}":              "◑",    // 59% left
		"{codex.5h.dial}":               DialMissing,
		"{claude.5h.moon}":              "🌔", // 76% left
		"{codex.5h.moon}":               DialMissing,
		"{claude.5h.resets}":            "↻10:00", // 02:00+09:00 in resetZone
		"{claude.tokens}":               "989k",
		"{claude.tokens.session}":       "989k",
		"{claude.tokens.session.today}": "120k",
		"{claude.tokens.all}":           "12.7M/d",
		"{claude.cost}":                 "$0.05",
		"{claude.cost.session}":         "$0.05",
		"{claude.cost.session.today}":   "$0.03",
		"{claude.cost.all}":             "$1.20/d",
		"{codex.tokens.all}":            Missing, // codex unavailable
		"{codex.cost.session.today}":    Missing, // codex has no session.today
		"{claude.cwd}":                  "project",
		"{claude.stale}":                "",
		"{codex.model}":                 Missing, // unavailable tool
		"{codex.5h.pct}":                Missing,
		"{codex.5h.bar:4}":              "┄┄┄┄", // bars keep their width when absent, unlike an empty ░ gauge (#271)
		"{codex.5h.bar}":                "┄┄┄┄┄┄┄┄",
		"{claude.plan}":                 Missing,
		"{claude.credits}":              "23.5", // trailing zeros trimmed
		"{codex.credits}":               Missing,
		"{claude.nope}":                 Missing,
		"{nope.model}":                  Missing,
	}
	for tmpl, want := range cases {
		if got := Template(tmpl, s, now, plain); got != want {
			t.Errorf("Template(%q) = %q, want %q", tmpl, got, want)
		}
	}
}

func TestTemplateComposite(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := Template("[{claude.model}] 5h {claude.5h.bar:8} {claude.5h.pct}", testStatus(), now, plain)
	want := "[Fable 5] 5h ██████░░ 76%" // 23.5% used → 76% left
	if got != want {
		t.Errorf("Template = %q, want %q", got, want)
	}
}

func TestTemplateSanitizesModelAndPlan(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	s := testStatus()
	model := "Fable | bash=/tmp/pwn\nForged\x1b"
	plan := "pro | href=https://example.invalid\n--fake"
	s.Tools[0].Model.DisplayName = &model
	s.Tools[0].Plan = &plan

	got := Template("{claude.model} / {claude.plan}", s, now, plain)
	want := "Fable  bash=/tmp/pwnForged / pro  href=https://example.invalid--fake"
	if got != want {
		t.Errorf("Template = %q, want %q", got, want)
	}
}

func TestTemplateStaleMarker(t *testing.T) {
	s := testStatus()
	s.Tools[0].Stale = true
	got := Template("{claude.stale}ctx", s, time.Now(), plain)
	if !strings.HasPrefix(got, "⚠ ") {
		t.Errorf("Template = %q, want stale marker prefix", got)
	}

	now := time.Now()
	collected := now.Add(-90 * time.Minute).Format(time.RFC3339)
	s.Tools[0].CollectedAt = &collected
	if got := Template("{claude.stale}", s, now, plain); got != "⚠1h " {
		t.Errorf("Template stale with age = %q, want \"⚠1h \"", got)
	}
	if got := Template("{claude.age}", s, now, plain); got != "1h" {
		t.Errorf("Template age = %q, want \"1h\"", got)
	}
	s.Tools[1].CollectedAt = nil
	if got := Template("{codex.age}", s, now, plain); got != Missing {
		t.Errorf("Template age (nil collected_at) = %q, want %q", got, Missing)
	}
}

func TestDefaultTemplateRenders(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	got := Template(DefaultTemplate, testStatus(), now, plain)
	if strings.Contains(got, "{") {
		t.Errorf("DefaultTemplate left unexpanded placeholders: %q", got)
	}
}

// {effort} must slot in cleanly: a marker with trailing space when present,
// and nothing (no "--", no double space) when the model reports no effort.
func TestTemplateEffort(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	tmpl := "{claude.model} {claude.effort}ctx {claude.ctx}"

	s := testStatus() // limitsTool() sets effort "xhigh"
	if got, want := Template(tmpl, s, now, plain), "Fable 5 ⚡xhi ctx 8%"; got != want {
		t.Errorf("effort present: Template = %q, want %q", got, want)
	}

	s.Tools[0].Model.Effort = nil
	if got, want := Template(tmpl, s, now, plain), "Fable 5 ctx 8%"; got != want {
		t.Errorf("effort absent: Template = %q, want %q", got, want)
	}
}

// A window whose reset time has passed renders as absent in every field: its
// use belongs to an ended window (#374). The running weekly window is kept.
func TestTemplateResetPassedWindowAbsent(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T02:00:00+09:00") // limitsTool()'s 5h reset
	s := testStatus()
	for tmpl, want := range map[string]string{
		"{claude.5h.pct}":    Missing,
		"{claude.5h}":        Missing,
		"{claude.5h.bar:4}":  BarMissing(4),
		"{claude.5h.dial}":   DialMissing,
		"{claude.5h.moon}":   DialMissing,
		"{claude.5h.resets}": Missing,
		"{claude.wk.pct}":    "59%",
	} {
		if got := Template(tmpl, s, now, plain); got != want {
			t.Errorf("%s = %q, want %q", tmpl, got, want)
		}
	}
}

// An old reading is dimmed and carries one ⚠ per window: after its
// percentage, or after each gauge when the template shows no percentage for
// the window (#374).
func TestTemplateMarksOldWindow(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	fresh := now.Add(-time.Minute).Format(time.RFC3339)
	old := now.Add(-3 * time.Hour).Format(time.RFC3339)
	used := 59.0
	s := testStatus()
	s.Tools[0].Limits[0].ObservedAt = &fresh
	s.Tools[0].Limits[1].ObservedAt = &old // weekly, on a fresh row
	s.Tools[0].Limits[1].UsedPct = &used   // 41% left
	for tmpl, want := range map[string]string{
		"{claude.wk.pct}":                   "41%⚠",
		"{claude.wk}":                       "41%⚠",
		"{claude.wk.bar:4} {claude.wk.pct}": "██░░ 41%⚠",
		"{claude.wk.bar:4} {claude.wk}":     "██░░ 41%⚠",
		"{claude.wk.dial}{claude.wk.moon}":  "◑⚠🌓⚠",
		"{claude.wk.bar:4}":                 "██░░⚠",
		"{claude.5h.pct} {claude.5h.dial}":  "76% ◕",
	} {
		if got := Template(tmpl, s, now, plain); got != want {
			t.Errorf("%s = %q, want %q", tmpl, got, want)
		}
	}
	color := Style{Color: true}
	if got, want := Template("{claude.wk.bar:4} {claude.wk.pct} {claude.5h.pct}", s, now, color),
		cDim+"██░░"+cReset+" "+cDim+"41%⚠"+cReset+" "+cGreen+"76%"+cReset; got != want {
		t.Errorf("color = %q, want %q (old window dimmed, fresh one colored)", got, want)
	}
}

// On a stale row every window is an old reading. A template that shows the
// row's {tool.stale} leaves its values as they were, so the marks don't
// repeat; one that doesn't marks the values itself. Another tool's
// {tool.stale} doesn't count (#374).
func TestTemplateStaleRowMarkedOnce(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	collected := now.Add(-2 * time.Hour).Format(time.RFC3339)
	s := testStatus()
	s.Tools[0].Stale = true
	s.Tools[0].CollectedAt = &collected
	for tmpl, want := range map[string]string{
		"{claude.stale}5h {claude.5h.pct}":                "⚠2h 5h 76%",
		"5h {claude.5h.pct} {claude.5h.dial}":             "5h 76%⚠ ◕",
		"{codex.stale}5h {claude.5h.pct}":                 "5h 76%⚠",
		"{claude.stale}{claude.5h.dial} {claude.wk.dial}": "⚠2h ◕ ◑",
	} {
		if got := Template(tmpl, s, now, plain); got != want {
			t.Errorf("%s = %q, want %q", tmpl, got, want)
		}
	}
	if got, want := Template("{claude.stale}{claude.5h.pct}", s, now, Style{Color: true}), "⚠2h "+cGreen+"76%"+cReset; got != want {
		t.Errorf("color = %q, want %q (values beside {claude.stale} unchanged)", got, want)
	}
}

// Every preset that shows Codex marks an old Codex reading — through
// {codex.stale} or on the values — and the default bar marks it once (#374).
func TestPresetsMarkOldCodex(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	collected := now.Add(-6 * time.Hour).Format(time.RFC3339)
	codex := limitsTool()
	codex.Tool = schema.ToolCodex
	codex.Stale = true
	codex.CollectedAt = &collected
	for i := range codex.Limits {
		codex.Limits[i].ObservedAt = &collected
	}
	s := schema.Status{Tools: []schema.Tool{limitsTool(), codex}}
	for _, p := range Presets {
		if !strings.Contains(p.Template, "{codex.") {
			continue
		}
		got := Template(p.Template, s, now, plain)
		part := got[strings.LastIndex(got, "codex"):]
		if !strings.Contains(part, "⚠") {
			t.Errorf("preset %s: codex part %q has no old-reading mark", p.Name, part)
		}
		if p.Name == "bar" && strings.Count(part, "⚠") != 1 {
			t.Errorf("preset bar: codex part %q marks the old reading more than once", part)
		}
		if strings.Contains(got[:strings.LastIndex(got, "codex")], "⚠") {
			t.Errorf("preset %s: fresh claude part of %q is marked", p.Name, got)
		}
	}
}
