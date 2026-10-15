package daily

import (
	"math"
	"testing"

	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/pricing"
)

// A transcript line that usage reports as fast costs twice the standard
// rate, cache reads and both cache-write TTLs included; a standard line, or
// one from before usage recorded its speed, costs the standard rate. Tokens
// don't change with the speed (#407).
func TestClaudeLineTotalsFastMode(t *testing.T) {
	prices := pricing.Table{"claude-opus-5-5": {In: 4, Out: 20, CacheRead: 0.2, CacheWrite: 5}}
	totals := func(speed string) Totals {
		t.Helper()
		sp := ""
		if speed != "" {
			sp = `,"speed":"` + speed + `"`
		}
		line := `{"type":"assistant","timestamp":"2026-10-15T12:00:00Z","message":{"id":"m","model":"claude-opus-5-5","role":"assistant","usage":{"input_tokens":1000,"cache_creation_input_tokens":3000,"cache_read_input_tokens":4000,"output_tokens":500,"cache_creation":{"ephemeral_5m_input_tokens":1000,"ephemeral_1h_input_tokens":2000}` + sp + `}}}`
		var got []Totals
		claude.EachUsageLine([]byte(line), func(l claude.TranscriptLine) { got = append(got, claudeLineTotals(l, prices)) })
		if len(got) != 1 {
			t.Fatalf("speed %q: %d usage lines, want 1", speed, len(got))
		}
		return got[0]
	}
	// input 1000×4 + 5m write 1000×5 + 1h write 2000×(4×2) + read 4000×0.2 +
	// output 500×20 = 35,800 per million at the standard rate.
	const standard = 35_800.0 / 1_000_000
	for _, c := range []struct {
		speed string
		want  float64
	}{
		{"standard", standard},
		{"", standard},
		{"fast", 2 * standard},
	} {
		got := totals(c.speed)
		if math.Abs(got.Cost-c.want) > 1e-12 {
			t.Errorf("speed %q: cost = %v, want %v", c.speed, got.Cost, c.want)
		}
		if got.Tokens != 8_500 || got.Input != 8_000 || got.CachedInput != 4_000 || got.Output != 500 {
			t.Errorf("speed %q: tokens = %+v, want 8500 (input 8000, cached 4000, output 500)", c.speed, got)
		}
	}
}
