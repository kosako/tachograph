package render

import (
	"strings"
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

// Every preset must render a value for every field it uses, or a typo'd field
// or tool name slipped into the catalog. Template expands every well-formed
// {tool.field}, an unknown one included (as Missing), so a typo only shows
// as "--" against a status that has a value for each field the presets use.
// Rendering through resolve itself, rather than checking the placeholders
// against a list of known fields kept here, leaves nothing to drift: a field
// resolve learns is accepted at once, and a preset using a field this status
// leaves empty fails until the status gets a value for it. A brace left in
// the output is a malformed placeholder the pattern didn't match.
func TestPresetsRender(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-06-12T21:00:00+09:00")
	// testStatus fills every Claude field; its Codex is unavailable, so
	// give Codex both windows too.
	codex := limitsTool()
	codex.Tool = schema.ToolCodex
	s := schema.Status{Tools: []schema.Tool{testStatus().Tools[0], codex}}
	for _, p := range Presets {
		out := Template(p.Template, s, now, plain)
		if strings.ContainsAny(out, "{}") {
			t.Errorf("preset %q left an unexpanded placeholder: %q", p.Name, out)
		}
		if strings.Contains(out, Missing) {
			t.Errorf("preset %q rendered %q for a field the status fills (a typo'd field or tool name?): %q", p.Name, Missing, out)
		}
	}
}

// PresetNames lists each preset's name in catalog order, and no two presets
// share a name: PresetTemplate takes the first match, so a duplicate would
// shadow the later preset.
func TestPresetNamesMatchCatalog(t *testing.T) {
	names := PresetNames()
	if len(names) != len(Presets) {
		t.Fatalf("PresetNames len %d != Presets len %d", len(names), len(Presets))
	}
	seen := map[string]bool{}
	for i, p := range Presets {
		if names[i] != p.Name {
			t.Errorf("PresetNames()[%d] = %q, want %q", i, names[i], p.Name)
		}
		if seen[p.Name] {
			t.Errorf("preset name %q is used twice", p.Name)
		}
		seen[p.Name] = true
	}
}
