package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

// TestWriteStatusConcurrent exercises the tmp-file + atomic-rename contract:
// many writers racing on one path never corrupt it, a reader racing them never
// sees a partial file, the survivor is exactly one writer's payload, and no
// temp files are left behind.
func TestWriteStatusConcurrent(t *testing.T) {
	dir := setCacheDir(t)
	now := time.Now()

	const writers = 20
	want := make(map[string]bool, writers)
	for i := 0; i < writers; i++ {
		want[strconv.Itoa(i)] = true
	}

	// A reader racing the writers must only ever observe a complete file:
	// rename is all-or-nothing, so ReadFile gets either ENOENT or whole JSON.
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		path := filepath.Join(dir, "status.json")
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue // not written yet, or mid-rename — both fine
			}
			var s schema.Status
			if json.Unmarshal(b, &s) != nil {
				t.Error("reader observed a partial/corrupt status.json")
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := &schema.Status{SchemaVersion: schema.Version, GeneratedAt: strconv.Itoa(i)}
			if err := WriteStatus(s); err != nil {
				t.Errorf("WriteStatus: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	readerWG.Wait()

	got, ok := ReadStatus(StatusTTL, now)
	if !ok {
		t.Fatal("ReadStatus miss after concurrent writes")
	}
	if !want[got.GeneratedAt] {
		t.Errorf("final status.json = %q, not one of the written payloads", got.GeneratedAt)
	}

	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("leftover temp files after writes: %v", leftovers)
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

func TestSnapshotRejectsOtherSchemaVersion(t *testing.T) {
	dir := setCacheDir(t)
	now := time.Now()
	collected := now.Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json"),
		[]byte(`{"schema_version":"9.9","tool":"claude-code","available":true,"collected_at":"`+collected+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now); ok {
		t.Error("ReadSnapshot accepted a different schema_version")
	}
}
