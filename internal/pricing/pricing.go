// Package pricing holds approximate model prices used to estimate daily cost.
// Prices are never exact and change often, so the built-in table is a rough
// default that users override via ~/.config/tachograph/pricing.json.
package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/kosako/tachograph/internal/config"
)

// Rate is the price per one million tokens, in USD.
type Rate struct {
	In         float64 `json:"input"`
	Out        float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// defaults are approximate public prices (USD per million tokens). They are
// matched by model-id prefix. Override or extend via the pricing.json file.
var defaults = map[string]Rate{
	"claude-opus":   {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
	"claude-sonnet": {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-haiku":  {In: 0.8, Out: 4, CacheRead: 0.08, CacheWrite: 1},
	"claude-fable":  {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}, // approx (Opus tier)
	"claude-mythos": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}, // approx
	"gpt-5":         {In: 1.25, Out: 10, CacheRead: 0.125, CacheWrite: 1.25},
	"codex":         {In: 1.25, Out: 10, CacheRead: 0.125, CacheWrite: 1.25},
}

// Table is the merged price table (overrides applied over defaults).
type Table map[string]Rate

// Load returns the built-in defaults merged with the user's pricing.json,
// if present. Unparseable files are ignored (defaults stand).
func Load() Table {
	t := Table{}
	for k, v := range defaults {
		t[k] = v
	}
	dir := config.Dir()
	if dir == "" {
		return t
	}
	b, err := os.ReadFile(filepath.Join(dir, "pricing.json"))
	if err != nil {
		return t
	}
	// Merge field-by-field over the existing rate so a partial override
	// (e.g. {"claude-opus":{"input":20}}) tweaks one price without zeroing the
	// others. Pointer fields distinguish an explicit 0 from an omitted field.
	var over map[string]rateOverride
	if json.Unmarshal(b, &over) == nil {
		for k, o := range over {
			r := t[k] // existing default, or zero Rate for a brand-new model id
			if o.In != nil {
				r.In = *o.In
			}
			if o.Out != nil {
				r.Out = *o.Out
			}
			if o.CacheRead != nil {
				r.CacheRead = *o.CacheRead
			}
			if o.CacheWrite != nil {
				r.CacheWrite = *o.CacheWrite
			}
			t[k] = r
		}
	}
	return t
}

// rateOverride mirrors Rate with pointer fields so an omitted price keeps the
// built-in default instead of becoming 0.
type rateOverride struct {
	In         *float64 `json:"input"`
	Out        *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// For returns the rate for a model id by longest-prefix match, and whether a
// price was found.
func (t Table) For(model string) (Rate, bool) {
	best := ""
	for key := range t {
		if strings.HasPrefix(model, key) && len(key) > len(best) {
			best = key
		}
	}
	if best == "" {
		return Rate{}, false
	}
	return t[best], true
}

// Cost returns the USD cost of a token breakdown at the given rate.
func (r Rate) Cost(in, cacheWrite, cacheRead, out int64) float64 {
	return (float64(in)*r.In +
		float64(cacheWrite)*r.CacheWrite +
		float64(cacheRead)*r.CacheRead +
		float64(out)*r.Out) / 1_000_000
}
