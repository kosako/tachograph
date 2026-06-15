package render

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func TestPresetTemplate(t *testing.T) {
	if _, ok := PresetTemplate("nope"); ok {
		t.Error("unknown preset reported as found")
	}
	tmpl, ok := PresetTemplate("bar")
	if !ok || tmpl != DefaultTemplate {
		t.Errorf("bar preset = %q (ok=%v), want DefaultTemplate", tmpl, ok)
	}
}

// Every preset must render without leaving raw {placeholders} behind, which
// would mean a typo'd field name slipped into the catalog.
func TestPresetsRender(t *testing.T) {
	now := time.Now()
	st := Style{Color: false}
	s := schema.Status{Tools: []schema.Tool{
		{Tool: schema.ToolClaudeCode, Available: true},
		{Tool: schema.ToolCodex, Available: true},
	}}
	for _, p := range Presets {
		out := Template(p.Template, s, now, st)
		if placeholderRe.MatchString(out) {
			t.Errorf("preset %q left an unexpanded placeholder: %q", p.Name, out)
		}
	}
}

func TestPresetNamesMatchCatalog(t *testing.T) {
	if len(PresetNames()) != len(Presets) {
		t.Fatalf("PresetNames len %d != Presets len %d", len(PresetNames()), len(Presets))
	}
}
