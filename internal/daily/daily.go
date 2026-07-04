// Package daily aggregates today's usage across all of a tool's sessions.
// It reads the same on-disk logs the collectors use, summing only entries
// dated today (local time).
package daily

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
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

// ClaudeSessionToday totals today's new tokens and estimated cost for one
// Claude session transcript plus its nested subagent/workflow transcripts
// (used for the {tool.*.session.today} scope). It reuses the same per-message
// accounting as ClaudeTotals with a fresh dedup set for that session tree.
func ClaudeSessionToday(transcriptPath string, now time.Time, prices pricing.Table) Totals {
	if transcriptPath == "" {
		return Totals{}
	}
	day := now.Local().Format("2006-01-02")
	seen := map[usageKey]bool{}
	out := claudeFileTotals(transcriptPath, day, prices, seen)

	sessionDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	_ = filepath.WalkDir(sessionDir, func(path string, f os.DirEntry, err error) error {
		if err != nil || f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
			return nil
		}
		info, err := f.Info()
		if err != nil || info.ModTime().Local().Format("2006-01-02") != day {
			return nil
		}
		ft := claudeFileTotals(path, day, prices, seen)
		out.Tokens += ft.Tokens
		out.Cost += ft.Cost
		return nil
	})
	return out
}

// ClaudeTotals sums today's new tokens and estimated cost across every Claude
// transcript message under <root>/projects. root defaults to CLAUDE_CONFIG_DIR
// or ~/.claude.
func ClaudeTotals(root string, now time.Time, prices pricing.Table) Totals {
	var ok bool
	root, ok = agentpath.ClaudeRoot(root)
	if !ok {
		return Totals{}
	}
	day := now.Local().Format("2006-01-02")
	var out Totals
	// One dedup set spans every file so a response duplicated across files
	// (resume/compaction copies prior turns forward) is also counted once.
	seen := map[usageKey]bool{}

	projects := filepath.Join(root, "projects")
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return Totals{}
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		projectDir := filepath.Join(projects, d.Name())
		err := filepath.WalkDir(projectDir, func(path string, f os.DirEntry, err error) error {
			if err != nil || f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				return nil
			}
			info, err := f.Info()
			if err != nil || info.ModTime().Local().Format("2006-01-02") != day {
				return nil // only files touched today can hold today's messages
			}
			ft := claudeFileTotals(path, day, prices, seen)
			out.Tokens += ft.Tokens
			out.Cost += ft.Cost
			return nil
		})
		if err != nil {
			continue
		}
	}
	return out
}

// usageKey identifies one assistant API response. Claude Code writes a
// transcript line per content block (thinking/text/tool_use…), each repeating
// that response's full usage, so counting per line multiplies a turn by its
// block count. Summing once per (message id, request id) counts each response
// once — across its blocks and across files duplicated by resume/compaction.
type usageKey struct{ id, req string }

type claudeUsage struct {
	InputTokens              int64                `json:"input_tokens"`
	CacheCreationInputTokens int64                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                `json:"cache_read_input_tokens"`
	OutputTokens             int64                `json:"output_tokens"`
	CacheCreation            *claudeCacheCreation `json:"cache_creation"`
}

type claudeCacheCreation struct {
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
}

type claudeLine struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   *struct {
		ID    string       `json:"id"`
		Model string       `json:"model"`
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
}

// claudeFileTotals sums one transcript's today entries into out, recording
// counted responses in seen so the caller can dedup across files.
func claudeFileTotals(path, day string, prices pricing.Table, seen map[usageKey]bool) Totals {
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
		// A response spans multiple content-block lines with identical usage;
		// count it once. Lines without a message id (rare, e.g. synthetic)
		// can't be deduped, so they're always counted.
		if id := line.Message.ID; id != "" {
			k := usageKey{id, line.RequestID}
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		u := line.Message.Usage
		cacheWrite5m, cacheWrite1h, cacheWriteUnknown, cacheWriteTotal := claudeCacheWrites(u)
		// New tokens exclude cache reads (same context re-read each message).
		out.Tokens += u.InputTokens + cacheWriteTotal + u.OutputTokens
		if r, ok := prices.For(line.Message.Model); ok {
			out.Cost += claudeAPICost(r, u.InputTokens, cacheWrite5m, cacheWrite1h, cacheWriteUnknown, u.CacheReadInputTokens, u.OutputTokens)
		}
	}
	return out
}

func claudeCacheWrites(u *claudeUsage) (fiveMinute, oneHour, unknown, total int64) {
	if u == nil {
		return 0, 0, 0, 0
	}
	total = u.CacheCreationInputTokens
	if u.CacheCreation == nil {
		return 0, 0, total, total
	}
	fiveMinute = u.CacheCreation.Ephemeral5mInputTokens
	oneHour = u.CacheCreation.Ephemeral1hInputTokens
	known := fiveMinute + oneHour
	if total >= known {
		return fiveMinute, oneHour, total - known, total
	}
	// Trust the top-level total when the TTL breakdown is inconsistent; there
	// is no reliable way to reconstruct the 5m/1h split from malformed logs.
	return 0, 0, total, total
}

func claudeAPICost(r pricing.Rate, in, cacheWrite5m, cacheWrite1h, cacheWriteUnknown, cacheRead, out int64) float64 {
	return (float64(in)*r.In +
		float64(cacheWrite5m+cacheWriteUnknown)*r.CacheWrite +
		float64(cacheWrite1h)*r.In*2 +
		float64(cacheRead)*r.CacheRead +
		float64(out)*r.Out) / 1_000_000
}

// codexScanDays is how many day directories (today and previous) CodexTotals
// scans. Rollouts live in their session's START-day directory, so a session
// running past midnight keeps writing to an older day's file; reading only
// today's directory would miss its today-portion entirely (#133). Matches the
// collector's cross-day comparison window.
const codexScanDays = 3

// CodexTotals sums today's new tokens and estimated cost across Codex
// sessions. root defaults to CODEX_HOME or ~/.codex. total_token_usage is
// cumulative per session, so each session contributes its growth since local
// midnight: the last cumulative snapshot minus the last snapshot before today
// (zero for sessions started today).
func CodexTotals(root string, now time.Time, prices pricing.Table) Totals {
	var ok bool
	root, ok = agentpath.CodexRoot(root)
	if !ok {
		return Totals{}
	}
	local := now.Local()
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())

	var out Totals
	// Dedup by session id (the UUID in the rollout filename) keeping the
	// largest cumulative on each side of midnight, so a session that resumed
	// into a second file isn't counted twice. Files without a parseable id
	// can't be deduped and are summed as-is.
	bySession := map[string]*codexRollout{}
	for back := 0; back < codexScanDays; back++ {
		day := local.AddDate(0, 0, -back)
		dayDir := filepath.Join(root, "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
		entries, err := os.ReadDir(dayDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			ro := codexRolloutUsage(filepath.Join(dayDir, e.Name()), dayStart)
			if ro.last == nil {
				continue
			}
			// Without any parsable timestamp the cumulative can't be split
			// at midnight; attribute it to the file's own day directory
			// (count fully in today's directory, ignore in older ones).
			if !ro.tsParsed && back != 0 {
				continue
			}
			m := codexSessionIDRe.FindStringSubmatch(e.Name())
			if m == nil {
				t := codexDeltaTotals(ro.model, ro.before, ro.last, prices)
				out.Tokens += t.Tokens
				out.Cost += t.Cost
				continue
			}
			mergeCodexRollout(bySession, m[1], ro)
		}
	}
	for _, ro := range bySession {
		t := codexDeltaTotals(ro.model, ro.before, ro.last, prices)
		out.Tokens += t.Tokens
		out.Cost += t.Cost
	}
	return out
}

// mergeCodexRollout folds one rollout into its session's entry. Cumulative
// usage only grows within a session (resumed files carry the total forward),
// so the largest snapshot on each side of midnight is that side's latest.
func mergeCodexRollout(bySession map[string]*codexRollout, id string, ro codexRollout) {
	prev, ok := bySession[id]
	if !ok {
		bySession[id] = &ro
		return
	}
	if ro.before != nil && (prev.before == nil || ro.before.Total > prev.before.Total) {
		prev.before = ro.before
	}
	if ro.last.Total > prev.last.Total {
		prev.last, prev.model = ro.last, ro.model
	}
}

// codexDeltaTotals prices the growth from before (nil = session started
// today) to last. Tokens counts new tokens — cumulative total minus cached
// input — the same measure the whole-session accounting used.
func codexDeltaTotals(model string, before, last *codexUsage, prices pricing.Table) Totals {
	var b codexUsage
	if before != nil {
		b = *before
	}
	out := Totals{Tokens: clamp0((last.Total - last.Cached) - (b.Total - b.Cached))}
	if r, ok := prices.For(model); ok {
		in := clamp0(last.Input - b.Input)
		cached := clamp0(last.Cached - b.Cached)
		outTok := clamp0(last.Output - b.Output)
		out.Cost = r.Cost(clamp0(in-cached), 0, cached, outTok)
	}
	return out
}

func clamp0(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// codexSessionIDRe extracts the session UUID from a rollout filename
// (rollout-<ISO-ts>-<uuid>.jsonl) so daily totals can dedup a session that
// resumed into more than one file.
var codexSessionIDRe = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

type codexEvent struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
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

// codexUsage is one cumulative total_token_usage snapshot.
type codexUsage struct {
	Input, Cached, Output, Total int64
}

// codexRollout is what one rollout file contributes to today's totals: the
// last cumulative snapshot strictly before local midnight (nil when the file
// has none) and the last snapshot overall, plus the session's model.
type codexRollout struct {
	model    string
	before   *codexUsage
	last     *codexUsage
	tsParsed bool // at least one token_count carried a parsable timestamp
}

// codexRolloutUsage extracts one rollout's cumulative usage snapshots around
// dayStart. Events are appended in order, so a forward scan keeps the latest
// snapshot on each side of midnight and the latest turn_context model.
func codexRolloutUsage(path string, dayStart time.Time) codexRollout {
	b, err := os.ReadFile(path)
	if err != nil {
		return codexRollout{}
	}
	var ro codexRollout
	for _, raw := range bytes.Split(b, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		switch {
		case bytes.Contains(line, []byte("turn_context")):
			var ev codexEvent
			if json.Unmarshal(line, &ev) != nil || ev.Type != "turn_context" {
				continue
			}
			var p struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil {
				ro.model = p.Model
			}
		case bytes.Contains(line, []byte("token_count")):
			var ev codexEvent
			if json.Unmarshal(line, &ev) != nil || ev.Type != "event_msg" {
				continue
			}
			var tc codexTokenCount
			if json.Unmarshal(ev.Payload, &tc) != nil || tc.Type != "token_count" ||
				tc.Info == nil || tc.Info.TotalTokenUsage == nil {
				continue
			}
			u := tc.Info.TotalTokenUsage
			snap := codexUsage{Input: u.InputTokens, Cached: u.CachedInputTokens, Output: u.OutputTokens, Total: u.TotalTokens}
			ro.last = &snap
			if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				ro.tsParsed = true
				if ts.Before(dayStart) {
					ro.before = &snap
				}
			}
		}
	}
	return ro
}

// sameDay reports whether an RFC 3339 timestamp falls on the given local day.
func sameDay(ts, day string) bool {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return t.Local().Format("2006-01-02") == day
}
