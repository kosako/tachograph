// Package cache is the short-lived file cache shared by all renderers.
// Writes are tmp-file + rename so concurrent readers never see partial JSON.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// StatusTTL is how long an assembled status document stays fresh.
const StatusTTL = 30 * time.Second

// SnapshotMaxAge is how long a statusline-derived snapshot is preferred
// over the transcript route (it carries rate limits the latter cannot see).
const SnapshotMaxAge = 10 * time.Minute

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

// ReadStatus returns the cached status if its file is younger than ttl.
func ReadStatus(ttl time.Duration, now time.Time) (*schema.Status, bool) {
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
	var s schema.Status
	if json.Unmarshal(b, &s) != nil || s.SchemaVersion != schema.Version {
		return nil, false
	}
	return &s, true
}

func WriteStatus(s *schema.Status) error {
	return writeJSON("status.json", s)
}

// WriteSnapshot persists a single tool's collected state outside the TTL
// cache. Used by `tacho statusline` to piggyback Claude Code's push so
// other renderers can show rate limits without a statusline stdin.
func WriteSnapshot(t schema.Tool) error {
	return writeJSON("snapshot-"+t.Tool+".json", t)
}

// ReadSnapshot returns a tool snapshot no older than maxAge, with its
// stale flag recomputed against now.
func ReadSnapshot(tool string, maxAge time.Duration, now time.Time) (*schema.Tool, bool) {
	dir, err := Dir()
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "snapshot-"+tool+".json"))
	if err != nil {
		return nil, false
	}
	var t schema.Tool
	if json.Unmarshal(b, &t) != nil || t.CollectedAt == nil {
		return nil, false
	}
	ts, err := time.Parse(time.RFC3339, *t.CollectedAt)
	if err != nil || now.Sub(ts) > maxAge {
		return nil, false
	}
	t.Stale = now.Sub(ts) > schema.StaleAfterMinutes*time.Minute
	return &t, true
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
