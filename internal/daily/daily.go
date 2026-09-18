// Package daily aggregates usage per local calendar day across all of a
// tool's sessions. It walks the same on-disk logs the collectors read,
// delegating log-format parsing to the collector packages and keeping only
// the date filtering and aggregation here. Nothing is stored: every call
// recomputes from the logs, so a day only ever reflects what is still on
// disk (#242).
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

// Totals is today's aggregate for one tool. Tokens is the billing volume —
// input (cache writes and cache reads included) plus output, the same measure
// as session.tokens.total — so it shares a denominator with Cost (#234).
type Totals struct {
	Tokens      int64 // Input + Output as the provider reports it
	Input       int64 // incl. cache writes and cache reads
	CachedInput int64 // cache reads within Input
	Output      int64
	Cost        float64
}

func (t *Totals) add(o Totals) {
	t.Tokens += o.Tokens
	t.Input += o.Input
	t.CachedInput += o.CachedInput
	t.Output += o.Output
	t.Cost += o.Cost
}

// Schema packs Totals into the wire type. Cost is set only when non-zero (a
// priced model was seen), so an unpriced model reads as "tokens known, cost
// unknown" rather than "$0.00".
func (t Totals) Schema() *schema.Daily {
	d := &schema.Daily{Tokens: t.Tokens, Input: t.Input, CachedInput: t.CachedInput, Output: t.Output}
	if t.Cost > 0 {
		c := t.Cost
		d.CostUSD = &c
	}
	return d
}

// DayStart returns the local midnight that begins t's calendar day. Day
// windows passed to ClaudeDays / CodexDays are built from it, so a window
// boundary is always a local midnight (DST-safe: time.Date normalizes the
// 23h / 25h days).
func DayStart(t time.Time) time.Time {
	l := t.Local()
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, l.Location())
}

// DayKey is the key ClaudeDays / CodexDays use for t's local calendar day.
func DayKey(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

// ClaudeSessionToday totals today's tokens and estimated cost for one
// Claude session transcript plus its nested subagent/workflow transcripts
// (used for the {tool.*.session.today} scope). It reuses the same per-message
// accounting as ClaudeTotals with a fresh dedup set for that session tree.
func ClaudeSessionToday(transcriptPath string, now time.Time, prices pricing.Table) Totals {
	if transcriptPath == "" {
		return Totals{}
	}
	from := DayStart(now)
	to := from.AddDate(0, 0, 1)
	seen := claude.UsageSet{}
	days := map[string]Totals{}
	// session_today has no unknown-vs-zero contract (unlike daily, #187): an
	// unreadable transcript contributes nothing rather than nulling the value.
	_ = claudeFileDays(transcriptPath, from, to, prices, seen, days)

	sessionDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	_ = filepath.WalkDir(sessionDir, func(path string, f os.DirEntry, err error) error {
		if err != nil || f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
			return nil
		}
		info, err := f.Info()
		if err != nil || info.ModTime().Before(from) {
			return nil
		}
		_ = claudeFileDays(path, from, to, prices, seen, days)
		return nil
	})
	return days[DayKey(now)]
}

// ClaudeTotals sums today's tokens and estimated cost across every Claude
// transcript message under <root>/projects: the single-day case of
// ClaudeDays, so today's figure is the same whichever entry point asks.
func ClaudeTotals(root string, now time.Time, prices pricing.Table) (Totals, error) {
	from := DayStart(now)
	days, err := ClaudeDays(root, from, from.AddDate(0, 0, 1), prices)
	if err != nil {
		return Totals{}, err
	}
	return days[DayKey(now)], nil
}

// ClaudeDays sums tokens and estimated cost per local calendar day across
// every Claude transcript message under <root>/projects, for the days in
// [from, to) (local midnights, see DayStart). Days without usage are absent
// from the result. root defaults to CLAUDE_CONFIG_DIR or ~/.claude.
func ClaudeDays(root string, from, to time.Time, prices pricing.Table) (map[string]Totals, error) {
	var ok bool
	root, ok = agentpath.ClaudeRoot(root)
	if !ok {
		return nil, errors.New("claude root could not be resolved")
	}
	out := map[string]Totals{}
	// One dedup set spans every file so a response duplicated across files
	// (resume/compaction copies prior turns forward) is also counted once.
	seen := claude.UsageSet{}

	projects := filepath.Join(root, "projects")
	dirs, err := os.ReadDir(projects)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil // no sessions yet: a real zero, not unknown
	}
	if err != nil {
		return nil, err // unknown totals; callers keep daily null, not 0
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		projectDir := filepath.Join(projects, d.Name())
		err := filepath.WalkDir(projectDir, func(path string, f os.DirEntry, err error) error {
			if err != nil {
				return err // a listed entry we can't descend into: totals are unknown
			}
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				return nil
			}
			info, err := f.Info()
			if err != nil {
				return err // can't check the mtime filter: totals are unknown
			}
			if info.ModTime().Before(from) {
				return nil // a file last written before the window can't hold its messages
			}
			return claudeFileDays(path, from, to, prices, seen, out)
		})
		if err != nil {
			return nil, err // unknown totals; callers keep daily null, not 0
		}
	}
	return out, nil
}

// claudeFileDays sums one transcript's entries within [from, to) into days,
// keyed by local calendar day, recording counted responses in seen so the
// caller can dedup across files. A read failure is an error: a transcript
// that was listed but can't be read means the totals are unknown, not
// smaller.
func claudeFileDays(path string, from, to time.Time, prices pricing.Table, seen claude.UsageSet, days map[string]Totals) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	claude.EachUsageLine(b, func(line claude.TranscriptLine) {
		day, ok := dayIn(line.Timestamp, from, to)
		if !ok || seen.Dup(line) {
			return
		}
		u := line.Message.Usage
		cacheWrite5m, cacheWrite1h, cacheWriteUnknown, cacheWriteTotal := u.CacheWrites()
		// Same convention as session.tokens: input is everything sent,
		// cache reads included, so tokens and cost share a denominator.
		in := u.InputTokens + cacheWriteTotal + u.CacheReadInputTokens
		var t Totals
		t.Input = in
		t.CachedInput = u.CacheReadInputTokens
		t.Output = u.OutputTokens
		t.Tokens = in + u.OutputTokens
		if r, ok := prices.For(line.Message.Model); ok {
			t.Cost = claudeAPICost(r, u.InputTokens, cacheWrite5m, cacheWrite1h, cacheWriteUnknown, u.CacheReadInputTokens, u.OutputTokens)
		}
		acc := days[day]
		acc.add(t)
		days[day] = acc
	})
	return nil
}

func claudeAPICost(r pricing.Rate, in, cacheWrite5m, cacheWrite1h, cacheWriteUnknown, cacheRead, out int64) float64 {
	return (float64(in)*r.In +
		float64(cacheWrite5m+cacheWriteUnknown)*r.CacheWrite +
		float64(cacheWrite1h)*r.In*2 +
		float64(cacheRead)*r.CacheRead +
		float64(out)*r.Out) / 1_000_000
}

// CodexTotals sums today's tokens and estimated cost across Codex sessions:
// the single-day case of CodexDays, so today's figure is the same whichever
// entry point asks.
func CodexTotals(root string, now time.Time, prices pricing.Table) (Totals, error) {
	from := DayStart(now)
	days, err := CodexDays(root, from, from.AddDate(0, 0, 1), prices)
	if err != nil {
		return Totals{}, err
	}
	return days[DayKey(now)], nil
}

// CodexDays sums tokens and estimated cost per local calendar day across
// Codex sessions, for the days in [from, to) (local midnights, see
// DayStart). Days without usage are absent from the result. root defaults to
// CODEX_HOME or ~/.codex. total_token_usage is cumulative per session, so
// each session contributes its growth within each day: the last cumulative
// snapshot of the day minus the last snapshot before it (zero for sessions
// started that day). Rollouts live in their session's START-day directory
// however long the session runs (#133, #188), so candidates come from file
// mtime, not the directory date: only a file modified since the window
// opened can hold growth inside it.
func CodexDays(root string, from, to time.Time, prices pricing.Table) (map[string]Totals, error) {
	var ok bool
	root, ok = agentpath.CodexRoot(root)
	if !ok {
		return nil, errors.New("codex root could not be resolved")
	}
	sessions := filepath.Join(root, "sessions")

	// Each day directory in the window must be listable when it exists: it
	// holds that day's sessions by default, so "can't list" means the totals
	// are unknown, not zero (#180) — the tree walk below skips non-directory
	// entries silently. The map also places timestamp-less rollouts on their
	// own directory's day.
	dayDirs := map[string]time.Time{}
	window := map[string]bool{} // day keys inside [from, to)
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		dir := filepath.Join(sessions, d.Format("2006"), d.Format("01"), d.Format("02"))
		if _, err := os.ReadDir(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		dayDirs[dir] = d
		window[DayKey(d)] = true
	}

	files, err := codexRolloutFiles(sessions)
	if err != nil {
		return nil, err // unknown totals; callers keep daily null, not 0
	}

	// Dedup by session id (the UUID in the rollout filename) so a session
	// that resumed into a second file isn't counted twice. Files without a
	// parseable id can't be deduped and are summed as-is (keyed by path).
	bySession := map[string]*codexRollout{}
	// addRollout folds one window-modified rollout into its session. Without
	// any parsable timestamp the cumulative can't be split across days, so it
	// is attributed to the file's own day directory — as a snapshot at the
	// end of that day — and ignored when the directory is outside the window.
	addRollout := func(f codexRolloutFile, ro codexRollout) {
		if ro.last == nil {
			return
		}
		if !ro.tsParsed {
			day, ok := dayDirs[f.day]
			if !ok {
				return
			}
			ro.events = []codexTC{{ts: day.AddDate(0, 0, 1).Add(-time.Nanosecond), usage: *ro.last, model: ro.model}}
		}
		key, ok := codex.SessionID(filepath.Base(f.path))
		if !ok {
			key = f.path
		}
		mergeCodexRollout(bySession, key, ro)
	}
	var old []codexRolloutFile
	for _, f := range files {
		if f.mod.Before(from) {
			old = append(old, f)
			continue
		}
		ro, err := codexRolloutUsage(f.path, from)
		if err != nil {
			return nil, err // unknown totals; callers keep daily null, not 0
		}
		addRollout(f, ro)
	}
	// The classification mtime is from enumeration and these are live
	// sessions: re-stat each old file and count those appended since the
	// window opened as candidates after all (their content, not their mtime,
	// decides the day split). This runs before base-linking so a promoted
	// session's own delta base isn't skipped by ordering.
	var bases []codexRolloutFile
	for _, f := range old {
		info, err := os.Stat(f.path)
		if err != nil {
			// Can't confirm an append: keep the enumeration-time "old"
			// classification instead of erroring, so long-dead junk (e.g. a
			// dangling symlink in an ancient day directory) doesn't null the
			// daily forever. If the file matters — it shares a session id
			// with a candidate rollout — the linking pass reads it and
			// surfaces the failure.
			bases = append(bases, f)
			continue
		}
		if info.ModTime().Before(from) {
			bases = append(bases, f)
			continue
		}
		ro, err := codexRolloutUsage(f.path, from)
		if err != nil {
			return nil, err // unknown totals; callers keep daily null, not 0
		}
		addRollout(f, ro)
	}
	// A session resumed inside the window carries its cumulative forward
	// from an earlier file last written before the window opened; that file
	// holds the delta base (the last pre-window snapshot). Only files sharing
	// a session id seen in the window can contribute, so only those are read.
	for _, f := range bases {
		id, ok := codex.SessionID(filepath.Base(f.path))
		if !ok || bySession[id] == nil {
			continue
		}
		ro, err := codexRolloutUsage(f.path, from)
		if err != nil {
			return nil, err // unknown totals; callers keep daily null, not 0
		}
		if ro.last == nil || !ro.tsParsed {
			continue
		}
		mergeCodexRollout(bySession, id, ro)
	}

	out := map[string]Totals{}
	for _, ro := range bySession {
		for day, dayRo := range splitCodexRolloutByDay(*ro) {
			if !window[day] {
				continue // snapshots past the window's end
			}
			acc := out[day]
			acc.add(codexRolloutTotals(dayRo, prices))
			out[day] = acc
		}
	}
	return out, nil
}

// splitCodexRolloutByDay slices one session's merged snapshots into a
// rollout per local calendar day, so each day prices exactly like the
// single-day computation: a day's delta base is the largest cumulative seen
// before it (the pre-window base, then earlier days' snapshots), its last is
// the largest snapshot within the day, and its events are the day's
// snapshots. Snapshots are walked in emission order so a base carries the
// model current at that point (#191). Days without a snapshot are absent.
func splitCodexRolloutByDay(ro codexRollout) map[string]codexRollout {
	events := append([]codexTC(nil), ro.events...)
	sortCodexTC(events)
	out := map[string]codexRollout{}
	base, baseModel := ro.before, ro.beforeModel
	model := ro.beforeModel // running last-seen turn_context model
	for i := 0; i < len(events); {
		day := DayKey(events[i].ts)
		j := i
		for j < len(events) && DayKey(events[j].ts) == day {
			j++
		}
		var last *codex.TokenUsage
		var lastModel string
		for _, e := range events[i:j] {
			if e.model != "" {
				model = e.model
			}
			if last == nil || e.usage.TotalTokens > last.TotalTokens {
				u := e.usage
				last, lastModel = &u, model
			}
		}
		out[day] = codexRollout{model: model, before: base, beforeModel: baseModel, last: last, tsParsed: true, events: events[i:j]}
		if base == nil || last.TotalTokens > base.TotalTokens {
			base, baseModel = last, lastModel
		}
		i = j
	}
	return out
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
		prev.before, prev.beforeModel = ro.before, ro.beforeModel
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
		out.Cost = codexEventCost(ro.events, ro.before, ro.beforeModel, prices)
	}
	return out
}

// codexDeltaTotals prices the growth from before (nil = session started
// today) to last. Tokens is the growth of the cumulative total as Codex
// reports it (cached input included), matching session.tokens.total.
func codexDeltaTotals(model string, before, last *codex.TokenUsage, prices pricing.Table) Totals {
	var b codex.TokenUsage
	if before != nil {
		b = *before
	}
	in := clamp0(last.InputTokens - b.InputTokens)
	cached := clamp0(last.CachedInputTokens - b.CachedInputTokens)
	outTok := clamp0(last.OutputTokens - b.OutputTokens)
	out := Totals{
		Tokens:      clamp0(last.TotalTokens - b.TotalTokens),
		Input:       in,
		CachedInput: cached,
		Output:      outTok,
	}
	if r, ok := prices.For(model); ok {
		out.Cost = r.Cost(clamp0(in-cached), 0, cached, outTok)
	}
	return out
}

// codexEventCost prices each today snapshot's growth at the model that was
// current when it was emitted, so a mid-session model switch doesn't reprice
// earlier turns at the newer model's rate (#191). base is the session's last
// cumulative before midnight (nil for sessions started today) and baseModel
// the model current at that point, inherited by events preceding their
// file's first turn_context (a freshly resumed file can emit a token_count
// first). Events may span resumed files; sorting by timestamp (total as the
// tie-break for equal times) restores the emission order across files.
//
// Deltas are accumulated per model with their sign and clamped once per
// model, so a single-model day prices exactly like the endpoint computation
// in codexDeltaTotals even when a cumulative component dips mid-day.
func codexEventCost(events []codexTC, base *codex.TokenUsage, baseModel string, prices pricing.Table) float64 {
	sortCodexTC(events)
	var prev codex.TokenUsage
	if base != nil {
		prev = *base
	}
	type sums struct{ in, cached, out int64 }
	byModel := map[string]*sums{}
	var order []string // deterministic summation order (first appearance)
	model := baseModel
	for _, e := range events {
		if e.model != "" {
			model = e.model
		}
		s := byModel[model]
		if s == nil {
			s = &sums{}
			byModel[model] = s
			order = append(order, model)
		}
		s.in += e.usage.InputTokens - prev.InputTokens
		s.cached += e.usage.CachedInputTokens - prev.CachedInputTokens
		s.out += e.usage.OutputTokens - prev.OutputTokens
		prev = e.usage
	}
	var cost float64
	for _, m := range order {
		r, ok := prices.For(m)
		if !ok {
			continue
		}
		s := byModel[m]
		in, cached, outTok := clamp0(s.in), clamp0(s.cached), clamp0(s.out)
		cost += r.Cost(clamp0(in-cached), 0, cached, outTok)
	}
	return cost
}

// sortCodexTC restores emission order across resumed files: by timestamp,
// with the cumulative total as the tie-break for equal times.
func sortCodexTC(events []codexTC) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].ts.Equal(events[j].ts) {
			return events[i].usage.TotalTokens < events[j].usage.TotalTokens
		}
		return events[i].ts.Before(events[j].ts)
	})
}

func clamp0(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// codexTC is one today token_count snapshot with its emission time and the
// model that was current when it was emitted.
type codexTC struct {
	ts    time.Time
	usage codex.TokenUsage
	model string
}

// codexRollout is what one rollout file contributes to today's totals: the
// last cumulative snapshot strictly before local midnight (nil when the file
// has none), the last snapshot overall, the session's model, and every
// today snapshot with its then-current model (for per-event pricing, #191).
type codexRollout struct {
	model       string
	before      *codex.TokenUsage
	beforeModel string // model current at the before snapshot
	last        *codex.TokenUsage
	tsParsed    bool // at least one token_count carried a parsable timestamp
	events      []codexTC
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
					ro.before, ro.beforeModel = ro.last, ro.model
				} else {
					// ro.model is the running last-seen turn_context, i.e.
					// the model this snapshot's growth was produced under.
					ro.events = append(ro.events, codexTC{ts: ts, usage: *ro.last, model: ro.model})
				}
			}
		}
	}
	return ro, nil
}

// dayIn reports whether an RFC 3339 timestamp falls inside [from, to) and,
// if so, which local calendar day it belongs to. An unparsable timestamp is
// outside every window.
func dayIn(ts string, from, to time.Time) (string, bool) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil || t.Before(from) || !t.Before(to) {
		return "", false
	}
	return DayKey(t), true
}
