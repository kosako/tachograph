package render

// Preset is a named, ready-to-use statusline template. Presets are a catalog
// of common "I want it to look like X" choices so users don't have to assemble
// the {tool.field} placeholders themselves.
type Preset struct {
	Name     string
	Desc     string
	Template string
}

// Presets is the ordered catalog surfaced by `tacho config statusline-preset`
// and documented in the README. "bar" is the built-in default.
var Presets = []Preset{
	{
		Name:     "bar",
		Desc:     "default — context + 5h gauge + weekly, both tools",
		Template: DefaultTemplate,
	},
	{
		Name:     "minimal",
		Desc:     "model + 5h/weekly percentages only",
		Template: "{claude.model} {claude.effort}5h {claude.5h.pct} · wk {claude.wk.pct}",
	},
	{
		Name:     "dial",
		Desc:     "compact single-character dials (○◔◑◕●)",
		Template: "{claude.model} {claude.effort}ctx {claude.ctx} · 5h {claude.5h.dial} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.dial} · codex {codex.5h.dial}{codex.wk.dial}",
	},
	{
		Name:     "moon",
		Desc:     "moon-phase dials (🌑🌒🌓🌔🌕)",
		Template: "{claude.model} {claude.effort}5h {claude.5h.moon} {claude.5h.pct} · wk {claude.wk.moon} · codex {codex.5h.moon}{codex.wk.moon}",
	},
	{
		Name:     "cost",
		Desc:     "model + context + 5h + this session's tokens/cost",
		Template: "{claude.model} {claude.effort}ctx {claude.ctx} · 5h {claude.5h.pct} · {claude.tokens} {claude.cost}",
	},
	{
		Name:     "cwd",
		Desc:     "working directory + model + context + 5h gauge",
		Template: "{claude.cwd} · {claude.model} {claude.effort}ctx {claude.ctx} · 5h {claude.5h.bar:6} {claude.5h.pct}",
	},
}

// PresetTemplate returns the template for a named preset.
func PresetTemplate(name string) (string, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p.Template, true
		}
	}
	return "", false
}

// PresetNames lists the preset names in catalog order.
func PresetNames() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}
