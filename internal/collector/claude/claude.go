// Package claude collects status from Claude Code, via two routes:
//
//  1. the statusline stdin JSON (richest: model, context %, rate limits)
//  2. transcript files under ~/.claude/projects (model + session tokens only)
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/schema"
)

type Options struct {
	Root            string    // Claude home, defaults to CLAUDE_CONFIG_DIR or ~/.claude
	Now             time.Time // defaults to time.Now()
	StatuslineInput []byte    // raw stdin JSON; when set it is the primary source
	Getenv          func(string) string
}

func Collect(opts Options) schema.Tool {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if root, ok := agentpath.ClaudeRoot(opts.Root); ok {
		opts.Root = root
	} else {
		return errTool("home_dir", "cannot locate the home directory")
	}

	if len(opts.StatuslineInput) > 0 {
		return fromStatusline(opts)
	}
	return fromTranscripts(opts)
}

func errTool(code, msg string) schema.Tool {
	t := schema.Unavailable(schema.ToolClaudeCode)
	t.Available = true
	t.Error = &schema.Error{Code: code, Message: msg}
	return t
}

// detectBackend distinguishes the auth backend from process environment.
func detectBackend(getenv func(string) string) string {
	switch {
	case truthy(getenv("CLAUDE_CODE_USE_BEDROCK")):
		return schema.BackendBedrock
	case truthy(getenv("CLAUDE_CODE_USE_VERTEX")):
		return schema.BackendVertex
	case truthy(getenv("ANTHROPIC_API_KEY")):
		return schema.BackendAPI
	}
	return schema.BackendSubscription
}

func truthy(v string) bool {
	return v != "" && v != "0" && v != "false"
}

// backendFromStatusline lets Claude Code's live payload override an ambient
// ANTHROPIC_API_KEY from other tools. Explicit Bedrock/Vertex env still wins:
// those backends do not expose subscription windows.
func backendFromStatusline(getenv func(string) string, rl *statuslineRateLimits) string {
	backend := detectBackend(getenv)
	if backend == schema.BackendAPI && hasRateLimitWindow(rl) {
		return schema.BackendSubscription
	}
	return backend
}

func hasRateLimitWindow(rl *statuslineRateLimits) bool {
	return rl != nil && (rl.FiveHour != nil || rl.SevenDay != nil)
}

// StatuslineInput mirrors the JSON Claude Code pipes to the statusline
// command (https://code.claude.com/docs/en/statusline).
type StatuslineInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	Model          struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Effort *struct {
		Level string `json:"level"`
	} `json:"effort"` // present only when the model supports reasoning effort
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cost *struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow *struct {
		ContextWindowSize int64    `json:"context_window_size"`
		UsedPercentage    *float64 `json:"used_percentage"`
		CurrentUsage      *Usage   `json:"current_usage"`
	} `json:"context_window"`
	RateLimits *statuslineRateLimits `json:"rate_limits"`
}

type statuslineRateLimits struct {
	FiveHour *slWindow `json:"five_hour"`
	SevenDay *slWindow `json:"seven_day"`
}

type slWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // epoch seconds
}

func fromStatusline(opts Options) schema.Tool {
	var in StatuslineInput
	if err := json.Unmarshal(opts.StatuslineInput, &in); err != nil {
		return errTool("statusline_parse", err.Error())
	}
	t := schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Backend:   backendFromStatusline(opts.Getenv, in.RateLimits),
	}
	collected := opts.Now.Local().Format(time.RFC3339)
	t.CollectedAt = &collected // stdin data is live by definition

	if in.Model.ID != "" {
		m := &schema.Model{ID: in.Model.ID}
		if in.Model.DisplayName != "" {
			m.DisplayName = &in.Model.DisplayName
		}
		if in.Effort != nil && in.Effort.Level != "" {
			lvl := in.Effort.Level
			m.Effort = &lvl
		}
		t.Model = m
	}

	sess := &schema.Session{}
	if in.SessionID != "" {
		sess.ID = &in.SessionID
	}
	if in.TranscriptPath != "" {
		tp := in.TranscriptPath
		sess.TranscriptPath = &tp
	}
	cwd := in.CWD
	if cwd == "" {
		cwd = in.Workspace.CurrentDir
	}
	if cwd != "" {
		sess.CWD = &cwd
	}
	fb := &schema.Fallback{}
	if cw := in.ContextWindow; cw != nil {
		if cw.ContextWindowSize > 0 {
			sess.ContextWindow = &cw.ContextWindowSize
		}
		sess.ContextUsedPct = cw.UsedPercentage
	}
	// session.tokens is the session's cumulative usage by contract. Since
	// Claude Code v2.1.132 the statusline's context_window.total_* mean
	// "currently in the context window" (the figure shrinks on /compact), so
	// the cumulative totals come from the transcript instead, with the same
	// aggregation as the transcript route. An unreadable or usage-less
	// transcript leaves tokens null (unknown, never a wrong-semantics
	// substitute) — see #185.
	if in.TranscriptPath != "" {
		if b, err := os.ReadFile(in.TranscriptPath); err == nil {
			if totals, last := usageFromTranscript(b); last != nil {
				totals.Total = totals.Input + totals.Output
				sess.Tokens = &totals
				fb.SessionTokens = &totals.Total
			}
		}
	}
	t.Session = sess
	if in.Cost != nil {
		fb.EstimatedCostUSD = &in.Cost.TotalCostUSD
	}
	t.Fallback = fb

	if rl := in.RateLimits; rl != nil && t.Backend == schema.BackendSubscription {
		var limits []schema.Limit
		if rl.FiveHour != nil {
			limits = append(limits, toLimit(schema.WindowFiveHour, 300, rl.FiveHour))
		}
		if rl.SevenDay != nil {
			limits = append(limits, toLimit(schema.WindowWeekly, 10080, rl.SevenDay))
		}
		t.Limits = limits
	}
	return t
}

func toLimit(window string, mins int, w *slWindow) schema.Limit {
	pct := w.UsedPercentage
	l := schema.Limit{
		Window:        window,
		WindowMinutes: &mins,
		UsedPct:       &pct,
	}
	// resets_at is nullable: keep an absent/zero epoch as null rather than
	// formatting it as 1970-01-01.
	if w.ResetsAt > 0 {
		resets := time.Unix(w.ResetsAt, 0).Local().Format(time.RFC3339)
		l.ResetsAt = &resets
	}
	return l
}

// ---- transcript route (no statusline stdin available) ----

func fromTranscripts(opts Options) schema.Tool {
	paths := transcriptsByRecency(filepath.Join(opts.Root, "projects"))
	if len(paths) == 0 {
		return schema.Unavailable(schema.ToolClaudeCode)
	}

	// The newest transcript may have no assistant usage yet (session just
	// opened, or a non-conversation entry was written last). Fall back to the
	// next most recent transcript that actually carries usage.
	var (
		totals  schema.Tokens
		last    *TranscriptLine
		path    string
		readErr error
	)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			readErr = err
			continue
		}
		if tt, ll := usageFromTranscript(b); ll != nil {
			totals, last, path = tt, ll, p
			break
		}
	}
	if last == nil {
		if readErr != nil {
			return errTool("read_error", readErr.Error())
		}
		return errTool("no_usage", "no assistant usage entries in recent transcripts")
	}

	t := schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Backend:   detectBackend(opts.Getenv),
	}
	sess := &schema.Session{}
	sess.TranscriptPath = &path
	totals.Total = totals.Input + totals.Output
	sess.Tokens = &totals
	t.Fallback = &schema.Fallback{SessionTokens: &totals.Total}

	if last.SessionID != "" {
		sess.ID = &last.SessionID
	}
	if last.CWD != "" {
		sess.CWD = &last.CWD
	}
	if last.Message.Model != "" {
		t.Model = &schema.Model{ID: last.Message.Model}
	}
	if ts, err := time.Parse(time.RFC3339Nano, last.Timestamp); err == nil {
		s := ts.Local().Format(time.RFC3339)
		t.CollectedAt = &s
		t.Stale = opts.Now.Sub(ts) > schema.StaleAfterMinutes*time.Minute
	}
	t.Session = sess
	// Rate limits are only delivered via the statusline stdin; this route
	// leaves limits null (renderers fall back to session tokens).
	return t
}

// usageFromTranscript sums token usage across a transcript's assistant turns.
// last is nil when the transcript carries no usable usage entries, which is
// the signal to fall back to an older transcript.
func usageFromTranscript(b []byte) (totals schema.Tokens, last *TranscriptLine) {
	seen := UsageSet{}
	EachUsageLine(b, func(line TranscriptLine) {
		// last tracks the newest usage-bearing line for model/session/timestamp.
		// Update it on every line, including duplicate blocks: a later block is
		// still the newest entry, so dedup must not rewind the metadata time.
		last = &line
		if seen.Dup(line) {
			return
		}
		u := line.Message.Usage
		totals.Input += u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		totals.CachedInput += u.CacheReadInputTokens
		totals.Output += u.OutputTokens
	})
	return totals, last
}

// transcriptsByRecency lists transcript paths newest-first by mtime, so the
// caller can fall back past a newest transcript that has no usage yet.
func transcriptsByRecency(projects string) []string {
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	type entry struct {
		path string
		mod  time.Time
	}
	var entries []entry
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
			if err != nil {
				continue
			}
			entries = append(entries, entry{filepath.Join(projects, d.Name(), f.Name()), info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		// Tie-break equal mtimes by path so selection is deterministic
		// rather than dependent on directory read order.
		if entries[i].mod.Equal(entries[j].mod) {
			return entries[i].path > entries[j].path
		}
		return entries[i].mod.After(entries[j].mod)
	})
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	return paths
}
