// Package cache is the short-lived file cache shared by all renderers.
// Writes are tmp-file + rename so concurrent readers never see partial JSON.
package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// StatusTTL is how long an assembled status document stays fresh.
const StatusTTL = 30 * time.Second

// SnapshotMaxAge is how long a statusline snapshot is still used. It carries
// rate limits and context the transcript route can't see, so we keep showing
// the last-known values (marked stale by age — see StaleAfterMinutes) rather
// than dropping to "--". Deliberately long: last-known beats no data.
const SnapshotMaxAge = 30 * 24 * time.Hour

type snapshotFile struct {
	SchemaVersion string `json:"schema_version"`
	// Root is the agent's config root the snapshot was observed from
	// (CLAUDE_CONFIG_DIR or its default). A reader working against another
	// root — a second profile on the same machine — must not take this
	// snapshot's session or limits for its own (#321).
	Root string `json:"root,omitempty"`
	// LimitsCollectedAt is when Limits were originally observed from a live
	// statusline payload. Re-saves that merely carry limits forward keep the
	// original time, so preserved limits age out from their real observation
	// instead of being re-stamped fresh on every rewrite (#186).
	LimitsCollectedAt *string `json:"limits_collected_at,omitempty"`
	schema.Tool
}

// Dir returns the cache directory, honoring TACHO_CACHE_DIR for tests
// and non-standard setups.
func Dir() (string, error) {
	if d := os.Getenv("TACHO_CACHE_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tachograph"), nil
}

// statusFile is the TTL cache's on-disk form: the status document plus the
// config roots (by tool) it was assembled from, so a run against other roots
// — another profile on the same machine — doesn't take it for its own (#321).
type statusFile struct {
	Roots map[string]string `json:"roots,omitempty"`
	schema.Status
}

// ReadStatus returns the cached status if its file is younger than ttl and
// it was assembled from the same config roots.
func ReadStatus(ttl time.Duration, now time.Time, roots map[string]string) (*schema.Status, bool) {
	dir, err := Dir()
	if err != nil {
		return nil, false
	}
	path := filepath.Join(dir, "status.json")
	st, err := os.Stat(path)
	if err != nil || now.Sub(st.ModTime()) > ttl {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var f statusFile
	if json.Unmarshal(b, &f) != nil || f.SchemaVersion != schema.Version || !sameRoots(f.Roots, roots) {
		return nil, false
	}
	return &f.Status, true
}

// WriteStatus caches s as assembled from roots (by tool, see ReadStatus).
func WriteStatus(s *schema.Status, roots map[string]string) error {
	f := statusFile{Roots: map[string]string{}, Status: *s}
	for tool, root := range roots {
		f.Roots[tool] = normalizeRoot(root)
	}
	return writeJSON("status.json", f)
}

// WriteSnapshot persists a single tool's collected state outside the TTL
// cache. Used by `tacho statusline` to piggyback Claude Code's push so
// other renderers can show rate limits without a statusline stdin.
// limitsObserved is when t.Limits were actually observed live: now for a
// payload that carried them, the original observation for limits preserved
// from a previous snapshot. With no limits (or a zero time) the field stays
// unset. root is the config root the payload was observed from (#321).
func WriteSnapshot(t schema.Tool, limitsObserved time.Time, root string) error {
	f := snapshotFile{SchemaVersion: schema.Version, Root: normalizeRoot(root), Tool: t}
	if len(t.Limits) > 0 && !limitsObserved.IsZero() {
		s := limitsObserved.Local().Format(time.RFC3339)
		f.LimitsCollectedAt = &s
	}
	return writeJSON("snapshot-"+t.Tool+".json", f)
}

// ReadSnapshot returns a tool snapshot no older than maxAge, with its
// stale flag recomputed against now. Rate limits age out separately, from
// their original observation time: a snapshot that preserved old limits
// shortly before its writer went quiet would otherwise keep showing them
// past the ceiling for up to another maxAge (#186). Only a snapshot observed
// from root is served (#321).
func ReadSnapshot(tool string, maxAge time.Duration, now time.Time, root string) (*schema.Tool, bool) {
	snap, ok := readSnapshotFile(tool)
	if !ok || !sameRoot(snap.Root, root) || snap.CollectedAt == nil {
		return nil, false
	}
	ts, err := time.Parse(time.RFC3339, *snap.CollectedAt)
	if err != nil || now.Sub(ts) > maxAge {
		return nil, false
	}
	t := snap.Tool
	t.Stale = now.Sub(ts) > schema.StaleAfterMinutes*time.Minute
	if len(t.Limits) > 0 {
		observed, ok := snap.limitsObservedAt()
		if !ok || now.Sub(observed) > maxAge {
			t.Limits = nil
		}
	}
	return &t, true
}

// ReadSnapshotLimits returns the snapshot's rate limits together with the
// backend they were observed under and their original observation time, for
// callers deciding whether to carry them into a payload that lacks limits.
// maxAge is measured from that observation, so limits that are only being
// carried forward still age out (#186). Only a snapshot observed from root
// is consulted (#321).
func ReadSnapshotLimits(tool string, maxAge time.Duration, now time.Time, root string) ([]schema.Limit, string, time.Time, bool) {
	snap, ok := readSnapshotFile(tool)
	if !ok || !sameRoot(snap.Root, root) || len(snap.Limits) == 0 {
		return nil, "", time.Time{}, false
	}
	observed, ok := snap.limitsObservedAt()
	if !ok || now.Sub(observed) > maxAge {
		return nil, "", time.Time{}, false
	}
	return snap.Limits, snap.Backend, observed, true
}

// normalizeRoot is the form config roots are stored and compared in: the
// absolute, cleaned path, resolved against the current directory at the time
// (a relative CLAUDE_CONFIG_DIR names a different directory from a different
// cwd). An empty root — none could be resolved — stays empty.
func normalizeRoot(root string) string {
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

// sameRoot reports whether two config roots name the same directory. An
// empty root never matches: a snapshot written before the root was recorded
// carries none, as does a run whose root couldn't be resolved, and neither
// may be taken for a given profile's (#321).
func sameRoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return normalizeRoot(a) == normalizeRoot(b)
}

// sameRoots reports whether two root sets (by tool) name the same
// directories for the same tools; an empty set matches nothing.
func sameRoots(a, b map[string]string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for tool, root := range a {
		if other, ok := b[tool]; !ok || !sameRoot(root, other) {
			return false
		}
	}
	return true
}

// limitsObservedAt resolves when the snapshot's limits were originally
// observed: limits_collected_at, or collected_at for snapshots written
// before the field existed.
func (s *snapshotFile) limitsObservedAt() (time.Time, bool) {
	str := s.CollectedAt
	if s.LimitsCollectedAt != nil {
		str = s.LimitsCollectedAt
	}
	if str == nil {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, *str)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func readSnapshotFile(tool string) (*snapshotFile, bool) {
	dir, err := Dir()
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "snapshot-"+tool+".json"))
	if err != nil {
		return nil, false
	}
	var snap snapshotFile
	if json.Unmarshal(b, &snap) != nil || snap.SchemaVersion != schema.Version {
		return nil, false
	}
	return &snap, true
}

// ReadJSON decodes the named cache file into v. False when the file is
// missing or not valid JSON for v.
func ReadJSON(name string, v any) bool {
	dir, err := Dir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// WriteJSON writes v as the named cache file, tmp-file + rename so
// concurrent readers never see partial JSON.
func WriteJSON(name string, v any) error {
	return writeJSON(name, v)
}

func writeJSON(name string, v any) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// DailyHistoryEntry is one tool's figure for one closed day in the rolling
// history cache: Daily, or nil when the tool's logs could not be read at
// CheckedAt (the caller retries after a while instead of every tick).
type DailyHistoryEntry struct {
	Daily     *schema.Daily `json:"daily"`
	CheckedAt string        `json:"checked_at"` // RFC 3339
}

// dailyHistoryFile is the rolling cache of closed days behind the SwiftBar
// history rows (#243). It is derived data: every entry can be recomputed
// from the logs, it never holds more than the window's closed days, and it
// is dropped whole when its key (binary identity, schema, pricing override)
// changes. Days map a local calendar day to each tool's entry.
type dailyHistoryFile struct {
	SchemaVersion string                                  `json:"schema_version"`
	Key           string                                  `json:"key"`
	Days          map[string]map[string]DailyHistoryEntry `json:"days"`
}

// ReadDailyHistory returns the cached closed days when the file exists and
// was written under the same key and schema version; otherwise nothing.
func ReadDailyHistory(key string) (map[string]map[string]DailyHistoryEntry, bool) {
	dir, err := Dir()
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "daily-history.json"))
	if err != nil {
		return nil, false
	}
	var f dailyHistoryFile
	if json.Unmarshal(b, &f) != nil || f.SchemaVersion != schema.Version || f.Key != key || f.Days == nil {
		return nil, false
	}
	return f.Days, true
}

// WriteDailyHistory replaces the cached closed days under key.
func WriteDailyHistory(key string, days map[string]map[string]DailyHistoryEntry) error {
	return writeJSON("daily-history.json", dailyHistoryFile{SchemaVersion: schema.Version, Key: key, Days: days})
}

// SessionTreeCache persists, for one Claude session tree, what
// daily.ClaudeSessionTree needs from its transcripts last written before
// today (an opaque blob per file, daily.TreeFileCache), so the statusline
// doesn't re-read a long session's finished subagents on every call (#262).
// One small file per session tree; entries are keyed by path and invalidated
// by size or mtime, and the whole file by the schema version.
type SessionTreeCache struct {
	name    string
	entries map[string]sessionTreeEntry
	dirty   bool            // Put changed entries: write on Save
	used    bool            // Get hit: keep the file from looking unused
	seen    map[string]bool // paths asked about this run; others are gone
}

type sessionTreeEntry struct {
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // unix nanoseconds
	Data  []byte `json:"data"`  // base64 in the file
}

type sessionTreeFile struct {
	SchemaVersion string                      `json:"schema_version"`
	Entries       map[string]sessionTreeEntry `json:"entries"`
}

// sessionTreeMaxAge is how long an unused session-tree cache file is kept.
const sessionTreeMaxAge = 30 * 24 * time.Hour

// OpenSessionTree loads the cache for the session tree rooted at sessionDir
// (the main transcript's path without ".jsonl"). A missing, unreadable, or
// older-schema file starts empty.
func OpenSessionTree(sessionDir string) *SessionTreeCache {
	sum := sha256.Sum256([]byte(sessionDir))
	c := &SessionTreeCache{
		name:    fmt.Sprintf("session-tree-%x.json", sum[:8]),
		entries: map[string]sessionTreeEntry{},
		seen:    map[string]bool{},
	}
	var f sessionTreeFile
	if ReadJSON(c.name, &f) && f.SchemaVersion == schema.Version && f.Entries != nil {
		c.entries = f.Entries
	}
	return c
}

// Get returns the cached blob for path while its size and mtime match.
func (c *SessionTreeCache) Get(path string, size int64, mtime time.Time) ([]byte, bool) {
	c.seen[path] = true
	e, ok := c.entries[path]
	if !ok || e.Size != size || e.MTime != mtime.UnixNano() {
		return nil, false
	}
	c.used = true
	return e.Data, true
}

// Put records path's blob for its current size and mtime.
func (c *SessionTreeCache) Put(path string, size int64, mtime time.Time, data []byte) {
	c.seen[path] = true
	c.entries[path] = sessionTreeEntry{Size: size, MTime: mtime.UnixNano(), Data: data}
	c.dirty = true
}

// Save writes the cache when Put changed it — or, when it was only read,
// refreshes the file's mtime at most daily so an active tree's cache isn't
// taken for unused — and removes other session-tree cache files unused for
// sessionTreeMaxAge. Entries for paths not asked about this run (deleted
// transcripts, or ones now written today and read live) are dropped.
func (c *SessionTreeCache) Save(now time.Time) error {
	dir, err := Dir()
	if err != nil {
		return nil
	}
	for p := range c.entries {
		if !c.seen[p] {
			delete(c.entries, p)
			c.dirty = true
		}
	}
	if !c.dirty {
		if c.used {
			p := filepath.Join(dir, c.name)
			if info, err := os.Stat(p); err == nil && now.Sub(info.ModTime()) > 24*time.Hour {
				_ = os.Chtimes(p, now, now)
			}
		}
		return nil
	}
	if err := WriteJSON(c.name, sessionTreeFile{SchemaVersion: schema.Version, Entries: c.entries}); err != nil {
		return err
	}
	c.dirty = false
	old, _ := filepath.Glob(filepath.Join(dir, "session-tree-*.json"))
	for _, p := range old {
		if filepath.Base(p) == c.name {
			continue
		}
		if info, err := os.Stat(p); err == nil && now.Sub(info.ModTime()) > sessionTreeMaxAge {
			_ = os.Remove(p)
		}
	}
	return nil
}
