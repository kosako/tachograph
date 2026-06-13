package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForLongestPrefix(t *testing.T) {
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

func TestCost(t *testing.T) {
	r := Rate{In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}
	// (1_000_000*15 + 0 + 0 + 1_000_000*75) / 1e6 = 90
	if got := r.Cost(1_000_000, 0, 0, 1_000_000); got != 90 {
		t.Errorf("Cost = %v, want 90", got)
	}
}
