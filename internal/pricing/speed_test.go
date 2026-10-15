package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

// A fast response on a fast-mode model bills every category at twice the
// standard rate; any other speed, or a model without a known premium, bills
// at the standard rate (#407).
func TestForSpeed(t *testing.T) {
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir()) // pure defaults, ignore any local pricing.json
	tab := Load()
	double := func(r Rate) Rate {
		return Rate{In: r.In * 2, Out: r.Out * 2, CacheRead: r.CacheRead * 2, CacheWrite: r.CacheWrite * 2}
	}
	cases := []struct {
		model, speed string
		want         Rate
	}{
		// The published fast-mode prices, cache multipliers on top.
		{"claude-opus-5-5", SpeedFast, Rate{In: 8, Out: 40, CacheRead: 0.4, CacheWrite: 10}},
		{"claude-opus-5", SpeedFast, Rate{In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5}},
		{"claude-opus-4-8", SpeedFast, Rate{In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5}},
		{"anthropic.claude-opus-4-8", SpeedFast, double(tab["claude-opus"])}, // the canonical id decides
		// The date and context-window suffixes the price lookup accepts.
		{"claude-opus-5-5-20260923", SpeedFast, double(tab["claude-opus-5-5"])},
		{"claude-opus-5-5[1m]", SpeedFast, double(tab["claude-opus-5-5"])},
		{"claude-opus-5-5-20260923[1m]", SpeedFast, double(tab["claude-opus-5-5"])},
		{"claude-opus-5[1m]", SpeedFast, double(tab["claude-opus"])},
		{"claude-opus-5-20260501", SpeedFast, double(tab["claude-opus"])},
		{"claude-opus-4-8-20260527", SpeedFast, double(tab["claude-opus"])},
		// Standard, or speed not recorded: the standard rate.
		{"claude-opus-5-5", "standard", tab["claude-opus-5-5"]},
		{"claude-opus-5-5", "", tab["claude-opus-5-5"]},
		{"claude-opus-5-5[1m]", "standard", tab["claude-opus-5-5"]},
		{"claude-opus-4-8", "standard", tab["claude-opus"]},
		// No known premium: the standard rate, not a guessed one.
		{"claude-opus-4-7", SpeedFast, tab["claude-opus"]},
		{"claude-opus-4-7-20260416", SpeedFast, tab["claude-opus"]},
		{"claude-opus-5-5-preview", SpeedFast, tab["claude-opus-5-5"]},
		{"claude-opus-5-5-2026", SpeedFast, tab["claude-opus-5-5"]}, // not an 8-digit date
		{"claude-sonnet-5-5", SpeedFast, tab["claude-sonnet-5-5"]},
	}
	for _, c := range cases {
		r, ok := tab.ForSpeed(c.model, c.speed)
		if !ok {
			t.Errorf("ForSpeed(%q, %q): no price", c.model, c.speed)
			continue
		}
		if r != c.want {
			t.Errorf("ForSpeed(%q, %q) = %+v, want %+v", c.model, c.speed, r, c.want)
		}
	}
	if _, ok := tab.ForSpeed("totally-unknown-model", SpeedFast); ok {
		t.Error("ForSpeed priced an unknown model")
	}
}

// The premium multiplies the standard rate in force, so a pricing.json
// override of the standard rate carries into fast mode.
func TestForSpeedFollowsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TACHO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"), []byte(`{"claude-opus-5-5":{"input":6}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := Load().ForSpeed("claude-opus-5-5", SpeedFast)
	if want := (Rate{In: 12, Out: 40, CacheRead: 0.4, CacheWrite: 10}); !ok || r != want {
		t.Errorf("ForSpeed with an input override = %+v, %v; want %+v", r, ok, want)
	}
}
