// Package daily aggregates today's usage across all of a tool's sessions.
// It walks the same on-disk logs the collectors read, delegating log-format
// parsing to the collector packages and keeping only the date filtering and
// aggregation here.
package daily

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/collector/codex"
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
	seen := claude.UsageSet{}
	// session_today has no unknown-vs-zero contract (unlike daily, #187): an
	// unreadable transcript contributes nothing rather than nulling the value.
	out, _ := claudeFileTotals(transcriptPath, day, prices, seen)

	sessionDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	_ = filepath.WalkDir(sessionDir, func(path string, f os.DirEntry, err error) error {
		if err != nil || f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
			return nil
		}
		info, err := f.Info()
		if err != nil || info.ModTime().Local().Format("2006-01-02") != day {
			return nil
		}
		ft, err := claudeFileTotals(path, day, prices, seen)
		if err != nil {
			return nil
		}
		out.Tokens += ft.Tokens
		out.Cost += ft.Cost
		return nil
	})
	return out
}

// ClaudeTotals sums today's new tokens and estimated cost across every Claude
// transcript message under <root>/projects. root defaults to CLAUDE_CONFIG_DIR
// or ~/.claude.
func ClaudeTotals(root string, now time.Time, prices pricing.Table) (Totals, error) {
	var ok bool
	root, ok = agentpath.ClaudeRoot(root)
	if !ok {
		return Totals{}, errors.New("claude root could not be resolved")
	}
	day := now.Local().Format("2006-01-02")
	var out Totals
	// One dedup set spans every file so a response duplicated across files
	// (resume/compaction copies prior turns forward) is also counted once.
	seen := claude.UsageSet{}

	projects := filepath.Join(root, "projects")
	dirs, err := os.ReadDir(projects)
	if errors.Is(err, fs.ErrNotExist) {
		return Totals{}, nil // no sessions yet: a real zero, not unknown
	}
	if err != nil {
		return Totals{}, err // unknown total; callers keep daily null, not 0
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		projectDir := filepath.Join(projects, d.Name())
		err := filepath.WalkDir(projectDir, func(path string, f os.DirEntry, err error) error {
			if err != nil {
				return err // a listed entry we can't descend into: total is unknown
			}
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				return nil
			}
			info, err := f.Info()
			if err != nil {
				return err // can't check the mtime filter: total is unknown
			}
			if info.ModTime().Local().Format("2006-01-02") != day {
				return nil // only files touched today can hold today's messages
			}
			ft, err := claudeFileTotals(path, day, prices, seen)
			if err != nil {
				return err
			}
			out.Tokens += ft.Tokens
			out.Cost += ft.Cost
			return nil
		})
		if err != nil {
			return Totals{}, err // unknown total; callers keep daily null, not 0
		}
	}
	return out, nil
}

// claudeFileTotals sums one transcript's today entries into out, recording
// counted responses in seen so the caller can dedup across files. A read
// failure is an error: a transcript that was listed but can't be read means
// the day's total is unknown, not smaller.
func claudeFileTotals(path, day string, prices pricing.Table, seen claude.UsageSet) (Totals, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Totals{}, err
	}
	var out Totals
	claude.EachUsageLine(b, func(line claude.TranscriptLine) {
		if !sameDay(line.Timestamp, day) {
			return
		}
		if seen.Dup(line) {
			return
		}
		u := line.Message.Usage
		cacheWrite5m, cacheWrite1h, cacheWriteUnknown, cacheWriteTotal := u.CacheWrites()
		// New tokens exclude cache reads (same context re-read each message).
		out.Tokens += u.InputTokens + cacheWriteTotal + u.OutputTokens
		if r, ok := prices.For(line.Message.Model); ok {
			out.Cost += claudeAPICost(r, u.InputTokens, cacheWrite5m, cacheWrite1h, cacheWriteUnknown, u.CacheReadInputTokens, u.OutputTokens)
		}
	})
	return out, nil
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
func CodexTotals(root string, now time.Time, prices pricing.Table) (Totals, error) {
	var ok bool
	root, ok = agentpath.CodexRoot(root)
	if !ok {
		return Totals{}, errors.New("codex root could not be resolved")
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
		if errors.Is(err, fs.ErrNotExist) {
			continue // days without sessions have no directory
		}
		if err != nil {
			return Totals{}, err // unknown total; callers keep daily null, not 0
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			ro, err := codexRolloutUsage(filepath.Join(dayDir, e.Name()), dayStart)
			if err != nil {
				return Totals{}, err // unknown total; callers keep daily null, not 0
			}
			if ro.last == nil {
				continue
			}
			// Without any parsable timestamp the cumulative can't be split
			// at midnight; attribute it to the file's own day directory
			// (count fully in today's directory, ignore in older ones).
			if !ro.tsParsed && back != 0 {
				continue
			}
			id, ok := codex.SessionID(e.Name())
			if !ok {
				t := codexDeltaTotals(ro.model, ro.before, ro.last, prices)
				out.Tokens += t.Tokens
				out.Cost += t.Cost
				continue
			}
			mergeCodexRollout(bySession, id, ro)
		}
	}
	for _, ro := range bySession {
		t := codexDeltaTotals(ro.model, ro.before, ro.last, prices)
		out.Tokens += t.Tokens
		out.Cost += t.Cost
	}
	return out, nil
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
	if ro.before != nil && (prev.before == nil || ro.before.TotalTokens > prev.before.TotalTokens) {
		prev.before = ro.before
	}
	if ro.last.TotalTokens > prev.last.TotalTokens {
		prev.last, prev.model = ro.last, ro.model
	}
}

// codexDeltaTotals prices the growth from before (nil = session started
// today) to last. Tokens counts new tokens — cumulative total minus cached
// input — the same measure the whole-session accounting used.
func codexDeltaTotals(model string, before, last *codex.TokenUsage, prices pricing.Table) Totals {
	var b codex.TokenUsage
	if before != nil {
		b = *before
	}
	out := Totals{Tokens: clamp0((last.TotalTokens - last.CachedInputTokens) - (b.TotalTokens - b.CachedInputTokens))}
	if r, ok := prices.For(model); ok {
		in := clamp0(last.InputTokens - b.InputTokens)
		cached := clamp0(last.CachedInputTokens - b.CachedInputTokens)
		outTok := clamp0(last.OutputTokens - b.OutputTokens)
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

// codexRollout is what one rollout file contributes to today's totals: the
// last cumulative snapshot strictly before local midnight (nil when the file
// has none) and the last snapshot overall, plus the session's model.
type codexRollout struct {
	model    string
	before   *codex.TokenUsage
	last     *codex.TokenUsage
	tsParsed bool // at least one token_count carried a parsable timestamp
}

// codexRolloutUsage extracts one rollout's cumulative usage snapshots around
// dayStart. Events are appended in order, so a forward scan keeps the latest
// snapshot on each side of midnight and the latest turn_context model. A read
// failure is an error: a rollout that was listed but can't be read means the
// day's total is unknown, not smaller.
func codexRolloutUsage(path string, dayStart time.Time) (codexRollout, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return codexRollout{}, err
	}
	var ro codexRollout
	for _, raw := range bytes.Split(b, []byte("\n")) {
		// Cheap substring prefilter; the full envelope decode runs only for
		// candidate lines.
		switch {
		case bytes.Contains(raw, []byte("turn_context")):
			ev, ok := codex.ParseEvent(raw)
			if !ok {
				continue
			}
			if turn := ev.TurnContext(); turn != nil {
				ro.model = turn.Model
			}
		case bytes.Contains(raw, []byte("token_count")):
			ev, ok := codex.ParseEvent(raw)
			if !ok {
				continue
			}
			tc := ev.TokenCount()
			if tc == nil || tc.Info == nil || tc.Info.TotalTokenUsage == nil {
				continue
			}
			ro.last = tc.Info.TotalTokenUsage
			if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				ro.tsParsed = true
				if ts.Before(dayStart) {
					ro.before = ro.last
				}
			}
		}
	}
	return ro, nil
}

// sameDay reports whether an RFC 3339 timestamp falls on the given local day.
func sameDay(ts, day string) bool {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return t.Local().Format("2006-01-02") == day
}
