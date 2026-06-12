// Package cmuxbar is the R3 renderer: it pushes per-tool status pills to
// the cmux sidebar via the cmux CLI (set-status / clear-status).
//
// It piggybacks on `tacho statusline` invocations — no resident process.
// Inside a cmux terminal CMUX_WORKSPACE_ID is set and the cmux CLI uses it
// as the default --workspace, so pills land on the right workspace
// automatically.
package cmuxbar

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

// appBundleCLI is where the cmux.app ships its CLI when it is not on PATH.
const appBundleCLI = "/Applications/cmux.app/Contents/Resources/bin/cmux"

// Sidebar pill colors (used / stale pressure).
const (
	colorGreen  = "#34C759"
	colorYellow = "#FFCC00"
	colorRed    = "#FF3B30"
	colorGray   = "#8E8E93"
)

// Pill emoji prefixes — the sidebar shows only the value, so these are
// what identifies the tool at a glance.
const (
	emojiClaude = "✳️"
	emojiCodex  = "🤖"
)

// Detect reports whether we are running inside a cmux terminal.
func Detect() bool {
	return os.Getenv("CMUX_WORKSPACE_ID") != ""
}

// FindCLI locates the cmux CLI. TACHO_CMUX_BIN wins, then PATH, then the
// app bundle. Empty when cmux is not installed.
func FindCLI() string {
	if p := os.Getenv("TACHO_CMUX_BIN"); p != "" {
		return p
	}
	if p, err := exec.LookPath("cmux"); err == nil {
		return p
	}
	if _, err := os.Stat(appBundleCLI); err == nil {
		return appBundleCLI
	}
	return ""
}

// Pill is one sidebar status entry.
type Pill struct {
	Key   string
	Value string
	Color string
}

// Pills builds one pill per available tool.
func Pills(s schema.Status, now time.Time) []Pill {
	var pills []Pill
	for _, t := range s.Tools {
		if !t.Available || t.Error != nil {
			continue
		}
		key, emoji := t.Tool, emojiCodex
		if key == schema.ToolClaudeCode {
			key, emoji = "claude", emojiClaude
		}
		pills = append(pills, Pill{Key: key, Value: emoji + " " + pillValue(t, now), Color: pillColor(t)})
	}
	return pills
}

func pillValue(t schema.Tool, now time.Time) string {
	var parts []string
	if t.Stale && t.CollectedAt != nil {
		if a := render.Age(*t.CollectedAt, now); a != "" {
			parts = append(parts, "⚠"+a)
		}
	}
	if t.Session != nil && t.Session.ContextUsedPct != nil {
		parts = append(parts, fmt.Sprintf("ctx%.0f%%", *t.Session.ContextUsedPct))
	}
	if t.Limits != nil {
		for _, l := range t.Limits {
			if l.UsedPct == nil {
				continue
			}
			label := l.Window
			if label == schema.WindowWeekly {
				label = "wk"
			}
			parts = append(parts, fmt.Sprintf("%s%.0f%%", label, *l.UsedPct))
		}
	} else if t.Fallback != nil && t.Fallback.SessionTokens != nil {
		parts = append(parts, render.FormatTokens(*t.Fallback.SessionTokens)+"tok")
	}
	if len(parts) == 0 {
		return "--"
	}
	return strings.Join(parts, " ")
}

// pillColor reflects the highest limit pressure; gray for stale data.
func pillColor(t schema.Tool) string {
	if t.Stale {
		return colorGray
	}
	max := 0.0
	for _, l := range t.Limits {
		if l.UsedPct != nil && *l.UsedPct > max {
			max = *l.UsedPct
		}
	}
	switch {
	case max >= 80:
		return colorRed
	case max >= 50:
		return colorYellow
	default:
		return colorGreen
	}
}

// Push sends the pills. With wait=false (the statusline path) commands are
// started and abandoned so the caller's latency is unaffected; errors are
// best-effort by design. With wait=true it waits and reports the first
// failure (the `tacho cmux push` path).
func Push(cli string, s schema.Status, now time.Time, wait bool) error {
	var firstErr error
	for _, p := range Pills(s, now) {
		cmd := exec.Command(cli, "set-status", p.Key, p.Value, "--color", p.Color)
		if err := runCmd(cmd, wait); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Clear removes tacho's pills from the sidebar.
func Clear(cli string, wait bool) error {
	var firstErr error
	for _, key := range []string{"claude", "codex"} {
		cmd := exec.Command(cli, "clear-status", key)
		if err := runCmd(cmd, wait); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func runCmd(cmd *exec.Cmd, wait bool) error {
	if !wait {
		if err := cmd.Start(); err != nil {
			return err
		}
		// Reap in the background so we exit immediately without zombies.
		go cmd.Wait()
		return nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", cmd.Args[1], err, strings.TrimSpace(string(out)))
	}
	return nil
}
