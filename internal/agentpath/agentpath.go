// Package agentpath resolves on-disk homes for agent CLIs.
package agentpath

import (
	"os"
	"path/filepath"
)

// ClaudeRoot resolves the Claude Code config root.
func ClaudeRoot(root string) (string, bool) {
	if root != "" {
		return root, true
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".claude"), true
}

// CodexRoot resolves the Codex config root.
func CodexRoot(root string) (string, bool) {
	if root != "" {
		return root, true
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".codex"), true
}
