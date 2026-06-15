// Package claude collects status from Claude Code, via two routes:
//
//  1. the statusline stdin JSON (richest: model, context %, rate limits)
//  2. transcript files under ~/.claude/projects (model + session tokens only)
package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

type Options struct {
	Root            string    // Claude home, defaults to ~/.claude
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
	if opts.Root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return errTool("home_dir", err.Error())
		}
		opts.Root = filepath.Join(home, ".claude")
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

// detectBackend distinguishes the auth backend. Rate-limit windows only
// exist for subscription auth; Bedrock/Vertex degrade to limits=null.
func detectBackend(getenv func(string) string) string {
	switch {
	case truthy(getenv("CLAUDE_CODE_USE_BEDROCK")):
		return schema.BackendBedrock
	case truthy(getenv("CLAUDE_CODE_USE_VERTEX")):
		return schema.BackendVertex
	}
	return schema.BackendSubscription
}

func truthy(v string) bool {
	return v != "" && v != "0" && v != "false"
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
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cost *struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow *struct {
		TotalInputTokens  int64    `json:"total_input_tokens"`
		TotalOutputTokens int64    `json:"total_output_tokens"`
		ContextWindowSize int64    `json:"context_window_size"`
		UsedPercentage    *float64 `json:"used_percentage"`
		CurrentUsage      *usage   `json:"current_usage"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour *slWindow `json:"five_hour"`
		SevenDay *slWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type slWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // epoch seconds
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

func fromStatusline(opts Options) schema.Tool {
	var in StatuslineInput
	if err := json.Unmarshal(opts.StatuslineInput, &in); err != nil {
		return errTool("statusline_parse", err.Error())
	}
	t := schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Backend:   detectBackend(opts.Getenv),
	}
	collected := opts.Now.Local().Format(time.RFC3339)
	t.CollectedAt = &collected // stdin data is live by definition

	if in.Model.ID != "" {
		m := &schema.Model{ID: in.Model.ID}
		if in.Model.DisplayName != "" {
			m.DisplayName = &in.Model.DisplayName
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
		var cached int64
		if cw.CurrentUsage != nil {
			cached = cw.CurrentUsage.CacheReadInputTokens
		}
		sess.Tokens = &schema.Tokens{
			Input:       cw.TotalInputTokens,
			CachedInput: cached,
			Output:      cw.TotalOutputTokens,
			Total:       cw.TotalInputTokens + cw.TotalOutputTokens,
		}
		total := sess.Tokens.Total
		fb.SessionTokens = &total
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
	resets := time.Unix(w.ResetsAt, 0).Local().Format(time.RFC3339)
	return schema.Limit{
		Window:        window,
		WindowMinutes: &mins,
		UsedPct:       &pct,
		ResetsAt:      &resets,
	}
}

// ---- transcript route (no statusline stdin available) ----

type transcriptLine struct {
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Message   *struct {
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

func fromTranscripts(opts Options) schema.Tool {
	path := latestTranscript(filepath.Join(opts.Root, "projects"))
	if path == "" {
		return schema.Unavailable(schema.ToolClaudeCode)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return errTool("read_error", err.Error())
	}

	t := schema.Tool{
		Tool:      schema.ToolClaudeCode,
		Available: true,
		Backend:   detectBackend(opts.Getenv),
	}
	sess := &schema.Session{}
	tp := path
	sess.TranscriptPath = &tp
	totals := schema.Tokens{}
	var last *transcriptLine
	for _, raw := range bytes.Split(b, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || !bytes.Contains(raw, []byte(`"usage"`)) {
			continue
		}
		var line transcriptLine
		if json.Unmarshal(raw, &line) != nil || line.Message == nil || line.Message.Usage == nil {
			continue
		}
		u := line.Message.Usage
		totals.Input += u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		totals.CachedInput += u.CacheReadInputTokens
		totals.Output += u.OutputTokens
		last = &line
	}
	if last == nil {
		return errTool("no_usage", "no assistant usage entries in "+path)
	}
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

func latestTranscript(projects string) string {
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}
	var best string
	var bestMod time.Time
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
			if info.ModTime().After(bestMod) {
				best, bestMod = filepath.Join(projects, d.Name(), f.Name()), info.ModTime()
			}
		}
	}
	return best
}
