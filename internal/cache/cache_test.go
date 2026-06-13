package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

func setCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", dir)
	return dir
}

func TestStatusRoundTripAndTTL(t *testing.T) {
	dir := setCacheDir(t)
	now := time.Now()

	if _, ok := ReadStatus(StatusTTL, now); ok {
		t.Fatal("ReadStatus hit on empty cache")
	}
	s := &schema.Status{SchemaVersion: schema.Version, GeneratedAt: now.Format(time.RFC3339)}
	if err := WriteStatus(s); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadStatus(StatusTTL, now)
	if !ok || got.GeneratedAt != s.GeneratedAt {
		t.Fatalf("ReadStatus = %+v, %v", got, ok)
	}

	// Age the file past the TTL.
	old := now.Add(-StatusTTL - time.Second)
	path := filepath.Join(dir, "status.json")
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStatus(StatusTTL, now); ok {
		t.Error("ReadStatus hit on expired cache")
	}
}

func TestStatusRejectsOtherSchemaVersion(t *testing.T) {
	dir := setCacheDir(t)
	if err := os.WriteFile(filepath.Join(dir, "status.json"),
		[]byte(`{"schema_version":"9.9","generated_at":"x","tools":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStatus(StatusTTL, time.Now()); ok {
		t.Error("ReadStatus accepted a different schema_version")
	}
}

func TestSnapshot(t *testing.T) {
	setCacheDir(t)
	now := time.Now()

	collected := now.Add(-2 * time.Minute).Format(time.RFC3339)
	tool := schema.Unavailable(schema.ToolClaudeCode)
	tool.Available = true
	tool.CollectedAt = &collected
	if err := WriteSnapshot(tool); err != nil {
		t.Fatal(err)
	}

	got, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now)
	if !ok || !got.Available {
		t.Fatalf("ReadSnapshot = %+v, %v", got, ok)
	}
	if got.Stale {
		t.Error("Stale = true for 2-minute-old snapshot")
	}

	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now.Add(SnapshotMaxAge+time.Minute)); ok {
		t.Error("ReadSnapshot returned a snapshot older than maxAge")
	}

	// Returned within maxAge but past the stale threshold → Stale recomputed
	// to true. Age here is ~92min (> 60min threshold) but under the 2h maxAge.
	got, ok = ReadSnapshot(schema.ToolClaudeCode, 2*time.Hour, now.Add(90*time.Minute))
	if !ok || !got.Stale {
		t.Errorf("ReadSnapshot stale recompute: got %+v, %v", got, ok)
	}
}
