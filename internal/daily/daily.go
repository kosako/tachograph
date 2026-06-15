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

	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/schema"
)

// Totals is today's aggregate for one tool.
type Totals struct {
	Tokens int64
	Cost   float64
}

// Schema packs Totals into the wire type. Cost is set only when non-zero (a
// priced model was seen), so an unpriced model reads as "tokens known, cost
// unknown" rather than "$0.00".
func (t Totals) Schema() *schema.Daily {
	d := &schema.Daily{Tokens: t.Tokens}
	if t.Cost > 0 {
		c := t.Cost
		d.CostUSD = &c
	}
	return d
}

// ClaudeSessionToday totals today's new tokens and estimated cost for a single
// Claude session transcript (used for the {tool.*.session.today} scope). It
// reuses the same per-message accounting as ClaudeTotals, restricted to one
// file.
func ClaudeSessionToday(transcriptPath string, now time.Time, prices pricing.Table) Totals {
	if transcriptPath == "" {
		return Totals{}
	}
	return claudeFileTotals(transcriptPath, now.Local().Format("2006-01-02"), prices)
}

// ClaudeTotals sums today's new tokens and estimated cost across every Claude
// transcript message under <root>/projects. root defaults to ~/.claude.
func ClaudeTotals(root string, now time.Time, prices pricing.Table) Totals {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Totals{}
		}
		root = filepath.Join(home, ".claude")
	}
	day := now.Local().Format("2006-01-02")
	var out Totals

	projects := filepath.Join(root, "projects")
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return Totals{}
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
			ft := claudeFileTotals(filepath.Join(projects, d.Name(), f.Name()), day, prices)
			out.Tokens += ft.Tokens
			out.Cost += ft.Cost
		}
	}
	return out
}

type claudeLine struct {
	Timestamp string `json:"timestamp"`
	Message   *struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func claudeFileTotals(path, day string, prices pricing.Table) Totals {
	b, err := os.ReadFile(path)
	if err != nil {
		return Totals{}
	}
	var out Totals
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
		u := line.Message.Usage
		// New tokens exclude cache reads (same context re-read each message).
		out.Tokens += u.InputTokens + u.CacheCreationInputTokens + u.OutputTokens
		if r, ok := prices.For(line.Message.Model); ok {
			out.Cost += r.Cost(u.InputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens, u.OutputTokens)
		}
	}
	return out
}

// CodexTotals sums today's new tokens and estimated cost across Codex
// sessions. root defaults to ~/.codex; today's sessions live under
// sessions/YYYY/MM/DD.
func CodexTotals(root string, now time.Time, prices pricing.Table) Totals {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Totals{}
		}
		root = filepath.Join(home, ".codex")
	}
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		return Totals{}
	}
	var out Totals
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		st := codexSessionTotals(filepath.Join(dayDir, e.Name()), prices)
		out.Tokens += st.Tokens
		out.Cost += st.Cost
	}
	return out
}

type codexEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexTokenCount struct {
	Type string `json:"type"`
	Info *struct {
		TotalTokenUsage *struct {
			InputTokens       int64 `json:"input_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
			TotalTokens       int64 `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

// codexSessionTotals returns the session's new tokens (cumulative total minus
// cached input) and estimated cost, priced with the session's model.
func codexSessionTotals(path string, prices pricing.Table) Totals {
	b, err := os.ReadFile(path)
	if err != nil {
		return Totals{}
	}
	lines := bytes.Split(b, []byte("\n"))

	model := ""
	for i := len(lines) - 1; i >= 0 && model == ""; i-- {
		line := bytes.TrimSpace(lines[i])
		if !bytes.Contains(line, []byte("turn_context")) {
			continue
		}
		var ev codexEvent
		if json.Unmarshal(line, &ev) != nil || ev.Type != "turn_context" {
			continue
		}
		var p struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			model = p.Model
		}
	}

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
			out := Totals{Tokens: u.TotalTokens - u.CachedInputTokens}
			if out.Tokens < 0 {
				out.Tokens = 0
			}
			if r, ok := prices.For(model); ok {
				nonCached := u.InputTokens - u.CachedInputTokens
				if nonCached < 0 {
					nonCached = 0
				}
				out.Cost = r.Cost(nonCached, 0, u.CachedInputTokens, u.OutputTokens)
			}
			return out
		}
	}
	return Totals{}
}

// sameDay reports whether an RFC 3339 timestamp falls on the given local day.
func sameDay(ts, day string) bool {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return t.Local().Format("2006-01-02") == day
}
