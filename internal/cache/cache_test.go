package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// snapRoot stands in for the config root snapshots are observed from, and
// testRoots for the roots a status document is assembled from (#321).
const snapRoot = "/profiles/a"

var testRoots = map[string]string{schema.ToolClaudeCode: snapRoot, schema.ToolCodex: "/profiles/codex"}

func setCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", dir)
	return dir
}

func TestStatusRoundTripAndTTL(t *testing.T) {
	dir := setCacheDir(t)
	now := time.Now()

	if _, ok := ReadStatus(StatusTTL, now, testRoots); ok {
		t.Fatal("ReadStatus hit on empty cache")
	}
	s := &schema.Status{SchemaVersion: schema.Version, GeneratedAt: now.Format(time.RFC3339)}
	if err := WriteStatus(s, testRoots); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadStatus(StatusTTL, now, testRoots)
	if !ok || got.GeneratedAt != s.GeneratedAt {
		t.Fatalf("ReadStatus = %+v, %v", got, ok)
	}

	// Age the file past the TTL.
	old := now.Add(-StatusTTL - time.Second)
	path := filepath.Join(dir, "status.json")
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStatus(StatusTTL, now, testRoots); ok {
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
			if err := WriteStatus(s, testRoots); err != nil {
				t.Errorf("WriteStatus: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	readerWG.Wait()

	got, ok := ReadStatus(StatusTTL, now, testRoots)
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
		[]byte(`{"schema_version":"9.9","roots":{"claude-code":"`+snapRoot+`","codex":"/profiles/codex"},"generated_at":"x","tools":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadStatus(StatusTTL, time.Now(), testRoots); ok {
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
	if err := WriteSnapshot(tool, time.Time{}, snapRoot); err != nil {
		t.Fatal(err)
	}

	got, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot)
	if !ok || !got.Available {
		t.Fatalf("ReadSnapshot = %+v, %v", got, ok)
	}
	if got.Stale {
		t.Error("Stale = true for 2-minute-old snapshot")
	}

	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now.Add(SnapshotMaxAge+time.Minute), snapRoot); ok {
		t.Error("ReadSnapshot returned a snapshot older than maxAge")
	}

	// Returned within maxAge but past the stale threshold → Stale recomputed
	// to true. Age here is ~92min (> 60min threshold) but under the 2h maxAge.
	got, ok = ReadSnapshot(schema.ToolClaudeCode, 2*time.Hour, now.Add(90*time.Minute), snapRoot)
	if !ok || !got.Stale {
		t.Errorf("ReadSnapshot stale recompute: got %+v, %v", got, ok)
	}
}

// ReadSnapshotLimits ages limits from their original observation, not the
// snapshot's CollectedAt, so re-saved snapshots can't extend them (#186).
func TestReadSnapshotLimits(t *testing.T) {
	setCacheDir(t)
	now := time.Now().Truncate(time.Second)

	pct := 42.0
	collected := now.Add(-2 * time.Minute).Format(time.RFC3339)
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
	}
	observed := now.Add(-29 * 24 * time.Hour)
	if err := WriteSnapshot(tool, observed, snapRoot); err != nil {
		t.Fatal(err)
	}

	limits, backend, got, ok := ReadSnapshotLimits(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot)
	if !ok || len(limits) != 1 || backend != schema.BackendSubscription {
		t.Fatalf("ReadSnapshotLimits = %+v, %q, %v", limits, backend, ok)
	}
	if !got.Equal(observed) {
		t.Errorf("observed = %v, want %v", got, observed)
	}

	// The snapshot file is 2 minutes old, but the limits observation is 29
	// days old: 2 more days puts it past SnapshotMaxAge.
	if _, _, _, ok := ReadSnapshotLimits(schema.ToolClaudeCode, SnapshotMaxAge, now.Add(2*24*time.Hour), snapRoot); ok {
		t.Error("limits past maxAge from their observation were returned")
	}
}

// The display path must not show limits past their observation ceiling
// either: a fresh snapshot whose limits observation has expired keeps its
// other fields but drops the limits (#186).
func TestReadSnapshotDropsExpiredLimits(t *testing.T) {
	setCacheDir(t)
	now := time.Now().Truncate(time.Second)

	pct := 42.0
	collected := now.Add(-2 * time.Minute).Format(time.RFC3339)
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
	}
	if err := WriteSnapshot(tool, now.Add(-SnapshotMaxAge-time.Hour), snapRoot); err != nil {
		t.Fatal(err)
	}

	got, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot)
	if !ok || !got.Available {
		t.Fatalf("ReadSnapshot = %+v, %v (a fresh snapshot must still be served)", got, ok)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %+v, want nil past the observation ceiling", got.Limits)
	}
	if got.Backend != schema.BackendSubscription {
		t.Errorf("Backend = %q, want the other fields preserved", got.Backend)
	}
}

// Snapshots written before limits_collected_at existed (or with an unknown
// observation) fall back to CollectedAt as the observation time.
func TestReadSnapshotLimitsFallsBackToCollectedAt(t *testing.T) {
	setCacheDir(t)
	now := time.Now().Truncate(time.Second)

	pct := 42.0
	collectedTime := now.Add(-time.Hour)
	collected := collectedTime.Format(time.RFC3339)
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
	}
	if err := WriteSnapshot(tool, time.Time{}, snapRoot); err != nil {
		t.Fatal(err)
	}

	_, _, got, ok := ReadSnapshotLimits(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot)
	if !ok {
		t.Fatal("ReadSnapshotLimits = not ok, want CollectedAt fallback")
	}
	if got.Format(time.RFC3339) != collected {
		t.Errorf("observed = %v, want CollectedAt %s", got, collected)
	}
}

func TestReadSnapshotLimitsWithoutLimits(t *testing.T) {
	setCacheDir(t)
	now := time.Now()

	collected := now.Format(time.RFC3339)
	tool := schema.Unavailable(schema.ToolClaudeCode)
	tool.Available = true
	tool.CollectedAt = &collected
	if err := WriteSnapshot(tool, time.Time{}, snapRoot); err != nil {
		t.Fatal(err)
	}

	if _, _, _, ok := ReadSnapshotLimits(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot); ok {
		t.Error("ReadSnapshotLimits returned ok for a snapshot without limits")
	}
}

func TestSnapshotRejectsOtherSchemaVersion(t *testing.T) {
	dir := setCacheDir(t)
	now := time.Now()
	collected := now.Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json"),
		[]byte(`{"schema_version":"9.9","root":"`+snapRoot+`","tool":"claude-code","available":true,"collected_at":"`+collected+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot); ok {
		t.Error("ReadSnapshot accepted a different schema_version")
	}
}

// A snapshot is tied to the config root it was observed from: a reader
// working against another root (a second Claude profile) gets neither the
// snapshot nor limits to carry from it (#321). Roots compare as paths.
func TestSnapshotRootMismatch(t *testing.T) {
	setCacheDir(t)
	now := time.Now().Truncate(time.Second)
	pct := 42.0
	collected := now.Add(-time.Minute).Format(time.RFC3339)
	tool := schema.Tool{
		Tool:        schema.ToolClaudeCode,
		Available:   true,
		Backend:     schema.BackendSubscription,
		CollectedAt: &collected,
		Limits:      []schema.Limit{{Window: schema.WindowFiveHour, UsedPct: &pct}},
	}
	if err := WriteSnapshot(tool, now.Add(-time.Minute), snapRoot); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot+"/"); !ok {
		t.Error("ReadSnapshot = not ok for the same root spelled with a trailing slash")
	}
	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, "/profiles/b"); ok {
		t.Error("ReadSnapshot served a snapshot observed from another root")
	}
	if _, _, _, ok := ReadSnapshotLimits(schema.ToolClaudeCode, SnapshotMaxAge, now, "/profiles/b"); ok {
		t.Error("ReadSnapshotLimits offered limits observed from another root")
	}
}

// Roots are stored absolute, resolved against the writer's directory: a
// reader in that directory matches under any spelling, a reader elsewhere
// matches only the writer's absolute path, never the same relative spelling
// (#321).
func TestSnapshotRootIsStoredAbsolute(t *testing.T) {
	setCacheDir(t)
	now := time.Now().Truncate(time.Second)
	collected := now.Add(-time.Minute).Format(time.RFC3339)
	tool := schema.Tool{Tool: schema.ToolClaudeCode, Available: true, CollectedAt: &collected}

	writerDir, otherDir := t.TempDir(), t.TempDir()
	t.Chdir(writerDir)
	if err := WriteSnapshot(tool, time.Time{}, "profiles/rel"); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd() // as the writer resolved it (symlinks included)
	absRoot := filepath.Join(cwd, "profiles", "rel")
	for _, same := range []string{"profiles/rel", "./profiles/rel/", absRoot} {
		if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, same); !ok {
			t.Errorf("ReadSnapshot = not ok for %q from the writer's directory", same)
		}
	}

	t.Chdir(otherDir)
	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, "profiles/rel"); ok {
		t.Error("ReadSnapshot matched the same relative spelling from another directory")
	}
	if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, absRoot); !ok {
		t.Error("ReadSnapshot = not ok for the writer's absolute root from another directory")
	}
}

// A root that can't be made absolute (the current directory is gone) is
// stored as none and matches nothing — not even the same spelling from the
// same broken directory — rather than falling back to a relative path that
// another reader would resolve against its own directory.
func TestSnapshotUnresolvableRootNeverMatches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the current directory can't be removed on Windows")
	}
	setCacheDir(t)
	now := time.Now().Truncate(time.Second)
	collected := now.Add(-time.Minute).Format(time.RFC3339)
	tool := schema.Tool{Tool: schema.ToolClaudeCode, Available: true, CollectedAt: &collected}

	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Abs("profiles/rel"); err == nil {
		t.Skip("filepath.Abs still resolves a relative path from a removed directory here")
	}
	if err := WriteSnapshot(tool, time.Time{}, "profiles/rel"); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"profiles/rel", "/profiles/rel", ""} {
		if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, root); ok {
			t.Errorf("ReadSnapshot served a snapshot whose root couldn't be resolved to root %q", root)
		}
	}
}

// A snapshot without a recorded root (written before #321) and a reader
// whose root couldn't be resolved never match — not even each other, and not
// a root spelled ".", which Clean would otherwise equate with "".
func TestSnapshotWithoutRootNeverMatches(t *testing.T) {
	dir := setCacheDir(t)
	now := time.Now().Truncate(time.Second)
	collected := now.Add(-time.Minute).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json"),
		[]byte(`{"schema_version":"`+schema.Version+`","tool":"claude-code","available":true,"backend":"subscription","collected_at":"`+collected+`","limits":[{"window":"5h","used_pct":42}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{snapRoot, ".", ""} {
		if _, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, root); ok {
			t.Errorf("ReadSnapshot served a root-less snapshot to root %q", root)
		}
		if _, _, _, ok := ReadSnapshotLimits(schema.ToolClaudeCode, SnapshotMaxAge, now, root); ok {
			t.Errorf("ReadSnapshotLimits offered a root-less snapshot's limits to root %q", root)
		}
	}
}

// The TTL cache is keyed by the roots the document was assembled from: a run
// against other roots (another profile) misses and assembles its own (#321).
func TestStatusCacheNotSharedAcrossRoots(t *testing.T) {
	setCacheDir(t)
	now := time.Now()
	s := &schema.Status{SchemaVersion: schema.Version, GeneratedAt: now.Format(time.RFC3339)}
	if err := WriteStatus(s, testRoots); err != nil {
		t.Fatal(err)
	}
	same := map[string]string{schema.ToolClaudeCode: snapRoot + "/", schema.ToolCodex: "/profiles/codex"}
	if _, ok := ReadStatus(StatusTTL, now, same); !ok {
		t.Error("ReadStatus missed for the same roots")
	}
	other := map[string]string{schema.ToolClaudeCode: "/profiles/b", schema.ToolCodex: "/profiles/codex"}
	if _, ok := ReadStatus(StatusTTL, now, other); ok {
		t.Error("ReadStatus served a document assembled from another Claude root")
	}
	if _, ok := ReadStatus(StatusTTL, now, map[string]string{schema.ToolClaudeCode: snapRoot}); ok {
		t.Error("ReadStatus served a document to a run that resolved fewer roots")
	}
	if _, ok := ReadStatus(StatusTTL, now, nil); ok {
		t.Error("ReadStatus served a document to a run with no resolved roots")
	}
}

// The TTL cache's roots are stored absolute too: the same relative roots hit
// from the writer's directory, miss from another, and the writer's absolute
// roots hit from anywhere (#321).
func TestStatusCacheRootsStoredAbsolute(t *testing.T) {
	setCacheDir(t)
	now := time.Now()
	s := &schema.Status{SchemaVersion: schema.Version, GeneratedAt: now.Format(time.RFC3339)}
	rel := map[string]string{schema.ToolClaudeCode: "profiles/claude", schema.ToolCodex: "profiles/codex"}

	writerDir, otherDir := t.TempDir(), t.TempDir()
	t.Chdir(writerDir)
	if err := WriteStatus(s, rel); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	abs := map[string]string{
		schema.ToolClaudeCode: filepath.Join(cwd, "profiles", "claude"),
		schema.ToolCodex:      filepath.Join(cwd, "profiles", "codex"),
	}
	if _, ok := ReadStatus(StatusTTL, now, rel); !ok {
		t.Error("ReadStatus missed for the same relative roots from the writer's directory")
	}

	t.Chdir(otherDir)
	if _, ok := ReadStatus(StatusTTL, now, rel); ok {
		t.Error("ReadStatus hit for the same relative spelling from another directory")
	}
	if _, ok := ReadStatus(StatusTTL, now, abs); !ok {
		t.Error("ReadStatus missed for the writer's absolute roots from another directory")
	}
}

// The daily-history cache round-trips under its key and is a miss for another
// key or schema version (a release or price change starts it over).
func TestDailyHistoryRoundTripAndKey(t *testing.T) {
	dir := setCacheDir(t)
	if _, ok := ReadDailyHistory("k1"); ok {
		t.Fatal("ReadDailyHistory hit on empty cache")
	}
	cost := 1.5
	at := "2026-07-04T09:00:00+09:00"
	days := map[string]map[string]DailyHistoryEntry{
		"2026-07-03": {
			schema.ToolClaudeCode: {Daily: &schema.Daily{Tokens: 100, CostUSD: &cost}, CheckedAt: at},
			schema.ToolCodex:      {CheckedAt: at}, // unknown when checked
		},
	}
	if err := WriteDailyHistory("k1", days); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadDailyHistory("k1")
	claude, codex := got["2026-07-03"][schema.ToolClaudeCode], got["2026-07-03"][schema.ToolCodex]
	if !ok || claude.Daily == nil || claude.Daily.Tokens != 100 || *claude.Daily.CostUSD != 1.5 || claude.CheckedAt != at || codex.Daily != nil || codex.CheckedAt != at {
		t.Fatalf("ReadDailyHistory = %+v, %v", got, ok)
	}
	if _, ok := ReadDailyHistory("k2"); ok {
		t.Error("ReadDailyHistory hit for another key")
	}

	// Another schema version on disk is a miss.
	path := filepath.Join(dir, "daily-history.json")
	b, _ := os.ReadFile(path)
	b = []byte(strings.Replace(string(b), `"schema_version":"`+schema.Version+`"`, `"schema_version":"0.1"`, 1))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadDailyHistory("k1"); ok {
		t.Error("ReadDailyHistory hit for another schema version")
	}
}
