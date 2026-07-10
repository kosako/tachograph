package render

import (
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func hardeningStatus(t schema.Tool) schema.Status {
	return schema.Status{SchemaVersion: schema.Version, Tools: []schema.Tool{t}}
}

// Width modifiers are capped so a typo'd {tool.5h.bar:100000} can't flood
// the terminal (#194 L-05).
func TestTemplateWidthCapped(t *testing.T) {
	pct := 50.0
	mins := 300
	tool := schema.Tool{
		Tool: schema.ToolClaudeCode, Available: true,
		Limits: []schema.Limit{{Window: schema.WindowFiveHour, WindowMinutes: &mins, UsedPct: &pct}},
	}
	out := Template("{claude.5h.bar:100000}", hardeningStatus(tool), time.Now(), Style{})
	if n := len([]rune(out)); n > maxWidth {
		t.Errorf("bar length = %d runes, want <= %d", n, maxWidth)
	}
	// The absent case pads to the requested width too, and must be capped
	// the same way.
	out = Template("{codex.5h.bar:100000}", hardeningStatus(schema.Unavailable(schema.ToolCodex)), time.Now(), Style{})
	if n := len([]rune(out)); n > maxWidth {
		t.Errorf("absent bar length = %d runes, want <= %d", n, maxWidth)
	}
}

// Log-sourced display strings are stripped of control characters before
// reaching the terminal (#194 L-05).
func TestTemplateStripsControlCharacters(t *testing.T) {
	cwd := "/tmp/evil\x1b]0;owned\x07dir"
	effort := "hi\x1bgh"
	tool := schema.Tool{
		Tool: schema.ToolClaudeCode, Available: true,
		Model:   &schema.Model{ID: "claude-x", Effort: &effort},
		Session: &schema.Session{CWD: &cwd},
	}
	out := Template("{claude.cwd} {claude.effort}", hardeningStatus(tool), time.Now(), Style{})
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("output still contains control characters: %q", out)
	}
	if !strings.Contains(out, "owneddir") || !strings.Contains(out, "high") {
		t.Errorf("output = %q, want control chars stripped but text kept", out)
	}
}
