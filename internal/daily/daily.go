// Package daily aggregates today's usage across all of a tool's sessions.
// It reads the same on-disk logs the collectors use, summing only entries
// dated today (local time).
package daily

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ClaudeTokens sums total tokens across today's Claude transcript messages
// in every project under <root>/projects. root defaults to ~/.claude.
func ClaudeTokens(root string, now time.Time) int64 {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return 0
		}
		root = filepath.Join(home, ".claude")
	}
	day := now.Local().Format("2006-01-02")
	var total int64

	projects := filepath.Join(root, "projects")
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return 0
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projects, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			info, err := f.Info()
			if err != nil || info.ModTime().Local().Format("2006-01-02") != day {
				continue // only files touched today can hold today's messages
			}
			total += claudeFileTokens(filepath.Join(projects, d.Name(), f.Name()), day)
		}
	}
	return total
}

type claudeLine struct {
	Timestamp string `json:"timestamp"`
	Message   *struct {
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func claudeFileTokens(path, day string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var total int64
	for _, raw := range bytes.Split(b, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || !bytes.Contains(raw, []byte(`"usage"`)) {
			continue
		}
		var line claudeLine
		if json.Unmarshal(raw, &line) != nil || line.Message == nil || line.Message.Usage == nil {
			continue
		}
		if !sameDay(line.Timestamp, day) {
			continue
		}
		// Exclude cache reads: they re-read the same context every message and
		// would dwarf the figure. This is "new" tokens processed today.
		u := line.Message.Usage
		total += u.InputTokens + u.CacheCreationInputTokens + u.OutputTokens
	}
	return total
}

// CodexTokens sums total tokens across today's Codex sessions. root defaults
// to ~/.codex; today's sessions live under sessions/YYYY/MM/DD.
func CodexTokens(root string, now time.Time) int64 {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return 0
		}
		root = filepath.Join(home, ".codex")
	}
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		total += codexSessionTokens(filepath.Join(dayDir, e.Name()))
	}
	return total
}

type codexEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexTokenCount struct {
	Type string `json:"type"`
	Info *struct {
		TotalTokenUsage *struct {
			TotalTokens       int64 `json:"total_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

// codexSessionTokens returns the session's new tokens (final cumulative total
// minus cached input, to match Claude's cache-read exclusion).
func codexSessionTokens(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || !bytes.Contains(line, []byte("token_count")) {
			continue
		}
		var ev codexEvent
		if json.Unmarshal(line, &ev) != nil || ev.Type != "event_msg" {
			continue
		}
		var tc codexTokenCount
		if json.Unmarshal(ev.Payload, &tc) == nil && tc.Type == "token_count" &&
			tc.Info != nil && tc.Info.TotalTokenUsage != nil {
			u := tc.Info.TotalTokenUsage
			n := u.TotalTokens - u.CachedInputTokens
			if n < 0 {
				n = 0
			}
			return n
		}
	}
	return 0
}

// sameDay reports whether an RFC 3339 timestamp falls on the given local day.
func sameDay(ts, day string) bool {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return t.Local().Format("2006-01-02") == day
}
