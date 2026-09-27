// Package config persists user preferences for what tachograph displays.
// The file is ~/.config/tachograph/config.json (stdlib JSON, no deps).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// Menu bar display styles.
const (
	StyleMeter  = "meter"  // logo + colored progress ring image
	StyleNumber = "number" // plain text value
)

// DefaultMetric drives the gauge / number when none is configured.
const DefaultMetric = "limit_5h"

// DefaultLimitDisplay is what 5h / weekly percentages show when none is
// configured (render.LimitRemaining, the headroom display since #223).
const DefaultLimitDisplay = "remaining"

// Config is the persisted preference set.
type Config struct {
	Tools   []string `json:"tools"` // which tools to show, in order
	Menubar Menubar  `json:"menubar"`
	Limits  Limits   `json:"limits"`
	Notify  Notify   `json:"notify"`

	// warnings describes the values in the loaded file that no renderer
	// understands (see Warnings). Unexported so it stays out of the JSON
	// that `config show` prints and Save writes.
	warnings []string
}

// Warnings lists the values of the loaded config file that are not valid
// choices, with what tacho does instead (#230). Load keeps such values as
// written so the read path always renders something — an unknown style is
// drawn as meter, an unknown metric as "--", an unknown limits display as
// remaining, an unknown tool is ignored, an out-of-range threshold is
// dropped — and this is how `tacho doctor` / `tacho config show` surface
// the typo. The checks are the ones `tacho config set` rejects up front.
func (c Config) Warnings() []string {
	return c.warnings
}

// ValidStyle reports whether v is a menu bar display style.
func ValidStyle(v string) bool {
	return v == StyleMeter || v == StyleNumber
}

// ValidTool reports whether name is a tool tacho knows.
func ValidTool(name string) bool {
	return name == schema.ToolClaudeCode || name == schema.ToolCodex
}

// valueWarnings checks every enum-like key of a loaded config against the
// same validators `config set` uses. thresholds are the raw values, before
// NormalizeThresholds drops the invalid ones.
func valueWarnings(c Config, thresholds []int) []string {
	var w []string
	for _, t := range c.Tools {
		if !ValidTool(t) {
			w = append(w, fmt.Sprintf("tools: %q is not claude-code or codex — ignored", t))
		}
	}
	if !ValidStyle(c.Menubar.Style) {
		w = append(w, fmt.Sprintf("menubar.style: %q is not meter or number — shown as meter", c.Menubar.Style))
	}
	if !render.ValidMenubarMetric(c.Menubar.Metric) {
		w = append(w, fmt.Sprintf("menubar.metric: %q is not one of %s — shown as --", c.Menubar.Metric, strings.Join(render.MenubarMetrics, ", ")))
	}
	if !render.ValidLimitDisplay(c.Limits.Display) {
		w = append(w, fmt.Sprintf("limits.display: %q is not remaining or used — shown as remaining", c.Limits.Display))
	}
	for _, t := range thresholds {
		if !ValidThreshold(t) {
			w = append(w, fmt.Sprintf("notify.thresholds: %d is not a whole percentage from 1 to 99 — dropped", t))
		}
	}
	return w
}

type Menubar struct {
	Style  string `json:"style"`  // StyleMeter | StyleNumber
	Metric string `json:"metric"` // see render.MenubarMetrics
}

type Limits struct {
	Display string `json:"display"` // see render.LimitDisplay
}

// Notify configures the rate-limit headroom notifications raised from the
// SwiftBar refresh (#244). Thresholds are "% left" figures: a notification
// fires when a 5h / weekly window's headroom drops to or below one of them,
// once per reset cycle. Empty (the default) means off.
type Notify struct {
	Thresholds []int `json:"thresholds"`
}

// ValidThreshold reports whether t is a usable headroom threshold: a whole
// percentage strictly inside the 0–100 range.
func ValidThreshold(t int) bool {
	return t >= 1 && t <= 99
}

// NormalizeThresholds drops invalid and duplicate thresholds and sorts the
// rest descending, so callers see the highest (first crossed) threshold
// first. It never returns nil, so an empty selection persists as [].
func NormalizeThresholds(in []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, t := range in {
		if ValidThreshold(t) && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// Default is the configuration applied when no file exists — it preserves the
// original behavior (both tools, meter style, 5-hour limit, headroom).
func Default() Config {
	return Config{
		Tools:   []string{schema.ToolClaudeCode, schema.ToolCodex},
		Menubar: Menubar{Style: StyleMeter, Metric: DefaultMetric},
		Limits:  Limits{Display: DefaultLimitDisplay},
		Notify:  Notify{Thresholds: []int{}},
	}
}

// Dir returns the config directory, honoring TACHO_CONFIG_DIR and XDG.
func Dir() string {
	if d := os.Getenv("TACHO_CONFIG_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "tachograph")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "tachograph")
	}
	return ""
}

// Path is the config file location.
func Path() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "config.json")
}

// Load returns the saved config merged over defaults. A missing or invalid
// file yields defaults so the tool always renders something sensible. Write
// paths must use LoadStrict instead, so a broken file is surfaced rather
// than silently replaced with defaults.
func Load() Config {
	c, _ := load()
	return c
}

// LoadStrict is Load for write paths and diagnostics: it also reports a
// config file that exists but can't be read or parsed (the returned Config
// is the defaults in that case).
func LoadStrict() (Config, error) {
	return load()
}

func load() (Config, error) {
	c := Default()
	p := Path()
	if p == "" {
		return c, nil
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil // no file yet: plain defaults
	}
	if err != nil {
		return c, err
	}
	c, err = parse(b)
	if err != nil {
		return c, fmt.Errorf("%s: %w", p, err)
	}
	return c, nil
}

// Validate reports whether b decodes as a config.json the way Load reads it.
// Load falls back to all defaults on any decode error — a wrongly typed value
// as much as a syntax error — so doctor uses this to surface both (#260).
// Unknown enum values are not errors: they load and are listed by Warnings.
func Validate(b []byte) error {
	_, err := parse(b)
	return err
}

// parse is the single decode path shared by load and Validate. On error it
// returns clean defaults.
func parse(b []byte) (Config, error) {
	c := Default()
	// Unmarshal onto the defaults: absent keys keep their default values. A
	// JSON array (including an explicit empty []) replaces Tools, so "show
	// nothing" is honored; only an absent/null tools key leaves it nil and
	// falls back to defaults. The writers persist [] (not null) for an empty
	// selection so that distinction survives a round-trip.
	if err := json.Unmarshal(b, &c); err != nil {
		// A partial unmarshal may have touched c; hand back clean defaults.
		return Default(), err
	}
	if c.Tools == nil {
		c.Tools = Default().Tools
	}
	if c.Menubar.Style == "" {
		c.Menubar.Style = StyleMeter
	}
	if c.Menubar.Metric == "" {
		c.Menubar.Metric = DefaultMetric
	}
	if c.Limits.Display == "" {
		c.Limits.Display = DefaultLimitDisplay
	}
	// Out-of-range or duplicate thresholds in a hand-edited file are dropped
	// rather than failing the load (Load never fails); `config set` rejects
	// them up front. Every ignored value is kept as a warning for doctor /
	// config show (#230).
	c.warnings = valueWarnings(c, c.Notify.Thresholds)
	c.Notify.Thresholds = NormalizeThresholds(c.Notify.Thresholds)
	return c, nil
}

// Save writes the config atomically (tmp file + rename).
func Save(c Config) error {
	dir := Dir()
	if dir == "" {
		return fmt.Errorf("config: no home directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, "config.json.tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), Path())
}

// ToolEnabled reports whether a tool should be shown.
func (c Config) ToolEnabled(tool string) bool {
	for _, t := range c.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

// FilterStatus keeps only configured tools, in configured order.
func (c Config) FilterStatus(s schema.Status) schema.Status {
	out := s
	out.Tools = nil
	for _, name := range c.Tools {
		for _, t := range s.Tools {
			if t.Tool == name {
				out.Tools = append(out.Tools, t)
			}
		}
	}
	return out
}
