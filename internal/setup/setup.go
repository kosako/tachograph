// Package setup generates and applies the Claude Code statusLine
// configuration. It exists so first-time users don't have to hand-edit
// ~/.claude/settings.json or guess the absolute path to the tacho binary
// when it isn't on their PATH.
package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Command builds the shell command Claude Code runs for the status line.
// When the bare `tacho` on the PATH is the running binary itself, the short
// form is safe; otherwise the absolute path is baked in so the snippet runs
// the binary that generated it — never a different install shadowing it on
// the PATH (#193). Callers must pass a non-empty exe. A path containing
// spaces is double-quoted so the shell treats it as one argument.
func Command(bareIsSelf bool, exe string) string {
	if bareIsSelf {
		return "tacho statusline"
	}
	if strings.ContainsAny(exe, " \t") {
		return `"` + exe + `" statusline`
	}
	return exe + " statusline"
}

// statusLine mirrors the Claude Code settings block we manage.
type statusLine struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding int    `json:"padding"`
}

// Snippet renders the JSON block to paste into ~/.claude/settings.json.
func Snippet(command string) string {
	b, _ := json.MarshalIndent(map[string]statusLine{
		"statusLine": {Type: "command", Command: command, Padding: 0},
	}, "", "  ")
	return string(b)
}

// MergeSettings returns existing settings with the statusLine key set to our
// command, preserving every other top-level key. existing may be empty (a
// fresh file). An existing file that isn't a JSON object is an error rather
// than something we silently overwrite.
func MergeSettings(existing []byte, command string) ([]byte, error) {
	obj := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &obj); err != nil {
			return nil, fmt.Errorf("existing settings is not valid JSON: %w", err)
		}
	}
	sl, _ := json.Marshal(statusLine{Type: "command", Command: command, Padding: 0})
	obj["statusLine"] = sl
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
