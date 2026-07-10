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

// SnapshotMaxAge is how long a statusline snapshot is still used. It carries
// rate limits and context the transcript route can't see, so we keep showing
// the last-known values (marked stale by age — see StaleAfterMinutes) rather
// than dropping to "--". Deliberately long: last-known beats no data.
const SnapshotMaxAge = 30 * 24 * time.Hour

type snapshotFile struct {
	SchemaVersion string `json:"schema_version"`
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
// limitsObserved is when t.Limits were actually observed live: now for a
// payload that carried them, the original observation for limits preserved
// from a previous snapshot. With no limits (or a zero time) the field stays
// unset.
func WriteSnapshot(t schema.Tool, limitsObserved time.Time) error {
	f := snapshotFile{SchemaVersion: schema.Version, Tool: t}
	if len(t.Limits) > 0 && !limitsObserved.IsZero() {
		s := limitsObserved.Local().Format(time.RFC3339)
		f.LimitsCollectedAt = &s
	}
	return writeJSON("snapshot-"+t.Tool+".json", f)
}

// ReadSnapshot returns a tool snapshot no older than maxAge, with its
// stale flag recomputed against now.
func ReadSnapshot(tool string, maxAge time.Duration, now time.Time) (*schema.Tool, bool) {
	snap, ok := readSnapshotFile(tool)
	if !ok || snap.CollectedAt == nil {
		return nil, false
	}
	ts, err := time.Parse(time.RFC3339, *snap.CollectedAt)
	if err != nil || now.Sub(ts) > maxAge {
		return nil, false
	}
	t := snap.Tool
	t.Stale = now.Sub(ts) > schema.StaleAfterMinutes*time.Minute
	return &t, true
}

// ReadSnapshotLimits returns the snapshot's rate limits together with the
// backend they were observed under and their original observation time, for
// callers deciding whether to carry them into a payload that lacks limits.
// maxAge is measured from that observation (limits_collected_at, falling
// back to collected_at for snapshots written before the field existed), so
// limits that are only being carried forward still age out (#186).
func ReadSnapshotLimits(tool string, maxAge time.Duration, now time.Time) ([]schema.Limit, string, time.Time, bool) {
	snap, ok := readSnapshotFile(tool)
	if !ok || len(snap.Limits) == 0 {
		return nil, "", time.Time{}, false
	}
	observedStr := snap.CollectedAt
	if snap.LimitsCollectedAt != nil {
		observedStr = snap.LimitsCollectedAt
	}
	if observedStr == nil {
		return nil, "", time.Time{}, false
	}
	observed, err := time.Parse(time.RFC3339, *observedStr)
	if err != nil || now.Sub(observed) > maxAge {
		return nil, "", time.Time{}, false
	}
	return snap.Limits, snap.Backend, observed, true
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
