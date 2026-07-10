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
	"sort"
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

// CodexTotals sums today's new tokens and estimated cost across Codex
// sessions. root defaults to CODEX_HOME or ~/.codex. total_token_usage is
// cumulative per session, so each session contributes its growth since local
// midnight: the last cumulative snapshot minus the last snapshot before today
// (zero for sessions started today). Rollouts live in their session's
// START-day directory however long the session runs (#133, #188), so
// candidates come from file mtime, not the directory date: only a file
// modified today can hold today's growth.
func CodexTotals(root string, now time.Time, prices pricing.Table) (Totals, error) {
	var ok bool
	root, ok = agentpath.CodexRoot(root)
	if !ok {
		return Totals{}, errors.New("codex root could not be resolved")
	}
	local := now.Local()
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	sessions := filepath.Join(root, "sessions")
	todayDir := filepath.Join(sessions, local.Format("2006"), local.Format("01"), local.Format("02"))

	// Today's directory must be listable when it exists: it holds today's
	// sessions by default, so "can't list" means the total is unknown, not
	// zero (#180) — the tree walk below skips non-directory entries silently.
	if _, err := os.ReadDir(todayDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Totals{}, err
	}

	files, err := codexRolloutFiles(sessions)
	if err != nil {
		return Totals{}, err // unknown total; callers keep daily null, not 0
	}

	var out Totals
	// Dedup by session id (the UUID in the rollout filename) keeping the
	// largest cumulative on each side of midnight, so a session that resumed
	// into a second file isn't counted twice. Files without a parseable id
	// can't be deduped and are summed as-is.
	bySession := map[string]*codexRollout{}
	// addToday folds one today-modified rollout into the totals. Without any
	// parsable timestamp the cumulative can't be split at midnight, so it is
	// attributed to the file's own day directory (counted fully in today's
	// directory, ignored elsewhere).
	addToday := func(f codexRolloutFile, ro codexRollout) {
		if ro.last == nil || (!ro.tsParsed && f.day != todayDir) {
			return
		}
		id, ok := codex.SessionID(filepath.Base(f.path))
		if !ok {
			t := codexRolloutTotals(ro, prices)
			out.Tokens += t.Tokens
			out.Cost += t.Cost
			return
		}
		mergeCodexRollout(bySession, id, ro)
	}
	var old []codexRolloutFile
	for _, f := range files {
		if f.mod.Before(dayStart) {
			old = append(old, f)
			continue
		}
		ro, err := codexRolloutUsage(f.path, dayStart)
		if err != nil {
			return Totals{}, err // unknown total; callers keep daily null, not 0
		}
		addToday(f, ro)
	}
	// The classification mtime is from enumeration and these are live
	// sessions: re-stat each old file and count those appended past midnight
	// since as today files after all (their content, not their mtime,
	// decides the midnight split). This runs before base-linking so a
	// promoted session's own delta base isn't skipped by ordering.
	var bases []codexRolloutFile
	for _, f := range old {
		info, err := os.Stat(f.path)
		if err != nil {
			// Can't confirm an append: keep the enumeration-time "old"
			// classification instead of erroring, so long-dead junk (e.g. a
			// dangling symlink in an ancient day directory) doesn't null the
			// daily forever. If the file matters — it shares a session id
			// with a today rollout — the linking pass reads it and surfaces
			// the failure.
			bases = append(bases, f)
			continue
		}
		if info.ModTime().Before(dayStart) {
			bases = append(bases, f)
			continue
		}
		ro, err := codexRolloutUsage(f.path, dayStart)
		if err != nil {
			return Totals{}, err // unknown total; callers keep daily null, not 0
		}
		addToday(f, ro)
	}
	// A session resumed today carries its cumulative forward from an earlier
	// file last written before midnight; that file holds the delta base (the
	// last pre-midnight snapshot). Only files sharing a session id seen today
	// can contribute, so only those are read.
	for _, f := range bases {
		id, ok := codex.SessionID(filepath.Base(f.path))
		if !ok || bySession[id] == nil {
			continue
		}
		ro, err := codexRolloutUsage(f.path, dayStart)
		if err != nil {
			return Totals{}, err // unknown total; callers keep daily null, not 0
		}
		if ro.last == nil || !ro.tsParsed {
			continue
		}
		mergeCodexRollout(bySession, id, ro)
	}
	for _, ro := range bySession {
		t := codexRolloutTotals(*ro, prices)
		out.Tokens += t.Tokens
		out.Cost += t.Cost
	}
	return out, nil
}

// codexRolloutFile is one rollout candidate: its path, the day directory
// holding it, and its mtime.
type codexRolloutFile struct {
	path string
	day  string
	mod  time.Time
}

// codexRolloutFiles lists every rollout under sessions/YYYY/MM/DD with its
// mtime. Listing or stat failures make a day's contents unknown, so they are
// errors (callers keep daily null) rather than silent gaps (#187). Non-.jsonl
// and non-directory entries are skipped.
func codexRolloutFiles(sessions string) ([]codexRolloutFile, error) {
	years, err := os.ReadDir(sessions)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // no sessions yet: a real zero, not unknown
	}
	if err != nil {
		return nil, err
	}
	var out []codexRolloutFile
	for _, y := range years {
		if !y.IsDir() {
			continue
		}
		months, err := os.ReadDir(filepath.Join(sessions, y.Name()))
		if err != nil {
			return nil, err
		}
		for _, m := range months {
			if !m.IsDir() {
				continue
			}
			days, err := os.ReadDir(filepath.Join(sessions, y.Name(), m.Name()))
			if err != nil {
				return nil, err
			}
			for _, d := range days {
				if !d.IsDir() {
					continue
				}
				day := filepath.Join(sessions, y.Name(), m.Name(), d.Name())
				entries, err := os.ReadDir(day)
				if err != nil {
					return nil, err
				}
				for _, e := range entries {
					if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
						continue
					}
					info, err := e.Info()
					if err != nil {
						return nil, err
					}
					out = append(out, codexRolloutFile{filepath.Join(day, e.Name()), day, info.ModTime()})
				}
			}
		}
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
	// Order across files doesn't matter here: codexEventCost sorts by the
	// cumulative total, which restores emission order within a session.
	prev.events = append(prev.events, ro.events...)
}

// codexRolloutTotals turns one session's (or standalone rollout's) merged
// snapshots into today's totals. Cost is priced per token_count event when
// event timestamps were available, so a mid-session model switch charges
// each portion at the model that produced it (#191); timestamp-less rollouts
// fall back to pricing the whole delta at the last-seen model.
func codexRolloutTotals(ro codexRollout, prices pricing.Table) Totals {
	out := codexDeltaTotals(ro.model, ro.before, ro.last, prices)
	if len(ro.events) > 0 {
		out.Cost = codexEventCost(ro.events, ro.before, prices)
	}
	return out
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

// codexEventCost prices each today snapshot's growth at the model that was
// current when it was emitted, so a mid-session model switch doesn't reprice
// earlier turns at the newer model's rate (#191). base is the session's last
// cumulative before midnight (nil for sessions started today). Events may
// span resumed files; cumulative totals only grow within a session, so
// ordering by total restores the emission order across files. An event
// without a model (a resumed file's snapshot before its first turn_context)
// inherits the previous event's.
func codexEventCost(events []codexTC, base *codex.TokenUsage, prices pricing.Table) float64 {
	sort.Slice(events, func(i, j int) bool {
		return events[i].usage.TotalTokens < events[j].usage.TotalTokens
	})
	var prev codex.TokenUsage
	if base != nil {
		prev = *base
	}
	model := ""
	var cost float64
	for _, e := range events {
		if e.model != "" {
			model = e.model
		}
		in := clamp0(e.usage.InputTokens - prev.InputTokens)
		cached := clamp0(e.usage.CachedInputTokens - prev.CachedInputTokens)
		outTok := clamp0(e.usage.OutputTokens - prev.OutputTokens)
		if r, ok := prices.For(model); ok {
			cost += r.Cost(clamp0(in-cached), 0, cached, outTok)
		}
		if e.usage.TotalTokens >= prev.TotalTokens {
			prev = e.usage
		}
	}
	return cost
}

func clamp0(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// codexTC is one today token_count snapshot with the model that was current
// when it was emitted.
type codexTC struct {
	usage codex.TokenUsage
	model string
}

// codexRollout is what one rollout file contributes to today's totals: the
// last cumulative snapshot strictly before local midnight (nil when the file
// has none), the last snapshot overall, the session's model, and every
// today snapshot with its then-current model (for per-event pricing, #191).
type codexRollout struct {
	model    string
	before   *codex.TokenUsage
	last     *codex.TokenUsage
	tsParsed bool // at least one token_count carried a parsable timestamp
	events   []codexTC
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
				} else {
					// ro.model is the running last-seen turn_context, i.e.
					// the model this snapshot's growth was produced under.
					ro.events = append(ro.events, codexTC{usage: *ro.last, model: ro.model})
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
