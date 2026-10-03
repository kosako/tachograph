package swiftbar

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/menubar"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// barRow builds the expected per-tool metric row text (label-padded + bar)
// for the displayed percentage: headroom for limits, usage for context.
func barRow(label string, pct float64, suffix string) string {
	return fmt.Sprintf("%-*s %s %.0f%%%s", labelW, label, lineBar(pct, barWidth), pct, suffix)
}

func tool(name string, stale bool, pct5, pctW float64) schema.Tool {
	m5, mW := 300, 10080
	ctx := 24.0
	collected := "2026-06-13T10:00:00+09:00"
	r5 := "2026-06-13T14:00:00+09:00"
	display := "Fable 5"
	t := schema.Tool{
		Tool:        name,
		Available:   true,
		Stale:       stale,
		CollectedAt: &collected,
		Backend:     schema.BackendSubscription,
		Model:       &schema.Model{ID: "claude-fable-5", DisplayName: &display},
		Session:     &schema.Session{ContextUsedPct: &ctx},
		Limits: []schema.Limit{
			{Window: "5h", WindowMinutes: &m5, UsedPct: &pct5, ResetsAt: &r5},
			{Window: "weekly", WindowMinutes: &mW, UsedPct: &pctW},
		},
	}
	return t
}

func TestRenderStructure(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1") // assert the text title fallback
	now, _ := time.Parse(time.RFC3339, "2026-06-13T11:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{
		tool(schema.ToolClaudeCode, false, 24, 41),
		schema.Unavailable(schema.ToolCodex),
	}}
	out := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{})
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if lines[0] != "C🌔" { // 24% used → 76% left
		t.Errorf("title = %q, want C🌔 (codex unavailable is omitted)", lines[0])
	}
	if lines[1] != "---" {
		t.Errorf("line 2 = %q, want ---", lines[1])
	}
	// Reset time is rendered in the runner's local zone; compute the
	// expected "↻HH:MM" the same way to stay valid on any CI timezone.
	r5, _ := time.Parse(time.RFC3339, "2026-06-13T14:00:00+09:00")
	resets := r5.Local().Format("↻15:04")

	suffix := " " + enableParams + "\n"
	joined := out
	for _, want := range []string{
		"Claude — Fable 5 | color=" + inkLight,
		barRow("context", 24, "") + " | font=" + dataFont + " color=" + inkLight + suffix,
		barRow("5h", 76, " "+resets) + " | font=" + dataFont + " color=" + inkLight + suffix, // 24% used → 76% left
		barRow("weekly", 59, "") + " | font=" + dataFont + " color=" + inkLight + suffix,     // 41% used → 59% left
		"Codex — not found | color=" + colorGray,
		"Refresh | refresh=true",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderColorsOnlyAttention(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T11:00:00+09:00")
	// 5h at 85% used (red, shows 15% left), weekly at 60% used (yellow, shows
	// 40% left), ctx normal (uncolored): rows read as headroom, color by use.
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 85, 60)}}
	out := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.Contains(out, barRow("5h", 15, "")) || !strings.Contains(out, "| font="+dataFont+" color="+(Renderer{}).attnRed()) {
		t.Errorf("expected red 5h row:\n%s", out)
	}
	if !strings.Contains(out, barRow("weekly", 40, "")+" | font="+dataFont+" color="+(Renderer{}).attnYellow()) {
		t.Errorf("expected yellow weekly row:\n%s", out)
	}
	// Normal context row uses the ink color, not an attention color.
	if !strings.Contains(out, barRow("context", 24, "")+" | font="+dataFont+" color="+inkLight) {
		t.Errorf("normal context row should use ink color:\n%s", out)
	}
}

func TestRenderTitleImageByDefault(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "")
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	out := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.HasPrefix(out, "| image=") {
		t.Errorf("default title should be a gauge image, got:\n%s", strings.SplitN(out, "\n", 2)[0])
	}
}

// The meter style can't fill a ring for cost/tokens (no fraction), so it must
// fall back to the number/text title instead of drawing an empty gauge.
func TestRenderMeterCostFallsBackToNumber(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	cost := 1.5
	s.Tools[0].Daily = &schema.Daily{Tokens: 1000, CostUSD: &cost}
	cfg := config.Default() // meter style
	cfg.Menubar.Metric = render.MetricCost
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if strings.HasPrefix(title, "| image=") {
		t.Errorf("meter + cost should not render an empty gauge image; got %q", title)
	}
	if title != "C $1.50/d" {
		t.Errorf("meter + cost title = %q, want number fallback \"C $1.50/d\"", title)
	}
}

// Right after midnight (no usage today), the cost title must read today's
// $0.00/d — not a session cost carried over from earlier days, which used to
// show as if it were today's (#261).
func TestRenderCostZeroDayShowsTodaysZero(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolCodex, true, 7, 2)}}
	carried := 3.21
	s.Tools[0].Daily = &schema.Daily{Tokens: 0}
	s.Tools[0].Fallback = &schema.Fallback{EstimatedCostUSD: &carried}
	cfg := config.Default()
	cfg.Menubar.Style = config.StyleNumber
	cfg.Menubar.Metric = render.MetricCost
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if !strings.Contains(title, "$0.00/d") || strings.Contains(title, "$3.21") {
		t.Errorf("zero-day cost title = %q, want today's $0.00/d, not the carried-over $3.21", title)
	}
}

func TestRenderNumberStyle(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{
		tool(schema.ToolClaudeCode, false, 24, 41),
		tool(schema.ToolCodex, false, 7, 2),
	}}
	cfg := config.Default()
	cfg.Menubar.Style = config.StyleNumber
	cfg.Menubar.Metric = render.MetricLimit5h
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if title != "C 76%  X 93%" { // headroom: 24% / 7% used
		t.Errorf("number title = %q, want \"C 76%%  X 93%%\"", title)
	}
}

// limits.display=used flips the number title, the dropdown limit rows, and
// the ring to use; the settings menu offers both displays (#228).
func TestRenderUsedDisplay(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{
		tool(schema.ToolClaudeCode, false, 24, 41),
		tool(schema.ToolCodex, false, 7, 2),
	}}
	cfg := config.Default()
	cfg.Limits.Display = string(render.LimitUsed)
	cfg.Menubar.Style = config.StyleNumber
	out := (Renderer{}).Render(s, now, true, cfg, core.DailyHistory{})
	if title := strings.SplitN(out, "\n", 2)[0]; title != "C 24%  X 7%" {
		t.Errorf("number title (used) = %q, want \"C 24%%  X 7%%\"", title)
	}
	for _, want := range []string{
		barRow("5h", 24, " "+render.ResetShort("2026-06-13T14:00:00+09:00", now)),
		barRow("weekly", 41, ""),
		barRow("context", 24, ""), // unchanged: context is always usage
		"--リミット表示\n",
		"----    残量 | bash=",
		"----✓ 使用率 | bash=",
		"param3=\"limits.display\" param4=\"remaining\"",
		"param3=\"limits.display\" param4=\"used\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("used display output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, barRow("5h", 76, "")) {
		t.Errorf("used display still shows the 5h headroom row:\n%s", out)
	}

	cfg.Menubar.Style = config.StyleMeter
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	if title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]; title != "C🌒 X🌑" { // 24% / 7% used
		t.Errorf("moon title (used) = %q, want \"C🌒 X🌑\"", title)
	}
}

// weeklyOnlyTool mirrors Codex after OpenAI's 2026-07 5h-limit removal: the
// payload reports only a weekly window.
func weeklyOnlyTool(pctW float64) schema.Tool {
	c := tool(schema.ToolCodex, false, 0, pctW)
	c.Limits = c.Limits[1:] // drop the 5h window, keep weekly
	return c
}

// A tool without the configured limit window must fall back to its available
// window with a tag, not show "--" (issue #210).
func TestRenderNumberStyleLimitFallback(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{
		tool(schema.ToolClaudeCode, false, 24, 41),
		weeklyOnlyTool(15),
	}}
	cfg := config.Default()
	cfg.Menubar.Style = config.StyleNumber
	cfg.Menubar.Metric = render.MetricLimit5h
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if title != "C 76%  X wk85%" { // headroom: 24% / 15% used
		t.Errorf("number title = %q, want \"C 76%%  X wk85%%\"", title)
	}
}

// The moon-dial text title falls back the same way instead of showing the
// missing dial.
func TestRenderTextTitleMoonFallback(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{weeklyOnlyTool(60)}}
	title := strings.SplitN((Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{}), "\n", 2)[0]
	if title != "X"+render.Moon(40) { // 60% used → 40% left
		t.Errorf("text title = %q, want %q", title, "X"+render.Moon(40))
	}
}

// The moon-dial text title follows menubar.metric like the ring and the
// number style; it was pinned to the 5h window (#266).
func TestRenderTextTitleFollowsMetric(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 90, 10)}}
	cfg := config.Default()
	cfg.Menubar.Metric = render.MetricLimitWeekly
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if want := "C" + render.Moon(90); title != want { // weekly: 10% used → 90% left
		t.Errorf("weekly text title = %q, want %q (not the 5h %q)", title, want, "C"+render.Moon(10))
	}
}

func TestRenderSettingsMenu(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	cfg := config.Default()
	cfg.Tools = []string{schema.ToolClaudeCode} // codex disabled
	out := (Renderer{}).Render(s, now, true, cfg, core.DailyHistory{})

	for _, want := range []string{
		"Settings\n",
		"--表示形式\n",
		"--指標\n",
		"--表示するツール\n",
		// style: meter selected (✓), set directly
		"----✓ メーター | bash=",
		"param3=\"menubar.style\" param4=\"meter\"",
		// metric submenu lists all options
		"param3=\"menubar.metric\" param4=\"cost\"",
		"param3=\"menubar.metric\" param4=\"tokens\"",
		// limit display: headroom selected by default
		"--リミット表示\n",
		"----✓ 残量 | bash=",
		"param3=\"limits.display\" param4=\"used\"",
		// tools as checkboxes: Claude enabled, Codex disabled
		"----☑ Claude | bash=",
		"----☐ Codex | bash=",
		"param2=\"toggle-tool\" param3=\"codex\"",
		// sections: limits shown by default; the click hides them (#301)
		"--表示する項目\n",
		"----☑ リミット(5h / weekly) | bash=",
		"param3=\"menubar.limits\" param4=\"hide\"",
		"----☑ 直近 7 日 | bash=",
		"param3=\"menubar.history\" param4=\"hide\"",
		"refresh=true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("settings menu missing %q:\n%s", want, out)
		}
	}
}

// menubar.history=hide leaves the last-7-days section out even when rows
// are supplied, and unchecks its entry in the sections menu; the limit rows
// are unaffected (#302).
func TestRenderHistoryHidden(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now, _ := time.Parse(time.RFC3339, "2026-07-04T11:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	hist := core.DailyHistory{
		Days:  []string{"2026-07-03", "2026-07-04"},
		Tools: map[string][]*schema.Daily{schema.ToolClaudeCode: {{Tokens: 1_000_000, CostUSD: usd(1)}, {Tokens: 2_000_000, CostUSD: usd(2.5)}}},
	}
	cfg := config.Default()
	cfg.Menubar.History = config.VisibilityHide
	out := (Renderer{}).Render(s, now, true, cfg, hist)

	for _, absent := range []string{"日の cost/tokens", "07/03", "日計"} {
		if strings.Contains(out, absent) {
			t.Errorf("hidden-history output still has %q:\n%s", absent, out)
		}
	}
	for _, want := range []string{
		fmt.Sprintf("%-*s ", labelW, "5h"), // the limits toggle is separate
		"----☐ 直近 7 日 | bash=",
		"param3=\"menubar.history\" param4=\"show\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hidden-history output missing %q:\n%s", want, out)
		}
	}
}

// menubar.limits=hide leaves the 5h / weekly rows, the limit display and the
// limit metrics out of the dropdown, and a menu bar set to a limit window
// shows cost instead — on every title path (gauge image, moon-dial text,
// number) and for either window. Showing the limits again restores the
// stored window (#301).
func TestRenderLimitsHidden(t *testing.T) {
	now := time.Now()
	c := tool(schema.ToolClaudeCode, false, 24, 41)
	c.Daily = &schema.Daily{Tokens: 1_000_000, CostUSD: usd(1.5)}
	s := schema.Status{Tools: []schema.Tool{c}}

	for _, tc := range []struct{ name, metric, style, text string }{
		{"5h meter image", render.MetricLimit5h, config.StyleMeter, ""},
		{"5h meter text", render.MetricLimit5h, config.StyleMeter, "1"},
		{"5h number", render.MetricLimit5h, config.StyleNumber, ""},
		{"weekly meter image", render.MetricLimitWeekly, config.StyleMeter, ""},
		{"weekly meter text", render.MetricLimitWeekly, config.StyleMeter, "1"},
		{"weekly number", render.MetricLimitWeekly, config.StyleNumber, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TACHO_SWIFTBAR_TEXT", tc.text)
			cfg := config.Default()
			cfg.Menubar.Metric = tc.metric
			cfg.Menubar.Style = tc.style
			cfg.Menubar.Limits = config.VisibilityHide
			out := (Renderer{}).Render(s, now, true, cfg, core.DailyHistory{})

			if title := strings.SplitN(out, "\n", 2)[0]; title != "C $1.50/d" {
				t.Errorf("title = %q, want the cost in place of the hidden limit", title)
			}
			for _, want := range []string{
				fmt.Sprintf("%-*s ", labelW, "context"),
				"--表示する項目\n",
				"----☐ リミット(5h / weekly) | bash=",
				"param3=\"menubar.limits\" param4=\"show\"",
				"----✓ cost | bash=", // the check follows what the menu bar shows
			} {
				if !strings.Contains(out, want) {
					t.Errorf("hidden-limits output missing %q:\n%s", want, out)
				}
			}
			for _, absent := range []string{
				fmt.Sprintf("%-*s ", labelW, "5h"),
				fmt.Sprintf("%-*s ", labelW, "weekly"),
				"--リミット表示\n",
				"param3=\"menubar.metric\" param4=\"limit_5h\"",
				"param3=\"menubar.metric\" param4=\"limit_weekly\"",
			} {
				if strings.Contains(out, absent) {
					t.Errorf("hidden-limits output still has %q:\n%s", absent, out)
				}
			}
		})
	}

	// The same config with only the visibility flipped back reads as before:
	// the stored window was never rewritten.
	t.Setenv("TACHO_SWIFTBAR_TEXT", "")
	cfg := config.Default()
	cfg.Menubar.Style = config.StyleNumber
	cfg.Menubar.Metric = render.MetricLimitWeekly
	cfg.Menubar.Limits = config.VisibilityHide
	hidden := (Renderer{}).Render(s, now, true, cfg, core.DailyHistory{})
	cfg.Menubar.Limits = config.VisibilityShow
	shown := (Renderer{}).Render(s, now, true, cfg, core.DailyHistory{})

	if title := strings.SplitN(shown, "\n", 2)[0]; title != "C 59%" { // 41% used
		t.Errorf("title after showing the limits again = %q, want the weekly headroom back", title)
	}
	for _, want := range []string{
		"----✓ weekly limit | bash=",
		fmt.Sprintf("%-*s ", labelW, "weekly"),
		"--リミット表示\n",
		"----☑ リミット(5h / weekly) | bash=",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("output after showing the limits again missing %q:\n%s", want, shown)
		}
	}
	if strings.Contains(hidden, "----✓ weekly limit") {
		t.Errorf("hidden output still checks the stored weekly limit:\n%s", hidden)
	}
}

func TestRenderSanitizesHeaderText(t *testing.T) {
	now := time.Now()
	model := "Fable | bash=/tmp/pwn\nForged\x1b"
	plan := "pro | href=https://example.invalid\n--fake"
	tl := tool(schema.ToolCodex, false, 24, 41)
	tl.Model = &schema.Model{ID: "gpt-safe", DisplayName: &model}
	tl.Plan = &plan

	out := (Renderer{}).Render(schema.Status{Tools: []schema.Tool{tl}}, now, true, config.Default(), core.DailyHistory{})
	for _, bad := range []string{
		"Fable | bash=/tmp/pwn",
		"\nForged",
		"pro | href=https://example.invalid",
		"\n--fake",
		"\x1b",
	} {
		if strings.Contains(out, bad) {
			t.Fatalf("SwiftBar output contains unsanitized header text %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "Codex — Fable  bash=/tmp/pwnForged (pro  href=https://example.invalid--fake) | color=") {
		t.Fatalf("sanitized header missing:\n%s", out)
	}
}

func TestRenderToolFilter(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{
		tool(schema.ToolClaudeCode, false, 24, 41),
		tool(schema.ToolCodex, false, 7, 2),
	}}
	cfg := config.Default()
	cfg.Tools = []string{schema.ToolCodex} // only codex
	cfg.Menubar.Style = config.StyleNumber
	out := (Renderer{}).Render(s, now, true, cfg, core.DailyHistory{})
	if strings.Contains(out, "Claude —") {
		t.Errorf("Claude should be filtered out:\n%s", out)
	}
	if !strings.Contains(out, "Codex —") {
		t.Errorf("Codex should be shown:\n%s", out)
	}
}

func TestRenderTitleBothTools(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{
		tool(schema.ToolClaudeCode, false, 24, 41),
		tool(schema.ToolCodex, false, 90, 10),
	}}
	out := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.HasPrefix(out, "C🌔 X🌑\n") { // 24% used → 76% left; 90% used → 10% left
		t.Errorf("title = %q, want C🌔 X🌑", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestRenderStaleGray(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00") // 2h after collected
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolCodex, true, 70, 10)}}
	out := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.Contains(out, "⚠2h") {
		t.Errorf("stale age missing:\n%s", out)
	}
	if strings.Contains(out, (Renderer{}).attnYellow()) || !strings.Contains(out, colorGray) {
		t.Errorf("stale lines should be gray, not pressure-colored:\n%s", out)
	}
}

func TestRenderFallback(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	tokens := int64(3962991)
	cost := 1.5
	tl := schema.Tool{
		Tool: schema.ToolCodex, Available: true, Backend: schema.BackendBedrock,
		Fallback: &schema.Fallback{SessionTokens: &tokens, EstimatedCostUSD: &cost},
	}
	out := (Renderer{}).Render(schema.Status{Tools: []schema.Tool{tl}}, time.Now(), true, config.Default(), core.DailyHistory{})
	costRow := fmt.Sprintf("%-*s $1.50 | font=%s color=%s %s\n", labelW, "cost", dataFont, inkLight, enableParams)
	tokRow := fmt.Sprintf("%-*s 4M | font=%s color=%s %s\n", labelW, "tokens", dataFont, inkLight, enableParams)
	missRow := fmt.Sprintf("%-*s -- | font=%s color=%s %s\n", labelW, "5h", dataFont, inkLight, enableParams)
	if !strings.Contains(out, costRow) || !strings.Contains(out, tokRow) {
		t.Errorf("cost/tokens rows missing:\n%s", out)
	}
	if !strings.Contains(out, missRow) {
		t.Errorf("absent limit should show --:\n%s", out)
	}
	if !strings.HasPrefix(out, "X◌\n") {
		t.Errorf("title = %q, want X◌ for tool without limits", strings.SplitN(out, "\n", 2)[0])
	}
}

func usd(v float64) *float64 { return &v }

// The history section lists one row per day with each shown tool's
// cost/tokens in config order; unknown days read "--", and an empty history
// adds no section.
func TestRenderHistory(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now, _ := time.Parse(time.RFC3339, "2026-07-04T11:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41), tool(schema.ToolCodex, false, 7, 2)}}
	hist := core.DailyHistory{
		Days: []string{"2026-07-02", "2026-07-03", "2026-07-04"},
		Tools: map[string][]*schema.Daily{
			schema.ToolClaudeCode: {{Tokens: 12_300_000, CostUSD: usd(3.21)}, {}, {Tokens: 59_000_000, CostUSD: usd(12.3)}},
			schema.ToolCodex:      {nil, {Tokens: 900_000}, {Tokens: 20_400_000, CostUSD: usd(4.1)}},
		},
	}

	out := (Renderer{}).Render(s, now, true, config.Default(), hist)
	for _, want := range []string{
		"直近 3 日の cost/tokens(青 = cost 上位 3 日) | color=" + colorGray,
		"07/02  C " + ansiBlue + "  $3.21/12.3M " + ansiReset + "  X --             | font=" + dataFont + " color=" + (Renderer{}).ink() + " ansi=true ",
		"07/03  C   $0.00/0       X      --/900k   | font=" + dataFont,
		"07/04  C " + ansiBlue + " $12.30/59M   " + ansiReset + "  X " + ansiBlue + "  $4.10/20.4M " + ansiReset + " | font=" + dataFont,
		// Claude's no-usage day adds $0; Codex's unknown day makes its sum,
		// and so the overall one, unknown (#298).
		"3日計  C $15.51  X --  計 -- | font=" + dataFont + " color=" + (Renderer{}).ink() + " " + enableParams + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("history row %q missing in:\n%s", want, out)
		}
	}

	// Only the configured tools get a column, in config order; with one tool
	// the total row has no overall sum.
	cfg := config.Default()
	cfg.Tools = []string{schema.ToolCodex}
	only := (Renderer{}).Render(s, now, true, cfg, hist)
	if !strings.Contains(only, "07/04  X "+ansiBlue+"  $4.10/20.4M "+ansiReset+" | font=") || strings.Contains(only, "07/04  C") {
		t.Errorf("codex-only history rows wrong:\n%s", only)
	}
	if !strings.Contains(only, "3日計  X -- | font=") || strings.Contains(only, "  計 ") {
		t.Errorf("codex-only total row wrong:\n%s", only)
	}

	if none := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{}); strings.Contains(none, "日の cost/tokens") || strings.Contains(none, "日計") {
		t.Errorf("empty history should add no section:\n%s", none)
	}
}

// The total row sums each tool's known costs and, across tools, all of
// them; a day with tokens but no price makes that tool's sum and the overall
// one unknown instead of being left out (#298).
func TestRenderHistoryTotal(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now, _ := time.Parse(time.RFC3339, "2026-07-04T11:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41), tool(schema.ToolCodex, false, 7, 2)}}
	days := []string{"2026-07-03", "2026-07-04"}

	known := core.DailyHistory{Days: days, Tools: map[string][]*schema.Daily{
		schema.ToolClaudeCode: {{Tokens: 1_000_000, CostUSD: usd(1)}, {Tokens: 2_000_000, CostUSD: usd(2.5)}},
		schema.ToolCodex:      {{}, {Tokens: 300_000, CostUSD: usd(0.25)}},
	}}
	if out := (Renderer{}).Render(s, now, true, config.Default(), known); !strings.Contains(out, "2日計  C $3.50  X $0.25  計 $3.75 | font=") {
		t.Errorf("known total row missing in:\n%s", out)
	}

	unpriced := core.DailyHistory{Days: days, Tools: map[string][]*schema.Daily{
		schema.ToolClaudeCode: {{Tokens: 1_000_000, CostUSD: usd(1)}, {Tokens: 2_000_000, CostUSD: usd(2.5)}},
		schema.ToolCodex:      {{Tokens: 5_000}, {Tokens: 300_000, CostUSD: usd(0.25)}},
	}}
	if out := (Renderer{}).Render(s, now, true, config.Default(), unpriced); !strings.Contains(out, "2日計  C $3.50  X --  計 -- | font=") {
		t.Errorf("unpriced total row missing in:\n%s", out)
	}

	// Days with no usage at all total an exact $0.00, not unknown.
	zero := core.DailyHistory{Days: days, Tools: map[string][]*schema.Daily{
		schema.ToolClaudeCode: {{}, {}},
		schema.ToolCodex:      {{}, {}},
	}}
	if out := (Renderer{}).Render(s, now, true, config.Default(), zero); !strings.Contains(out, "2日計  C $0.00  X $0.00  計 $0.00 | font=") {
		t.Errorf("all-zero total row missing in:\n%s", out)
	}

	// Both tools configured but only one has a column: no overall sum.
	claudeOnly := core.DailyHistory{Days: days, Tools: map[string][]*schema.Daily{
		schema.ToolClaudeCode: {{Tokens: 1_000_000, CostUSD: usd(1)}, {Tokens: 2_000_000, CostUSD: usd(2.5)}},
	}}
	if out := (Renderer{}).Render(s, now, true, config.Default(), claudeOnly); !strings.Contains(out, "2日計  C $3.50 | font=") || strings.Contains(out, "  計 ") {
		t.Errorf("single-column total row wrong:\n%s", out)
	}

	// No shown tool has a column: the day rows stay, the total row doesn't.
	noColumn := core.DailyHistory{Days: days, Tools: map[string][]*schema.Daily{}}
	if out := (Renderer{}).Render(s, now, true, config.Default(), noColumn); !strings.Contains(out, "07/04 | font=") || strings.Contains(out, "日計") {
		t.Errorf("total row should be absent without columns:\n%s", out)
	}
}

// Only known, non-zero costs compete for the highlight; at most historyTop
// days per tool are picked, ties keeping the earlier day.
func TestTopCostDays(t *testing.T) {
	col := []*schema.Daily{
		{Tokens: 1, CostUSD: usd(5)},
		nil,         // unknown
		{},          // no usage
		{Tokens: 1}, // cost unknown
		{Tokens: 1, CostUSD: usd(9)},
		{Tokens: 1, CostUSD: usd(9)}, // tie: the earlier day (index 4) ranks first
		{Tokens: 1, CostUSD: usd(7)},
		{Tokens: 1, CostUSD: usd(1)},
	}
	got := topCostDays(col, 3)
	if len(got) != 3 || !got[4] || !got[5] || !got[6] {
		t.Errorf("topCostDays = %v, want {4 5 6}", got)
	}
	if got := topCostDays(col[:2], 3); len(got) != 1 || !got[0] {
		t.Errorf("topCostDays(short) = %v, want {0}", got)
	}
	if got := topCostDays(nil, 3); len(got) != 0 {
		t.Errorf("topCostDays(nil) = %v, want empty", got)
	}
}

func TestRendererMenuDarkInk(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	light := (Renderer{}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.Contains(light, "Claude — Fable 5 | color="+inkLight) {
		t.Errorf("light menu should use the light ink:\n%s", light)
	}
	dark := (Renderer{MenuDark: true}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.Contains(dark, "Claude — Fable 5 | color="+inkDark) {
		t.Errorf("dark menu should use the dark ink:\n%s", dark)
	}
}

func TestRendererBinPathClickTarget(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	out := (Renderer{BinPath: "/opt/tacho/bin/tacho"}).Render(s, now, true, config.Default(), core.DailyHistory{})
	if !strings.Contains(out, `| bash="/opt/tacho/bin/tacho" terminal=false refresh=true param1="config"`) {
		t.Errorf("settings should click the renderer's BinPath:\n%s", out)
	}
}

// observedAgo dates every window of tl as observed ago before now.
func observedAgo(tl schema.Tool, now time.Time, ago time.Duration) schema.Tool {
	at := now.Add(-ago).Format(time.RFC3339)
	for i := range tl.Limits {
		tl.Limits[i].ObservedAt = &at
	}
	return tl
}

// A menu bar value whose window was observed longer ago than its tool's
// stale threshold (Claude 60 min, Codex 5 h) is marked in the number style,
// as is one from a stale row; a fresh one is not, and neither is cost, which
// is recomputed from the logs on every run (#331).
func TestRenderNumberStyleMarksOldReading(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00")
	cases := []struct {
		name   string
		tool   schema.Tool
		metric string
		want   string
	}{
		{name: "claude observed 2h ago", tool: observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 2*time.Hour), metric: render.MetricLimitWeekly, want: "C 8%" + staleMark},
		{name: "claude observed 10m ago", tool: observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 10*time.Minute), metric: render.MetricLimitWeekly, want: "C 8%"},
		{name: "codex observed 2h ago", tool: observedAgo(tool(schema.ToolCodex, false, 24, 92), now, 2*time.Hour), metric: render.MetricLimitWeekly, want: "X 8%"},
		{name: "stale row without an observation time", tool: tool(schema.ToolClaudeCode, true, 24, 92), metric: render.MetricLimitWeekly, want: "C 8%" + staleMark},
		{name: "cost on a stale row", tool: observedAgo(tool(schema.ToolClaudeCode, true, 24, 92), now, 2*time.Hour), metric: render.MetricCost, want: "C " + render.Missing},
	}
	for _, c := range cases {
		cfg := config.Default()
		cfg.Menubar.Style = config.StyleNumber
		cfg.Menubar.Metric = c.metric
		s := schema.Status{Tools: []schema.Tool{c.tool}}
		if got := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]; got != c.want {
			t.Errorf("%s: number title = %q, want %q", c.name, got, c.want)
		}
	}
}

// The moon-dial text title marks an old reading the same way (#331).
func TestRenderTextTitleMarksOldReading(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 2*time.Hour)}}
	cfg := config.Default()
	cfg.Menubar.Metric = render.MetricLimitWeekly
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if want := "C" + render.Moon(8) + staleMark; title != want {
		t.Errorf("text title = %q, want %q", title, want)
	}
}

// In the dropdown a window observed longer ago than the tool's stale
// threshold is grayed and carries its age, even when the row itself is fresh;
// a fresh window keeps its pressure color and no mark (#331).
func TestRenderLimitRowMarksOldObservation(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00")
	old := schema.Status{Tools: []schema.Tool{observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 3*time.Hour)}}
	out := (Renderer{}).Render(old, now, true, config.Default(), core.DailyHistory{})
	if want := barRow("weekly", 8, " "+staleMark+"3h") + " | font=" + dataFont + " color=" + colorGray; !strings.Contains(out, want) {
		t.Errorf("old weekly row missing %q:\n%s", want, out)
	}
	fresh := schema.Status{Tools: []schema.Tool{observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 10*time.Minute)}}
	out = (Renderer{}).Render(fresh, now, true, config.Default(), core.DailyHistory{})
	if want := barRow("weekly", 8, "") + " | font=" + dataFont + " color=" + (Renderer{}).attnRed(); !strings.Contains(out, want) {
		t.Errorf("fresh weekly row missing %q:\n%s", want, out)
	}
}

// The ring grays a tool whose shown limit is an old reading by marking it
// stale for the gauge only; the status passed in is left as is (#331).
func TestGaugeStatusMarksOldReading(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{
		observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 2*time.Hour),
		observedAgo(tool(schema.ToolCodex, false, 24, 92), now, 2*time.Hour),
	}}
	g := gaugeStatus(s, now, render.MetricLimitWeekly)
	if !g.Tools[0].Stale || g.Tools[1].Stale {
		t.Errorf("gauge stale = %v / %v, want the Claude reading (2h > 60 min) only", g.Tools[0].Stale, g.Tools[1].Stale)
	}
	if s.Tools[0].Stale {
		t.Error("gaugeStatus changed the status passed in")
	}
}

// `tacho swiftbar --png` previews the menu bar's ring through GaugePNG, so
// the two show the same image for the same input — an old reading grayed in
// both, not only in the menu bar (#331).
func TestGaugePNGMatchesTitleImage(t *testing.T) {
	t.Setenv("TACHO_SWIFTBAR_TEXT", "") // the meter style draws the image, not the moon text
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00")
	s := schema.Status{Tools: []schema.Tool{observedAgo(tool(schema.ToolClaudeCode, false, 24, 92), now, 2*time.Hour)}}
	cfg := config.Default()
	cfg.Menubar.Metric = render.MetricLimitWeekly
	limits := render.LimitDisplay(cfg.Limits.Display)
	b64, ok := GaugePNG(s, now, true, cfg.Menubar.Metric, limits)
	if !ok {
		t.Fatal("GaugePNG rendered nothing")
	}
	title := strings.SplitN((Renderer{}).Render(s, now, true, cfg, core.DailyHistory{}), "\n", 2)[0]
	if title != "| image="+b64 {
		t.Error("the menu bar ring and GaugePNG differ for the same input")
	}
	if raw, _ := menubar.PNGBase64(s, true, cfg.Menubar.Metric, limits); raw == b64 {
		t.Error("GaugePNG drew the old reading like a fresh one (ring not grayed)")
	}
}
