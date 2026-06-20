package swiftbar

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// row builds the expected per-tool metric row text (label-padded + bar).
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
	out := Render(s, now, true, config.Default())
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if lines[0] != "C🌒" {
		t.Errorf("title = %q, want C🌒 (codex unavailable is omitted)", lines[0])
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
		barRow("5h", 24, " "+resets) + " | font=" + dataFont + " color=" + inkLight + suffix,
		barRow("weekly", 41, "") + " | font=" + dataFont + " color=" + inkLight + suffix,
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
	// 5h at 85% (red), weekly at 60% (yellow), ctx normal (uncolored).
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 85, 60)}}
	out := Render(s, now, true, config.Default())
	if !strings.Contains(out, barRow("5h", 85, "")) || !strings.Contains(out, "| font="+dataFont+" color="+attnRed()) {
		t.Errorf("expected red 5h row:\n%s", out)
	}
	if !strings.Contains(out, barRow("weekly", 60, "")+" | font="+dataFont+" color="+attnYellow()) {
		t.Errorf("expected yellow weekly row:\n%s", out)
	}
	// Normal context row uses the ink color, not an attention color.
	if !strings.Contains(out, barRow("context", 24, "")+" | font="+dataFont+" color="+inkLight) {
		t.Errorf("normal context row should use ink color:\n%s", out)
	}
}

func TestRenderTitleImageByDefault(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	out := Render(s, now, true, config.Default())
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
	title := strings.SplitN(Render(s, now, true, cfg), "\n", 2)[0]
	if strings.HasPrefix(title, "| image=") {
		t.Errorf("meter + cost should not render an empty gauge image; got %q", title)
	}
	if title != "C $1.50/d" {
		t.Errorf("meter + cost title = %q, want number fallback \"C $1.50/d\"", title)
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
	title := strings.SplitN(Render(s, now, true, cfg), "\n", 2)[0]
	if title != "C 24%  X 7%" {
		t.Errorf("number title = %q, want \"C 24%%  X 7%%\"", title)
	}
}

func TestRenderSettingsMenu(t *testing.T) {
	now := time.Now()
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolClaudeCode, false, 24, 41)}}
	cfg := config.Default()
	cfg.Tools = []string{schema.ToolClaudeCode} // codex disabled
	out := Render(s, now, true, cfg)

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
		// tools as checkboxes: Claude enabled, Codex disabled
		"----☑ Claude | bash=",
		"----☐ Codex | bash=",
		"param2=\"toggle-tool\" param3=\"codex\"",
		"refresh=true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("settings menu missing %q:\n%s", want, out)
		}
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
	out := Render(s, now, true, cfg)
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
	out := Render(s, now, true, config.Default())
	if !strings.HasPrefix(out, "C🌒 X🌕\n") {
		t.Errorf("title = %q, want C🌒 X🌕", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestRenderStaleGray(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-13T12:00:00+09:00") // 2h after collected
	s := schema.Status{Tools: []schema.Tool{tool(schema.ToolCodex, true, 70, 10)}}
	out := Render(s, now, true, config.Default())
	if !strings.Contains(out, "⚠2h") {
		t.Errorf("stale age missing:\n%s", out)
	}
	if strings.Contains(out, attnYellow()) || !strings.Contains(out, colorGray) {
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
	out := Render(schema.Status{Tools: []schema.Tool{tl}}, time.Now(), true, config.Default())
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
