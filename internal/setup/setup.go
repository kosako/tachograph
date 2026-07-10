// Package setup generates and applies the Claude Code statusLine
// configuration. It exists so first-time users don't have to hand-edit
// ~/.claude/settings.json or guess the absolute path to the tacho binary
// when it isn't on their PATH.
package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Command builds the shell command Claude Code runs for the status line.
// When the bare `tacho` on the PATH is the running binary itself, the short
// form is safe; otherwise the absolute path is baked in so the snippet runs
// the binary that generated it — never a different install shadowing it on
// the PATH (#193). Callers must pass a non-empty exe. Only paths made of
// known-inert characters are emitted bare; anything else is serialized as
// one double-quoted POSIX shell argument so no character can split or
// rewrite the command (#194 L-01).
func Command(bareIsSelf bool, exe string) string {
	if bareIsSelf {
		return "tacho statusline"
	}
	if safeExe.MatchString(exe) {
		return exe + " statusline"
	}
	return quoteExe(exe) + " statusline"
}

// safeExe matches paths that no shell reinterprets when unquoted: letters,
// digits, and the punctuation real install paths use — including Windows
// drive colons and backslash separators, which must stay unquoted and
// unescaped for cmd-style interpreters.
var safeExe = regexp.MustCompile(`^[A-Za-z0-9_./:\\-]+$`)

// quoteExe serializes a path as one POSIX-shell double-quoted argument.
// Inside double quotes only " $ ` and \ keep meaning — and \ only when it
// precedes one of those, a newline, or the end of the string (the closing
// quote) — so backslashes are escaped exactly there and stay literal
// elsewhere, keeping Windows separators (backslash before a letter) intact.
func quoteExe(exe string) string {
	var b strings.Builder
	b.WriteByte('"')
	rs := []rune(exe)
	for i, r := range rs {
		switch r {
		case '"', '$', '`':
			b.WriteByte('\\')
		case '\\':
			if i+1 == len(rs) {
				b.WriteByte('\\')
			} else if n := rs[i+1]; n == '"' || n == '$' || n == '`' || n == '\\' || n == '\n' {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
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
