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

// defaults are approximate first-party API prices (USD per million tokens),
// matched by model-id prefix. Cache rates follow each provider's convention:
// Anthropic cache read = 0.1x input, write (5-min ephemeral) = 1.25x input;
// OpenAI uses its published cached-input price (and bills cache writes at the
// input rate). Long-context premiums (Opus >200K, GPT-5.5 >272K) are not modeled
// — this is a flat table. Override or extend via the pricing.json file.
var defaults = map[string]Rate{
	"claude-opus":     {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},   // Opus 4.5+
	"claude-opus-4-1": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}, // Opus 4.1 kept the older $15/$75
	"claude-sonnet":   {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},   // Sonnet 4.6
	"claude-haiku":    {In: 1, Out: 5, CacheRead: 0.1, CacheWrite: 1.25},    // Haiku 4.5
	"claude-fable":    {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5},    // Fable 5
	"claude-mythos":   {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5},    // Mythos 5
	// gpt-5.4 / gpt-5.5 and their variants are priced separately from the
	// original gpt-5; the more specific keys win by longest-prefix match.
	// -codex variants aren't separately priced, so they fall to the base.
	"gpt-5.5":      {In: 5, Out: 30, CacheRead: 0.5, CacheWrite: 5},
	"gpt-5.5-pro":  {In: 30, Out: 180, CacheRead: 3, CacheWrite: 30}, // cached input unpublished; 0.1x convention
	"gpt-5.4":      {In: 2.5, Out: 15, CacheRead: 0.25, CacheWrite: 2.5},
	"gpt-5.4-mini": {In: 0.75, Out: 4.5, CacheRead: 0.075, CacheWrite: 0.75},
	"gpt-5.4-nano": {In: 0.2, Out: 1.25, CacheRead: 0.02, CacheWrite: 0.2},
	"gpt-5.4-pro":  {In: 30, Out: 180, CacheRead: 3, CacheWrite: 30},        // cached input unpublished; 0.1x convention
	"gpt-5":        {In: 1.25, Out: 10, CacheRead: 0.125, CacheWrite: 1.25}, // original gpt-5 / base
	"codex":        {In: 1.25, Out: 10, CacheRead: 0.125, CacheWrite: 1.25}, // fallback for "codex*" ids
	// Bare aliases some tools record instead of the full model id (e.g. a
	// transcript that logs just "sonnet"). Same rate as the matching claude-* tier.
	"opus":   {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"sonnet": {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"haiku":  {In: 1, Out: 5, CacheRead: 0.1, CacheWrite: 1.25},
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

// bedrockProviders are the provider prefixes Amazon Bedrock prepends to a model
// id (optionally after a cross-region prefix like "us."). Stripping them lets a
// Bedrock id match the same bare keys as the first-party id.
var bedrockProviders = []string{"anthropic.", "openai."}

// canonical strips a Bedrock "[region.]provider." prefix so ids like
// "openai.gpt-5.5" or "us.anthropic.claude-sonnet-4-6" match the bare keys.
func canonical(model string) string {
	for _, p := range bedrockProviders {
		if i := strings.Index(model, p); i != -1 {
			return model[i+len(p):]
		}
	}
	return model
}

// For returns the rate for a model id by longest-prefix match, and whether a
// price was found. The id is matched as given first — so a pricing.json key for
// a provider-prefixed id (e.g. "openai.gpt-5") wins — then retried with the
// Bedrock provider prefix stripped, so a Bedrock-hosted model still prices like
// its first-party form when no provider-specific key exists.
func (t Table) For(model string) (Rate, bool) {
	if r, ok := t.match(model); ok {
		return r, true
	}
	if c := canonical(model); c != model {
		return t.match(c)
	}
	return Rate{}, false
}

// match does a single longest-prefix lookup against the table.
func (t Table) match(model string) (Rate, bool) {
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
