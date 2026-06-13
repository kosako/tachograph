// Package core assembles collector output into the unified status document.
package core

import (
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/collector/codex"
	"github.com/kosako/tachograph/internal/daily"
	"github.com/kosako/tachograph/internal/schema"
)

type Options struct {
	ClaudeRoot string // for tests; default ~/.claude
	CodexRoot  string // for tests; default ~/.codex
	Now        time.Time
	NoCache    bool
}

// Status returns the unified document, served from the TTL cache when fresh.
func Status(opts Options) schema.Status {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if !opts.NoCache {
		if s, ok := cache.ReadStatus(cache.StatusTTL, opts.Now); ok {
			return *s
		}
	}
	s := assemble(opts)
	if !opts.NoCache {
		_ = cache.WriteStatus(&s) // serving the live result matters more than caching it
	}
	return s
}

func assemble(opts Options) schema.Status {
	claudeT := claudeTool(opts)
	codexT := codex.Collect(codex.Options{Root: opts.CodexRoot, Now: opts.Now})
	addDaily(&claudeT, daily.ClaudeTokens(opts.ClaudeRoot, opts.Now))
	addDaily(&codexT, daily.CodexTokens(opts.CodexRoot, opts.Now))
	return schema.Status{
		SchemaVersion: schema.Version,
		GeneratedAt:   opts.Now.Local().Format(time.RFC3339),
		Tools:         []schema.Tool{claudeT, codexT},
	}
}

// addDaily attaches today's aggregate to an available tool.
func addDaily(t *schema.Tool, tokens int64) {
	if !t.Available || t.Error != nil {
		return
	}
	t.Daily = &schema.Daily{Tokens: tokens}
}

// claudeTool prefers a recent statusline snapshot (which carries rate
// limits) over the transcript route (which cannot see them).
func claudeTool(opts Options) schema.Tool {
	if snap, ok := cache.ReadSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, opts.Now); ok {
		return *snap
	}
	return claude.Collect(claude.Options{Root: opts.ClaudeRoot, Now: opts.Now})
}
