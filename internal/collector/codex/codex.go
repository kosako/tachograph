// Package codex collects status from Codex CLI session logs
// (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl), non-invasively.
package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/schema"
)

// tailBytes is how much of the session file is read from the end before
// falling back to a full scan. token_count / turn_context are emitted every
// turn, so the tail almost always contains both.
const tailBytes = 512 * 1024

// staleAfterMinutes is longer than the shared schema.StaleAfterMinutes (60).
// Codex has no live feed: freshness comes only from the last token_count in the
// log, which advances only when a turn completes. Its rate-limit windows (5h /
// weekly) stay valid for hours afterward, so greying the whole tool 60 min
// after the last turn would hide still-accurate limits. 5h matches the primary
// rate-limit window.
const staleAfterMinutes = 300

type Options struct {
	Root string    // Codex home, defaults to CODEX_HOME or ~/.codex
	Now  time.Time // defaults to time.Now()
}

func Collect(opts Options) schema.Tool {
	root, ok := agentpath.CodexRoot(opts.Root)
	if !ok {
		return errTool("home_dir", "cannot locate the home directory")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	res := pickSession(filepath.Join(root, "sessions"))
	if res.tc == nil {
		if res.readErr != nil {
			return errTool("read_error", res.readErr.Error())
		}
		if res.sawFile {
			return errTool("no_token_count", "no token_count event in recent Codex sessions")
		}
		return schema.Unavailable(schema.ToolCodex)
	}
	return build(res.path, res.tc, res.turn, now)
}

// pick is the freshest usable session found while walking the date tree.
type pick struct {
	tc      *TokenCount
	turn    *TurnContext
	path    string
	ts      time.Time
	sawFile bool  // any .jsonl was seen (to distinguish no_token_count vs unavailable)
	readErr error // last read error, surfaced only when no usable session was found
}

// extraDaysCompared is how many additional older day directories (that hold
// rollouts) are still compared by token_count timestamp after the newest
// usable day. Rollouts live in their session's START-day directory, so a
// session running past midnight keeps appending fresher token_counts to an
// older day's file; the newest day directory alone cannot be trusted (#133).
// Two extra days cover a session spanning a skipped-day gap (e.g. started
// Friday, still running Monday).
const extraDaysCompared = 2

// pickSession walks the YYYY/MM/DD tree newest-first and returns the rollout
// with the freshest token_count by event timestamp. Every Codex surface
// (interactive TUI, codex exec, desktop app, IDE extension) writes a rollout
// into its start-day directory; the freshest token_count — not the newest file
// by mtime, nor the newest day directory — is the current one (rate limits are
// account-global, so any session's latest token_count is valid). It backtracks
// past days whose rollouts all lack a token_count, and once one is found it
// compares extraDaysCompared more rollout-holding days so a cross-midnight
// session in an older directory can outrank a one-off run in today's.
func pickSession(dir string) pick {
	var acc pick
	extra := 0
	for _, day := range dayDirsDesc(dir) {
		p := pickFromDay(day)
		if acc.tc != nil && p.sawFile {
			extra++
		}
		acc.sawFile = acc.sawFile || p.sawFile
		if p.readErr != nil {
			acc.readErr = p.readErr
		}
		if p.tc != nil && (acc.tc == nil || p.ts.After(acc.ts)) {
			acc.tc, acc.turn, acc.path, acc.ts = p.tc, p.turn, p.path, p.ts
		}
		if acc.tc != nil && extra >= extraDaysCompared {
			break
		}
	}
	return acc
}

// dayDirsDesc lists the YYYY/MM/DD leaf directories under dir, newest first by
// name at each level.
func dayDirsDesc(dir string) []string {
	var days []string
	for _, y := range subdirsDesc(dir) {
		for _, m := range subdirsDesc(y) {
			days = append(days, subdirsDesc(m)...)
		}
	}
	return days
}

// subdirsDesc lists dir's immediate subdirectories in descending name order.
func subdirsDesc(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			out = append(out, filepath.Join(dir, entries[i].Name()))
		}
	}
	return out
}

// pickFromDay returns the rollout with the latest token_count directly inside
// day, or a token_count-less pick (sawFile set if any .jsonl existed) when none
// has one. A rollout with no token_count yet (just-started session) is skipped
// so it can't hide an older session's still-valid data.
func pickFromDay(day string) pick {
	files := jsonlFiles(day)
	out := pick{sawFile: len(files) > 0}
	for _, p := range files {
		tc, turn, err := sessionEvents(p)
		if err != nil {
			out.readErr = err
			continue
		}
		if tc == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, tc.timestamp)
		if err != nil {
			continue
		}
		if out.tc == nil || ts.After(out.ts) {
			out.tc, out.turn, out.path, out.ts = tc, turn, p, ts
		}
	}
	return out
}

// sessionEvents reads one rollout's most recent token_count and turn_context,
// reading the tail first and falling back to a full scan when the tail misses
// either. turn_context is emitted at turn start and token_count at the end, so a
// single turn longer than the tail can leave turn_context out of reach (model/cwd
// would silently go null) — the full scan recovers it.
func sessionEvents(path string) (tc *TokenCount, turn *TurnContext, err error) {
	lines, err := tailLines(path)
	if err != nil {
		return nil, nil, err
	}
	tc, turn, ok := lastEvents(lines)
	if !ok || turn == nil {
		if lines, err = allLines(path); err != nil {
			return nil, nil, err
		}
		tc, turn, _ = lastEvents(lines)
	}
	return tc, turn, nil
}

func errTool(code, msg string) schema.Tool {
	t := schema.Unavailable(schema.ToolCodex)
	t.Available = true
	t.Error = &schema.Error{Code: code, Message: msg}
	return t
}

// jsonlFiles returns the .jsonl rollout paths directly inside day (unordered),
// or nil when the directory holds none.
func jsonlFiles(day string) []string {
	entries, err := os.ReadDir(day)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		out = append(out, filepath.Join(day, e.Name()))
	}
	return out
}

func tailLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := st.Size() - tailBytes
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, err
	}
	lines := bytes.Split(buf, []byte("\n"))
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // first line is partial
	}
	return lines, nil
}

func allLines(path string) ([][]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return bytes.Split(b, []byte("\n")), nil
}

// lastEvents scans backwards for the most recent token_count and
// turn_context. ok reports whether a token_count was found.
func lastEvents(lines [][]byte) (tc *TokenCount, turn *TurnContext, ok bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		ev, evOK := ParseEvent(lines[i])
		if !evOK {
			continue
		}
		if tc == nil {
			tc = ev.TokenCount()
		}
		if turn == nil {
			turn = ev.TurnContext()
		}
		if tc != nil && turn != nil {
			break
		}
	}
	return tc, turn, tc != nil
}

func build(path string, tc *TokenCount, turn *TurnContext, now time.Time) schema.Tool {
	t := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Backend:   schema.BackendUnknown,
	}

	if ts, err := time.Parse(time.RFC3339Nano, tc.timestamp); err == nil {
		s := ts.Local().Format(time.RFC3339)
		t.CollectedAt = &s
		t.Stale = now.Sub(ts) > staleAfterMinutes*time.Minute
	}

	sess := &schema.Session{}
	if id, ok := SessionID(path); ok {
		sess.ID = &id
	}
	if turn != nil {
		if turn.CWD != "" {
			sess.CWD = &turn.CWD
		}
		if turn.Model != "" {
			t.Model = &schema.Model{ID: turn.Model}
		}
	}
	if info := tc.Info; info != nil {
		sess.ContextWindow = info.ModelContextWindow
		if u := info.TotalTokenUsage; u != nil {
			sess.Tokens = &schema.Tokens{
				Input:       u.InputTokens,
				CachedInput: u.CachedInputTokens,
				Output:      u.OutputTokens,
				Total:       u.TotalTokens,
			}
			t.Fallback = &schema.Fallback{SessionTokens: &u.TotalTokens}
		}
		// The last request's total (context sent + tokens generated)
		// approximates the current context size.
		if u := info.LastTokenUsage; u != nil && info.ModelContextWindow != nil && *info.ModelContextWindow > 0 {
			pct := float64(u.TotalTokens) / float64(*info.ModelContextWindow) * 100
			sess.ContextUsedPct = &pct
		}
	}
	t.Session = sess

	if rl := tc.RateLimits; rl != nil {
		t.Plan = rl.PlanType
		if rl.PlanType != nil {
			t.Backend = schema.BackendSubscription
		}
		if c, isFloat := rl.Credits.(float64); isFloat {
			t.Credits = &c
		}
		var limits []schema.Limit
		if rl.Primary != nil {
			limits = append(limits, toLimit(rl.Primary))
		}
		if rl.Secondary != nil {
			limits = append(limits, toLimit(rl.Secondary))
		}
		t.Limits = limits
	}
	return t
}

func toLimit(w *rlWindow) schema.Limit {
	mins := w.WindowMinutes
	pct := w.UsedPercent
	l := schema.Limit{
		Window:        windowName(mins),
		WindowMinutes: &mins,
		UsedPct:       &pct,
	}
	// resets_at is nullable: an absent/zero epoch must stay null, not format
	// as 1970-01-01 (which would render as a reset far in the past).
	if w.ResetsAt > 0 {
		resets := time.Unix(w.ResetsAt, 0).Local().Format(time.RFC3339)
		l.ResetsAt = &resets
	}
	return l
}

func windowName(mins int) string {
	switch mins {
	case 300:
		return schema.WindowFiveHour
	case 10080:
		return schema.WindowWeekly
	}
	if mins > 0 && mins%60 == 0 {
		return fmt.Sprintf("%dh", mins/60)
	}
	return fmt.Sprintf("%dm", mins)
}
