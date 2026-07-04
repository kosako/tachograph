// Transcript-line parsing shared by this collector and internal/daily. The
// on-disk transcript format is an external contract owned by Claude Code, so
// its line structure, usage fields, and dedup rule live here in one place.

package claude

import (
	"bytes"
	"encoding/json"
)

// TranscriptLine is one transcript entry carrying assistant usage.
type TranscriptLine struct {
	Timestamp string             `json:"timestamp"`
	SessionID string             `json:"sessionId"`
	CWD       string             `json:"cwd"`
	RequestID string             `json:"requestId"`
	Message   *TranscriptMessage `json:"message"`
}

type TranscriptMessage struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Usage *Usage `json:"usage"`
}

// Usage is one assistant response's token usage.
type Usage struct {
	InputTokens              int64          `json:"input_tokens"`
	CacheCreationInputTokens int64          `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64          `json:"cache_read_input_tokens"`
	OutputTokens             int64          `json:"output_tokens"`
	CacheCreation            *CacheCreation `json:"cache_creation"`
}

type CacheCreation struct {
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
}

// CacheWrites splits the cache-creation tokens by TTL: 5-minute, 1-hour, and
// unknown (no breakdown present, or the breakdown disagrees with the
// top-level total). total is always CacheCreationInputTokens.
func (u *Usage) CacheWrites() (fiveMinute, oneHour, unknown, total int64) {
	total = u.CacheCreationInputTokens
	if u.CacheCreation == nil {
		return 0, 0, total, total
	}
	fiveMinute = u.CacheCreation.Ephemeral5mInputTokens
	oneHour = u.CacheCreation.Ephemeral1hInputTokens
	known := fiveMinute + oneHour
	if total >= known {
		return fiveMinute, oneHour, total - known, total
	}
	// Trust the top-level total when the TTL breakdown is inconsistent; there
	// is no reliable way to reconstruct the 5m/1h split from malformed logs.
	return 0, 0, total, total
}

// EachUsageLine calls fn for every transcript line in b that carries
// assistant usage. Blank, non-JSON, and usage-less lines are skipped.
func EachUsageLine(b []byte, fn func(TranscriptLine)) {
	for _, raw := range bytes.Split(b, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || !bytes.Contains(raw, []byte(`"usage"`)) {
			continue
		}
		var line TranscriptLine
		if json.Unmarshal(raw, &line) != nil || line.Message == nil || line.Message.Usage == nil {
			continue
		}
		fn(line)
	}
}

// usageKey identifies one assistant API response. Claude Code writes a
// transcript line per content block (thinking/text/tool_use…), each repeating
// that response's full usage, so counting per line multiplies a turn by its
// block count. Summing once per (message id, request id) counts each response
// once — across its blocks and across files duplicated by resume/compaction.
type usageKey struct{ id, req string }

// UsageSet tracks which assistant responses have been counted. One set can
// span multiple files so a response copied forward by resume/compaction is
// still counted once.
type UsageSet map[usageKey]bool

// Dup reports whether line's response was already counted, recording it
// otherwise. Lines without a message id (rare, e.g. synthetic) can't be
// deduped and are never treated as duplicates.
func (s UsageSet) Dup(line TranscriptLine) bool {
	id := line.Message.ID
	if id == "" {
		return false
	}
	k := usageKey{id, line.RequestID}
	if s[k] {
		return true
	}
	s[k] = true
	return false
}
