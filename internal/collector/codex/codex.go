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

	path := latestSessionFile(filepath.Join(root, "sessions"))
	if path == "" {
		return schema.Unavailable(schema.ToolCodex)
	}
	lines, err := tailLines(path)
	if err != nil {
		return errTool("read_error", err.Error())
	}
	tc, turn, ok := lastEvents(lines)
	if !ok {
		// Tail missed the events (very long turns); scan the whole file.
		if lines, err = allLines(path); err != nil {
			return errTool("read_error", err.Error())
		}
		tc, turn, _ = lastEvents(lines)
	}
	if tc == nil {
		return errTool("no_token_count", "no token_count event found in "+path)
	}
	return build(path, tc, turn, now)
}

func errTool(code, msg string) schema.Tool {
	t := schema.Unavailable(schema.ToolCodex)
	t.Available = true
	t.Error = &schema.Error{Code: code, Message: msg}
	return t
}

// latestSessionFile prunes by the YYYY/MM/DD layout: it walks date
// directories in descending name order and returns the newest *.jsonl by
// mtime within the first day that has any.
func latestSessionFile(sessions string) string {
	day := latestDir(latestDir(latestDir(sessions)))
	if day == "" {
		return ""
	}
	var best string
	var bestMod time.Time
	entries, err := os.ReadDir(day)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMod) {
			best, bestMod = filepath.Join(day, e.Name()), info.ModTime()
		}
	}
	return best
}

// latestDir returns the lexicographically last subdirectory (dates sort
// naturally in YYYY/MM/DD layout), skipping empty branches.
func latestDir(dir string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			return filepath.Join(dir, entries[i].Name())
		}
	}
	return ""
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
		t.Stale = now.Sub(ts) > schema.StaleAfterMinutes*time.Minute
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
	resets := time.Unix(w.ResetsAt, 0).Local().Format(time.RFC3339)
	return schema.Limit{
		Window:        windowName(mins),
		WindowMinutes: &mins,
		UsedPct:       &pct,
		ResetsAt:      &resets,
	}
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
