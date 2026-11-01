// Package claude collects status from Claude Code, via two routes:
//
//  1. the statusline stdin JSON (richest: model, context %, rate limits)
//  2. transcript files under ~/.claude/projects (model + session only)
//
// Neither route counts the session's tokens: core.AddSessionTree does, over
// the session's whole transcript tree, so the collector leaves session.tokens
// and fallback.session_tokens null (#375).
package claude

import (
	"encoding/json"
	"errors"
	"io/fs"
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
	Cost          *statuslineCost `json:"cost"`
	ContextWindow *struct {
		ContextWindowSize int64    `json:"context_window_size"`
		UsedPercentage    *float64 `json:"used_percentage"`
		CurrentUsage      *Usage   `json:"current_usage"`
	} `json:"context_window"`
	RateLimits *statuslineRateLimits `json:"rate_limits"`
}

type statuslineCost struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	// TotalDurationMS counts from the session's start; a float so a
	// fractional value can't fail the whole payload's decoding.
	TotalDurationMS *float64 `json:"total_duration_ms"`
}

type statuslineRateLimits struct {
	FiveHour *slWindow `json:"five_hour"`
	SevenDay *slWindow `json:"seven_day"`
}

type slWindow struct {
	UsedPercentage *float64 `json:"used_percentage"` // nullable: absent means unknown, not 0 (#322)
	ResetsAt       int64    `json:"resets_at"`       // epoch seconds
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
	// they are never used for it (#185): core.AddSessionTree counts it from
	// the transcripts. Here the transcript only dates the rate limits.
	var lastResponse time.Time // the session's latest API response on record
	if in.TranscriptPath != "" {
		if b, err := os.ReadFile(in.TranscriptPath); err == nil {
			if last, ok := lastUsageLine(b); ok {
				if ts, err := time.Parse(time.RFC3339Nano, last.Timestamp); err == nil {
					lastResponse = ts
				}
			}
		}
	}
	t.Session = sess
	if in.Cost != nil {
		fb.EstimatedCostUSD = &in.Cost.TotalCostUSD
	}
	t.Fallback = fb

	if rl := in.RateLimits; rl != nil && t.Backend == schema.BackendSubscription {
		// Sub-second precision keeps two readings in the same second in order
		// when merged into the snapshot (#330).
		observed := limitsObservedAt(lastResponse, sessionStart(in.Cost, opts.Now), opts.Now).Local().Format(time.RFC3339Nano)
		var limits []schema.Limit
		if rl.FiveHour != nil {
			limits = append(limits, toLimit(schema.WindowFiveHour, 300, rl.FiveHour, observed))
		}
		if rl.SevenDay != nil {
			limits = append(limits, toLimit(schema.WindowWeekly, 10080, rl.SevenDay, observed))
		}
		t.Limits = limits
	}
	return t
}

// limitsObservedAt dates the rate limits a payload carries. Claude Code
// refreshes them only from an API response (and a quota probe as the session
// starts), but redraws the statusline on other triggers too — a reset time
// passing, the prompt cache expiring, a mode toggle — so an idle session
// pushes a reading as old as its last response. The payload doesn't date it,
// so the later of the session's last response on record and its start does
// (#330): an estimate that errs old, since a subagent's responses refresh the
// reading without reaching the main transcript. A payload carrying neither is
// dated at receipt, as is one whose dates lie ahead of it.
func limitsObservedAt(lastResponse, sessionStart, now time.Time) time.Time {
	observed := lastResponse
	if sessionStart.After(observed) {
		observed = sessionStart
	}
	if observed.IsZero() || observed.After(now) {
		return now
	}
	return observed
}

// sessionStart is when the payload's session began, from the time Claude
// Code says it has been running; zero when the payload doesn't say.
func sessionStart(cost *statuslineCost, now time.Time) time.Time {
	if cost == nil || cost.TotalDurationMS == nil {
		return time.Time{}
	}
	return now.Add(-time.Duration(*cost.TotalDurationMS * float64(time.Millisecond)))
}

// toLimit builds a window observed at observed (RFC 3339; see
// limitsObservedAt).
func toLimit(window string, mins int, w *slWindow, observed string) schema.Limit {
	l := schema.Limit{
		Window:        window,
		WindowMinutes: &mins,
		ObservedAt:    &observed,
	}
	// A window without used_percentage is one whose use is unknown; it stays
	// null rather than reading as 0% used / 100% left (#322).
	if w.UsedPercentage != nil {
		pct := *w.UsedPercentage
		l.UsedPct = &pct
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
	paths, listErr := transcriptsByRecency(filepath.Join(opts.Root, "projects"))
	if len(paths) == 0 {
		if listErr != nil {
			// Nothing listed because the listing failed, not because Claude
			// Code left no transcripts: unknown, not absent (#437).
			return errTool("read_error", listErr.Error())
		}
		return schema.Unavailable(schema.ToolClaudeCode)
	}

	// The newest transcript may have no assistant usage yet (session just
	// opened), and a transcript's mtime moves with any line, so an older
	// session that only got a user line appended can sort ahead of a session
	// with a newer response (#437). mtime therefore only orders the visit: the
	// transcript whose last usage line is the newest by timestamp wins. Once a
	// usage line is found, a transcript whose mtime predates it by more than
	// mtimeSlack is skipped without reading — a line is never newer than its
	// file's mtime, give or take the file system's mtime precision — and the
	// skip re-stats at decision time, so a response appended after the listing
	// is still seen. A usage line whose timestamp doesn't parse can't be
	// compared; when the first one found is like that, it is used as before.
	var (
		last    *TranscriptLine
		lastTS  time.Time
		path    string
		readErr error
	)
	for _, p := range paths {
		if last != nil {
			if lastTS.IsZero() {
				break
			}
			if info, err := os.Stat(p); err == nil && info.ModTime().Add(mtimeSlack).Before(lastTS) {
				continue
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			readErr = err
			continue
		}
		l, ok := lastUsageLine(b)
		if !ok {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		switch {
		case last == nil:
			last, path = &l, p
			if err == nil {
				lastTS = ts
			}
		case err == nil && ts.After(lastTS):
			last, lastTS, path = &l, ts, p
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
	t.Fallback = &schema.Fallback{} // session_tokens is core.AddSessionTree's to fill

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
		t.Stale = opts.Now.Sub(ts) > schema.StaleAfter(schema.ToolClaudeCode)
	}
	t.Session = sess
	// Rate limits are only delivered via the statusline stdin; this route
	// leaves limits null (renderers fall back to session tokens).
	return t
}

// mtimeSlack covers the file system's mtime precision when an mtime bounds
// the newest line a file can hold: a file system that keeps whole seconds (or
// FAT's two) can stamp a file before the sub-second timestamp of its last
// line.
const mtimeSlack = 2 * time.Second

// transcriptsByRecency lists transcript paths newest-first by mtime, so the
// caller can fall back past a newest transcript that has no usage yet. A
// project directory or entry that can't be listed is a gap: the paths are
// what was listed, and the first such error is returned for the caller to
// report when nothing was listed at all (#437). A missing projects directory
// (Claude Code not used yet) is not an error.
func transcriptsByRecency(projects string) ([]string, error) {
	dirs, err := os.ReadDir(projects)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var first error
	fail := func(err error) {
		// An entry removed while listing is gone, not unreadable.
		if first == nil && !errors.Is(err, fs.ErrNotExist) {
			first = err
		}
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
			fail(err)
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				fail(err)
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
	return paths, first
}
