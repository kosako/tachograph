// Package codex collects status from Codex CLI session logs
// (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl), non-invasively.
package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	return build(res.path, res.tc, res.lim, res.turn, now)
}

// pick is the freshest usable session found while walking the date tree,
// plus the freshest token_count carrying the account's rate limits (lim),
// which can sit in an older rollout than the session (#258).
type pick struct {
	tc      *TokenCount
	turn    *TurnContext
	path    string
	ts      time.Time
	lim     *TokenCount
	limTS   time.Time
	sawFile bool  // any .jsonl was seen (to distinguish no_token_count vs unavailable)
	readErr error // last read error, surfaced only when no usable session was found
}

// pickSession returns the rollout with the freshest token_count by event
// timestamp across every day directory. Every Codex surface (interactive
// TUI, codex exec, desktop app, IDE extension) writes a rollout into its
// START-day directory and keeps appending there however long the session
// runs (#133, #188), so recency comes from file mtime, not the directory
// date. Candidates are visited newest-mtime-first; once a token_count is
// found, files whose mtime predates it are skipped without reading —
// appends move mtime forward, so a file's events are never newer than its
// mtime and such a file cannot win. The skip re-stats at decision time
// because these are live sessions: an append landing after enumeration
// moves the file's current mtime past the cutoff and it is read after all.
// The freshest token_count — not the newest file by mtime — is still what
// decides (rate limits are account-global, so any session's latest
// token_count is valid); a rollout without a token_count yet (just-started
// session) is skipped so it can't hide older valid data. The account's rate
// limits are tracked the same way but separately: the freshest session may
// hold only non-account buckets (#258), so the walk continues until a file
// can improve neither the session nor the limits. Limits are only taken
// from within staleAfterMinutes before the session's token_count — older
// ones would make a fresh session render stale — so the walk never looks
// back past that window, and sessions that never carry limits (API-key use)
// don't make every rollout get read.
func pickSession(dir string) pick {
	files := rolloutsByMtime(dir)
	acc := pick{sawFile: len(files) > 0}
	for _, f := range files {
		if acc.tc != nil {
			info, err := os.Stat(f.path)
			if err != nil {
				acc.readErr = err
				continue
			}
			limCutoff := limitsFloor(acc.ts)
			if acc.lim != nil && acc.limTS.After(limCutoff) {
				limCutoff = acc.limTS
			}
			if info.ModTime().Before(acc.ts) && info.ModTime().Before(limCutoff) {
				continue
			}
		}
		tc, lim, turn, err := sessionEvents(f.path)
		if err != nil {
			acc.readErr = err
			continue
		}
		if tc != nil {
			if ts, err := time.Parse(time.RFC3339Nano, tc.timestamp); err == nil &&
				(acc.tc == nil || ts.After(acc.ts)) {
				acc.tc, acc.turn, acc.path, acc.ts = tc, turn, f.path, ts
			}
		}
		if lim != nil {
			if ts, err := time.Parse(time.RFC3339Nano, lim.timestamp); err == nil &&
				(acc.lim == nil || ts.After(acc.limTS)) {
				acc.lim, acc.limTS = lim, ts
			}
		}
	}
	// Judged against the final session: a fresher session found later in the
	// walk can push earlier-accepted limits out of the window.
	if acc.tc != nil && acc.lim != nil && acc.limTS.Before(limitsFloor(acc.ts)) {
		acc.lim = nil
	}
	return acc
}

// limitsFloor is the oldest account-limits timestamp still used alongside a
// session whose token_count is at sessionTS.
func limitsFloor(sessionTS time.Time) time.Time {
	return sessionTS.Add(-staleAfterMinutes * time.Minute)
}

// rolloutFile is one .jsonl candidate with its modification time.
type rolloutFile struct {
	path string
	mod  time.Time
}

// rolloutsByMtime lists every rollout under the YYYY/MM/DD tree, newest
// mtime first. Equal mtimes tie-break by path (descending) so selection is
// deterministic rather than dependent on directory read order. An entry
// whose stat fails stays listed with a zero mtime (sorting last): the mtime
// only orders the visit, and the caller's stat/read surfaces the error so a
// listed-but-unreadable rollout reads as read_error, not as absent.
func rolloutsByMtime(dir string) []rolloutFile {
	var out []rolloutFile
	for _, day := range dayDirsDesc(dir) {
		entries, err := os.ReadDir(day)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			var mod time.Time
			if info, err := e.Info(); err == nil {
				mod = info.ModTime()
			}
			out = append(out, rolloutFile{filepath.Join(day, e.Name()), mod})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].mod.Equal(out[j].mod) {
			return out[i].path > out[j].path
		}
		return out[i].mod.After(out[j].mod)
	})
	return out
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

// sessionEvents reads one rollout's most recent usable token_count, most
// recent token_count carrying the account's rate limits, and turn_context,
// reading the tail first and falling back to a full scan when the tail misses
// the token_count or turn_context. turn_context is emitted at turn start and
// token_count at the end, so a single turn longer than the tail can leave
// turn_context out of reach (model/cwd would silently go null) — the full scan
// recovers it. A missing lim alone doesn't trigger the full scan: the codex
// bucket is written just before the other buckets of the same turn, and
// sessions without limits (API-key use) would otherwise be read in full on
// every call; pickSession looks for limits in other rollouts instead.
func sessionEvents(path string) (tc, lim *TokenCount, turn *TurnContext, err error) {
	lines, err := tailLines(path)
	if err != nil {
		return nil, nil, nil, err
	}
	tc, lim, turn = lastEvents(lines)
	if tc == nil || turn == nil {
		if lines, err = allLines(path); err != nil {
			return nil, nil, nil, err
		}
		tc, lim, turn = lastEvents(lines)
	}
	return tc, lim, turn, nil
}

func errTool(code, msg string) schema.Tool {
	t := schema.Unavailable(schema.ToolCodex)
	t.Available = true
	t.Error = &schema.Error{Code: code, Message: msg}
	return t
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

// lastEvents scans backwards for the most recent usable token_count, the most
// recent token_count carrying the account's rate limits, and turn_context. An
// empty token_count (a usage-limit-refused request, #205) is skipped so it
// can't hide the session's — or an older session's — last valid data; a
// non-account bucket's token_count (#258) can supply session usage but never
// the limits. Once the token_count and turn_context are found, the search for
// limits stops at limitsFloor: older limits would be discarded anyway, and a
// session without limits (API-key use) must not be parsed back to its start.
func lastEvents(lines [][]byte) (tc, lim *TokenCount, turn *TurnContext) {
	var floor time.Time
	for i := len(lines) - 1; i >= 0; i-- {
		ev, evOK := ParseEvent(lines[i])
		if !evOK {
			continue
		}
		if tc != nil && turn != nil && !floor.IsZero() {
			if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil && ts.Before(floor) {
				break
			}
		}
		if c := ev.TokenCount(); c != nil {
			if tc == nil && c.Usable() {
				tc = c
				if ts, err := time.Parse(time.RFC3339Nano, c.timestamp); err == nil {
					floor = limitsFloor(ts)
				}
			}
			if lim == nil && c.AccountLimits() {
				lim = c
			}
		}
		if turn == nil {
			turn = ev.TurnContext()
		}
		if tc != nil && lim != nil && turn != nil {
			break
		}
	}
	return tc, lim, turn
}

// build assembles the tool from the session's token_count (tc) and the
// account's rate limits (lim, nil when none was found). The row is as old as
// its older source, so collected_at and stale follow the older timestamp.
func build(path string, tc, lim *TokenCount, turn *TurnContext, now time.Time) schema.Tool {
	t := schema.Tool{
		Tool:      schema.ToolCodex,
		Available: true,
		Backend:   schema.BackendUnknown,
	}

	if ts, err := time.Parse(time.RFC3339Nano, tc.timestamp); err == nil {
		if lim != nil {
			if lts, err := time.Parse(time.RFC3339Nano, lim.timestamp); err == nil && lts.Before(ts) {
				ts = lts
			}
		}
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

	if lim == nil {
		return t
	}
	if rl := lim.RateLimits; rl != nil {
		t.Plan = rl.PlanType
		if rl.PlanType != nil {
			t.Backend = schema.BackendSubscription
		}
		if c, ok := creditsBalance(rl.Credits); ok {
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
