// Package codex collects status from Codex CLI session logs
// (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl), non-invasively.
package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

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
	Root string    // Codex home, defaults to ~/.codex
	Now  time.Time // defaults to time.Now()
}

func Collect(opts Options) schema.Tool {
	root := opts.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return errTool("home_dir", err.Error())
		}
		root = filepath.Join(home, ".codex")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	dir := newestSessionDir(filepath.Join(root, "sessions"))
	if dir == "" {
		return schema.Unavailable(schema.ToolCodex)
	}

	// Every Codex surface (interactive TUI, codex exec, desktop app, IDE
	// extension) writes a rollout into the same day directory. Pick the session
	// whose last token_count is the most recent — not the newest file by mtime,
	// which can be a just-started session with no token_count yet (it would trip
	// no_token_count or hide an older session's still-valid data). Rate limits
	// are account-global, so the latest token_count from any session is current.
	var (
		best     *tokenCount
		bestTurn *turnContext
		bestPath string
		bestTS   time.Time
		readErr  error
	)
	for _, p := range jsonlFiles(dir) {
		tc, turn, err := sessionEvents(p)
		if err != nil {
			readErr = err
			continue
		}
		if tc == nil {
			continue // a session with no token_count yet
		}
		ts, err := time.Parse(time.RFC3339Nano, tc.timestamp)
		if err != nil {
			continue
		}
		if best == nil || ts.After(bestTS) {
			best, bestTurn, bestPath, bestTS = tc, turn, p, ts
		}
	}
	if best == nil {
		if readErr != nil {
			return errTool("read_error", readErr.Error())
		}
		return errTool("no_token_count", "no token_count event found under "+dir)
	}
	return build(bestPath, best, bestTurn, now)
}

// sessionEvents reads one rollout's most recent token_count and turn_context,
// reading the tail first and falling back to a full scan when the tail misses
// either. turn_context is emitted at turn start and token_count at the end, so a
// single turn longer than the tail can leave turn_context out of reach (model/cwd
// would silently go null) — the full scan recovers it.
func sessionEvents(path string) (tc *tokenCount, turn *turnContext, err error) {
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

// newestSessionDir prunes by the YYYY/MM/DD layout: it walks date directories
// in descending name order and returns the first day directory that holds any
// .jsonl, backtracking past empty branches (e.g. a freshly-rolled-over but
// still-empty newest day/month/year). The caller then compares the day's
// rollouts by their last token_count, so this returns the directory, not a file.
func newestSessionDir(sessions string) string {
	return descendForDay(sessions, 3) // year / month / day
}

// descendForDay walks the date tree in descending name order with backtracking:
// it tries the newest subdirectory first and falls back to older siblings
// whenever a branch yields no .jsonl, so an empty newest day/month/year doesn't
// hide real sessions in an older one. depth is the remaining directory levels.
func descendForDay(dir string, depth int) string {
	if depth == 0 {
		if len(jsonlFiles(dir)) > 0 {
			return dir
		}
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].IsDir() {
			continue
		}
		if found := descendForDay(filepath.Join(dir, entries[i].Name()), depth-1); found != "" {
			return found
		}
	}
	return ""
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

type event struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type tokenCount struct {
	timestamp string
	Type      string `json:"type"`
	Info      *struct {
		TotalTokenUsage    *tokenUsage `json:"total_token_usage"`
		LastTokenUsage     *tokenUsage `json:"last_token_usage"`
		ModelContextWindow *int64      `json:"model_context_window"`
	} `json:"info"`
	RateLimits *struct {
		Primary   *rlWindow `json:"primary"`
		Secondary *rlWindow `json:"secondary"`
		Credits   any       `json:"credits"`
		PlanType  *string   `json:"plan_type"`
	} `json:"rate_limits"`
}

type tokenUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
}

type rlWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"` // epoch seconds
}

type turnContext struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

// lastEvents scans backwards for the most recent token_count and
// turn_context. ok reports whether a token_count was found.
func lastEvents(lines [][]byte) (tc *tokenCount, turn *turnContext, ok bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var ev event
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "event_msg":
			if tc != nil {
				continue
			}
			var p tokenCount
			if json.Unmarshal(ev.Payload, &p) == nil && p.Type == "token_count" {
				p.timestamp = ev.Timestamp
				tc = &p
			}
		case "turn_context":
			if turn != nil {
				continue
			}
			var p turnContext
			if json.Unmarshal(ev.Payload, &p) == nil {
				turn = &p
			}
		}
		if tc != nil && turn != nil {
			break
		}
	}
	return tc, turn, tc != nil
}

var sessionIDRe = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

func build(path string, tc *tokenCount, turn *turnContext, now time.Time) schema.Tool {
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
	if m := sessionIDRe.FindStringSubmatch(path); m != nil {
		sess.ID = &m[1]
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
