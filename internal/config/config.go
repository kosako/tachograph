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

	"github.com/kosako/tachograph/internal/schema"
)

// Menu bar display styles.
const (
	StyleMeter  = "meter"  // logo + colored progress ring image
	StyleNumber = "number" // plain text value
)

// DefaultMetric drives the gauge / number when none is configured.
const DefaultMetric = "limit_5h"

// Config is the persisted preference set.
type Config struct {
	Tools   []string `json:"tools"` // which tools to show, in order
	Menubar Menubar  `json:"menubar"`
}

type Menubar struct {
	Style  string `json:"style"`  // StyleMeter | StyleNumber
	Metric string `json:"metric"` // see render.MenubarMetrics
}

// Default is the configuration applied when no file exists — it preserves the
// original behavior (both tools, meter style, 5-hour limit).
func Default() Config {
	return Config{
		Tools:   []string{schema.ToolClaudeCode, schema.ToolCodex},
		Menubar: Menubar{Style: StyleMeter, Metric: DefaultMetric},
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
	// Unmarshal onto the defaults: absent keys keep their default values. A
	// JSON array (including an explicit empty []) replaces Tools, so "show
	// nothing" is honored; only an absent/null tools key leaves it nil and
	// falls back to defaults. The writers persist [] (not null) for an empty
	// selection so that distinction survives a round-trip.
	if err := json.Unmarshal(b, &c); err != nil {
		// A partial unmarshal may have touched c; hand back clean defaults.
		return Default(), fmt.Errorf("%s: %w", p, err)
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
