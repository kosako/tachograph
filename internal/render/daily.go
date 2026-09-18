package render

import (
	"fmt"
	"strings"

	"github.com/kosako/tachograph/internal/schema"
)

// DailyNote is the caveat printed under the daily table: the figures are
// recomputed from whatever logs are still on disk, so days past a tool's
// retention read smaller than they were.
const DailyNote = "note: recomputed from the logs still on disk; days past Claude Code's transcript retention are under-reported."

// DailyTable renders per-day cost and tokens as a plain-text table: one row
// per day (oldest first), a cost and a tokens column per tool in the given
// order, a total-cost column when more than one tool is shown, and a total
// row for the whole window. cols maps a tool name to one entry per day
// (aligned with days); a nil entry renders as unknown ("--"). A tool without
// a column is skipped.
func DailyTable(days []string, tools []string, cols map[string][]*schema.Daily) string {
	var shown []string
	for _, tool := range tools {
		if _, ok := cols[tool]; ok {
			shown = append(shown, tool)
		}
	}
	withTotal := len(shown) > 1

	header := []string{"day"}
	for _, tool := range shown {
		name := toolShort(tool)
		header = append(header, name+" $", name+" tokens")
	}
	if withTotal {
		header = append(header, "total $")
	}

	rows := [][]string{header}
	for i, day := range days {
		row := []string{day}
		for _, tool := range shown {
			row = append(row, dailyCells(cols[tool][i])...)
		}
		if withTotal {
			var cells []*schema.Daily
			for _, tool := range shown {
				cells = append(cells, cols[tool][i])
			}
			row = append(row, dailyCostSum(cells))
		}
		rows = append(rows, row)
	}

	total := []string{"total"}
	var allDays []*schema.Daily
	for _, tool := range shown {
		col := cols[tool]
		total = append(total, dailyCells(dailySum(col))...)
		allDays = append(allDays, col...)
	}
	if withTotal {
		total = append(total, dailyCostSum(allDays))
	}
	rows = append(rows, total)

	widths := make([]int, len(header))
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	var b strings.Builder
	for i, row := range rows {
		if i == len(rows)-1 {
			b.WriteString(strings.Repeat("-", tableWidth(widths)))
			b.WriteByte('\n')
		}
		for j, cell := range row {
			if j > 0 {
				b.WriteString("  ")
			}
			if j == 0 {
				fmt.Fprintf(&b, "%-*s", widths[j], cell)
			} else {
				fmt.Fprintf(&b, "%*s", widths[j], cell)
			}
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(DailyNote)
	b.WriteByte('\n')
	return b.String()
}

func tableWidth(widths []int) int {
	w := 2 * (len(widths) - 1)
	for _, c := range widths {
		w += c
	}
	return w
}

func toolShort(tool string) string {
	if tool == schema.ToolClaudeCode {
		return "claude"
	}
	return tool
}

// dailyCells formats one day's cost and tokens. A nil day is unknown. Cost
// follows the daily contract — null means no priced model was seen — except
// that a day with no tokens at all is an exact $0.00, not unknown.
func dailyCells(d *schema.Daily) []string {
	if d == nil {
		return []string{"--", "--"}
	}
	return []string{dailyCost(d), FormatTokens(d.Tokens)}
}

func dailyCost(d *schema.Daily) string {
	switch {
	case d.CostUSD != nil:
		return fmt.Sprintf("$%.2f", *d.CostUSD)
	case d.Tokens == 0:
		return "$0.00"
	default:
		return "--"
	}
}

// dailySum adds days into one Daily (tokens and cost only — what the table
// shows) for the total cells. The sum is unknown (nil) when any day is; its
// cost is unknown when any day with tokens has an unknown cost, since the
// total would otherwise silently omit that day.
func dailySum(days []*schema.Daily) *schema.Daily {
	var sum schema.Daily
	var cost float64
	costKnown := true
	for _, d := range days {
		if d == nil {
			return nil
		}
		sum.Tokens += d.Tokens
		switch {
		case d.CostUSD != nil:
			cost += *d.CostUSD
		case d.Tokens != 0:
			costKnown = false
		}
	}
	if costKnown && cost > 0 {
		sum.CostUSD = &cost
	}
	return &sum
}

// dailyCostSum is the total-cost cell across tools (or across the window),
// with the same unknown rules as dailySum.
func dailyCostSum(days []*schema.Daily) string {
	sum := dailySum(days)
	if sum == nil {
		return "--"
	}
	return dailyCost(sum)
}
