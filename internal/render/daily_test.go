package render

import (
	"strings"
	"testing"

	"github.com/kosako/tachograph/internal/schema"
)

func usd(v float64) *float64 { return &v }

func TestDailyTable(t *testing.T) {
	days := []string{"2026-07-02", "2026-07-03", "2026-07-04"}
	cols := map[string][]*schema.Daily{
		schema.ToolClaudeCode: {
			{Tokens: 12_300_000, CostUSD: usd(3.21)},
			{}, // no usage: an exact zero
			{Tokens: 59_000_000, CostUSD: usd(12.3)},
		},
		schema.ToolCodex: {
			{Tokens: 2_100_000, CostUSD: usd(0.55)},
			{Tokens: 900_000}, // tokens seen, no priced model: cost unknown
			{Tokens: 20_400_000, CostUSD: usd(4.1)},
		},
	}
	got := DailyTable(days, []string{schema.ToolClaudeCode, schema.ToolCodex}, cols)
	want := strings.Join([]string{
		"day         claude $  claude tokens  codex $  codex tokens  total $",
		"2026-07-02     $3.21          12.3M    $0.55          2.1M    $3.76",
		"2026-07-03     $0.00              0       --          900k       --",
		"2026-07-04    $12.30            59M    $4.10         20.4M   $16.40",
		"-------------------------------------------------------------------",
		"total         $15.51          71.3M       --         23.4M       --",
		"",
		DailyNote,
		"",
	}, "\n")
	if got != want {
		t.Errorf("DailyTable =\n%s\nwant\n%s", got, want)
	}
}

// A tool whose logs could not be read is unknown on every day and in the
// totals; with a single tool shown there is no total-cost column.
func TestDailyTableUnknownColumnAndSingleTool(t *testing.T) {
	days := []string{"2026-07-03", "2026-07-04"}
	cols := map[string][]*schema.Daily{
		schema.ToolClaudeCode: {{Tokens: 1000, CostUSD: usd(0.01)}, {Tokens: 2000, CostUSD: usd(0.02)}},
		schema.ToolCodex:      {nil, nil},
	}
	got := DailyTable(days, []string{schema.ToolClaudeCode, schema.ToolCodex}, cols)
	for _, line := range []string{
		"2026-07-03     $0.01             1k       --            --       --",
		"total          $0.03             3k       --            --       --",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("DailyTable missing %q:\n%s", line, got)
		}
	}

	single := DailyTable(days, []string{schema.ToolCodex}, cols)
	if strings.Contains(single, "total $") || !strings.Contains(single, "codex $  codex tokens\n") {
		t.Errorf("single-tool DailyTable should have no total-cost column:\n%s", single)
	}

	// Tools without a column are skipped rather than rendered as unknown.
	none := DailyTable(days, []string{"other", schema.ToolClaudeCode}, cols)
	if strings.Contains(none, "other") {
		t.Errorf("DailyTable rendered a tool without a column:\n%s", none)
	}
}
