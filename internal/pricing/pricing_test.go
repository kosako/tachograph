package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForLongestPrefix(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir()) // pure defaults, ignore any local pricing.json
	tab := Load()
	if _, ok := tab.For("claude-opus-4-8"); !ok {
		t.Error("claude-opus-4-8 should match claude-opus")
	}
	if r, ok := tab.For("claude-fable-5"); !ok || r.In == 0 {
		t.Errorf("claude-fable-5 should be priced: %+v %v", r, ok)
	}
	if _, ok := tab.For("totally-unknown-model"); ok {
		t.Error("unknown model should not match")
	}
}

// Bedrock provider-prefixed ids and bare aliases (the strings actually seen in
// Codex/Claude logs) must price the same as their first-party / full-tier form.
func TestForBedrockPrefixAndAliases(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir()) // pure defaults, ignore any local pricing.json
	tab := Load()
	cases := []struct {
		model string
		want  Rate
	}{
		{"openai.gpt-5.5", tab["gpt-5.5"]},                       // Bedrock OpenAI → version key
		{"openai.gpt-5.4", tab["gpt-5.4"]},                       // Bedrock OpenAI → version key
		{"us.anthropic.claude-sonnet-4-6", tab["claude-sonnet"]}, // Bedrock cross-region Claude
		{"anthropic.claude-opus-4-8", tab["claude-opus"]},        // Bedrock Claude
		{"sonnet", tab["claude-sonnet"]},                         // bare alias
		{"opus", tab["claude-opus"]},                             // bare alias
		{"haiku", tab["claude-haiku"]},                           // bare alias
		{"gpt-5.5", tab["gpt-5.5"]},                              // first-party → version key
		{"gpt-5-codex", tab["gpt-5"]},                            // no version → gpt-5 base
		{"gpt-5.4-codex", tab["gpt-5.4"]},                        // -codex variant → 5.4 base
		{"gpt-5.6-sol", tab["gpt-5.6"]},                          // Sol tier id → base alias
		{"gpt-5.6-codex", tab["gpt-5.6"]},                        // -codex variant → 5.6 base (Sol)
		{"openai.gpt-5.6-terra", tab["gpt-5.6-terra"]},           // Bedrock OpenAI → tier key
		{"claude-opus-4-8", tab["claude-opus"]},                  // first-party (regression)
	}
	for _, c := range cases {
		r, ok := tab.For(c.model)
		if !ok {
			t.Errorf("%s: expected a price, got none", c.model)
			continue
		}
		if r != c.want {
			t.Errorf("%s: rate = %+v, want %+v", c.model, r, c.want)
		}
	}
	// A synthetic/placeholder model carries no billable price.
	if _, ok := tab.For("<synthetic>"); ok {
		t.Error("<synthetic> should not be priced")
	}
}

// The built-in defaults must reflect current first-party API prices (per-1M
// input/output). Locks the table against drift back to stale values.
func TestDefaultPricesCurrent(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	tab := Load()
	// Rate fields are {In, Out, CacheRead, CacheWrite}.
	cases := []struct {
		model string
		want  Rate
	}{
		{"claude-opus-4-8", Rate{5, 25, 0.5, 6.25}},
		{"claude-opus-5", Rate{5, 25, 0.5, 6.25}},              // same price as 4.8; resolves via the claude-opus key (#213)
		{"anthropic.claude-opus-5", Rate{5, 25, 0.5, 6.25}},    // Bedrock form of the same
		{"claude-opus-4-1-20250805", Rate{15, 75, 1.5, 18.75}}, // 4.1 kept the older price; not shadowed by claude-opus
		{"claude-sonnet-4-6", Rate{3, 15, 0.3, 3.75}},
		{"claude-sonnet-5", Rate{2, 10, 0.2, 2.5}},          // launch price made permanent, no 2026-09-01 increase (#200)
		{"claude-sonnet-5-20260401", Rate{2, 10, 0.2, 2.5}}, // dated id → sonnet-5 key, not shadowed by claude-sonnet
		{"claude-haiku-4-5", Rate{1, 5, 0.1, 1.25}},
		{"claude-fable-5", Rate{10, 50, 1, 12.5}},
		{"gpt-5.6", Rate{5, 30, 0.5, 6.25}},     // Sol (default tier)
		{"gpt-5.6-sol", Rate{5, 30, 0.5, 6.25}}, // full Sol id → base alias
		{"gpt-5.6-terra", Rate{2.5, 15, 0.25, 3.125}},
		{"gpt-5.6-luna", Rate{1, 6, 0.1, 1.25}},
		{"gpt-5.5", Rate{5, 30, 0.5, 5}}, // and openai.gpt-5.5 via canonical
		{"gpt-5.5-pro", Rate{30, 180, 3, 30}},
		{"gpt-5.4", Rate{2.5, 15, 0.25, 2.5}},
		{"gpt-5.4-codex", Rate{2.5, 15, 0.25, 2.5}}, // -codex variant falls to the base price
		{"gpt-5.4-mini", Rate{0.75, 4.5, 0.075, 0.75}},
		{"gpt-5.4-nano", Rate{0.2, 1.25, 0.02, 0.2}},
		{"gpt-5.4-pro", Rate{30, 180, 3, 30}},
	}
	for _, c := range cases {
		r, ok := tab.For(c.model)
		if !ok || r != c.want {
			t.Errorf("%s: got %+v (ok=%v), want %+v", c.model, r, ok, c.want)
		}
	}
}

// A pricing.json key for the provider-prefixed id must win over the canonical
// fallback to the bare default — otherwise a user couldn't price a Bedrock
// model separately from its first-party form.
func TestProviderOverrideBeatsCanonicalFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"),
		[]byte(`{"openai.gpt-5":{"input":2,"output":20,"cache_read":0.2,"cache_write":2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := Load().For("openai.gpt-5.5")
	// The provider-specific override (In=2) must win over the bare gpt-5 default (In=1.25).
	if !ok || r.In != 2 || r.Out != 20 {
		t.Errorf("provider override not honored: %+v %v, want In=2 Out=20", r, ok)
	}
}

func TestOverrideFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"),
		[]byte(`{"claude-fable":{"input":99,"output":1,"cache_read":0,"cache_write":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := Load().For("claude-fable-5")
	if !ok || r.In != 99 || r.Out != 1 {
		t.Errorf("override not applied: %+v %v", r, ok)
	}
}

// A partial override must keep the other prices at their built-in defaults
// rather than zeroing them (which would silently undercount cost).
func TestPartialOverrideMergesOverDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"),
		[]byte(`{"claude-opus":{"input":20}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := Load().For("claude-opus-4-8")
	if !ok {
		t.Fatal("claude-opus should be priced")
	}
	// input overridden; output / cache_read / cache_write keep defaults.
	if r.In != 20 || r.Out != 25 || r.CacheRead != 0.5 || r.CacheWrite != 6.25 {
		t.Errorf("partial override = %+v, want In=20 with default Out/CacheRead/CacheWrite", r)
	}
}

// An explicit 0 must be honored (distinct from an omitted field).
func TestExplicitZeroOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"),
		[]byte(`{"claude-opus":{"cache_read":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := Load().For("claude-opus-4-8")
	if r.CacheRead != 0 || r.In != 5 {
		t.Errorf("explicit zero override = %+v, want CacheRead=0 with default In=5", r)
	}
}

// A brand-new model id (not in defaults) takes only the fields given; the rest
// are 0, since there's no default to fall back to.
func TestNewModelOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"),
		[]byte(`{"my-model":{"input":5,"output":10}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := Load().For("my-model-x")
	if !ok || r.In != 5 || r.Out != 10 || r.CacheRead != 0 {
		t.Errorf("new model override = %+v %v, want In=5 Out=10 CacheRead=0", r, ok)
	}
}

func TestCost(t *testing.T) {
	r := Rate{In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}
	// (1_000_000*15 + 0 + 0 + 1_000_000*75) / 1e6 = 90
	if got := r.Cost(1_000_000, 0, 0, 1_000_000); got != 90 {
		t.Errorf("Cost = %v, want 90", got)
	}
}
