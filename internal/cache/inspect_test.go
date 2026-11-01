package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/schema"
)

// InspectSnapshot names each reason a snapshot isn't served, and agrees with
// ReadSnapshot on every case: served exactly when fresh or stale, stale
// exactly when the report says so (#379).
func TestInspectSnapshot(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	snapFile := func(dir string) string { return filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json") }
	raw := func(body string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			if err := os.WriteFile(snapFile(dir), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	written := func(root string, age time.Duration) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			collected := at(age)
			tool := schema.Tool{Tool: schema.ToolClaudeCode, Available: true, CollectedAt: &collected}
			if err := WriteSnapshot(tool, time.Time{}, root); err != nil {
				t.Fatal(err)
			}
		}
	}
	current := `"schema_version":"` + schema.Version + `","root":"` + snapRoot + `","tool":"claude-code","available":true`
	stored, _ := NormalizeRoot(snapRoot)
	other, _ := NormalizeRoot("/profiles/b")

	type inspectCase struct {
		name   string
		seed   func(t *testing.T, dir string)
		state  SnapshotState
		schema string // SchemaVersion, checked from SnapshotOldSchema on
		root   string // Root, checked from SnapshotOtherRoot on
		age    time.Duration
	}
	cases := []inspectCase{
		{name: "missing", seed: func(*testing.T, string) {}, state: SnapshotMissing},
		{name: "unreadable", seed: func(t *testing.T, dir string) {
			if err := os.Mkdir(snapFile(dir), 0o755); err != nil {
				t.Fatal(err)
			}
		}, state: SnapshotUnreadable},
		{name: "not JSON", seed: raw(`{`), state: SnapshotInvalid},
		{name: "other schema", seed: raw(`{"schema_version":"9.9","root":"` + snapRoot + `","collected_at":"` + at(time.Minute) + `"}`),
			state: SnapshotOldSchema, schema: "9.9"},
		{name: "other root", seed: written("/profiles/b", time.Minute), state: SnapshotOtherRoot, schema: schema.Version, root: other},
		{name: "no root recorded", seed: written("", time.Minute), state: SnapshotOtherRoot, schema: schema.Version, root: ""},
		{name: "no collected_at", seed: raw(`{` + current + `}`), state: SnapshotInvalid, schema: schema.Version, root: snapRoot},
		{name: "bad collected_at", seed: raw(`{` + current + `,"collected_at":"yesterday"}`), state: SnapshotInvalid,
			schema: schema.Version, root: snapRoot},
		{name: "fresh", seed: written(snapRoot, 2*time.Minute), state: SnapshotFresh,
			schema: schema.Version, root: stored, age: 2 * time.Minute},
	}
	// Ages at and around both thresholds: the stale threshold is the last age
	// still fresh and maxAge the last still served (both compare with >).
	staleAfter := schema.StaleAfterMinutes * time.Minute
	for _, b := range []struct {
		name  string
		age   time.Duration
		state SnapshotState
	}{
		{"just under the stale threshold", staleAfter - time.Second, SnapshotFresh},
		{"at the stale threshold", staleAfter, SnapshotFresh},
		{"just over the stale threshold", staleAfter + time.Second, SnapshotStale},
		{"just under maxAge", SnapshotMaxAge - time.Second, SnapshotStale},
		{"at maxAge", SnapshotMaxAge, SnapshotStale},
		{"just over maxAge", SnapshotMaxAge + time.Second, SnapshotExpired},
		// A collected_at after now: within the clock slack it is served, past
		// it the clock was set back and the snapshot is not used (#438).
		{"after now within the clock slack", -statusClockSlack, SnapshotFresh},
		{"after now past the clock slack", -statusClockSlack - time.Second, SnapshotFuture},
	} {
		cases = append(cases, inspectCase{name: b.name, seed: written(snapRoot, b.age), state: b.state,
			schema: schema.Version, root: stored, age: b.age})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := setCacheDir(t)
			c.seed(t, dir)
			r := InspectSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot)
			if r.State != c.state {
				t.Fatalf("State = %q, want %q (report %+v)", r.State, c.state, r)
			}
			if (r.Err != nil) != (c.state == SnapshotUnreadable) {
				t.Errorf("Err = %v; want one only for an unreadable file", r.Err)
			}
			if r.SchemaVersion != c.schema {
				t.Errorf("SchemaVersion = %q, want %q", r.SchemaVersion, c.schema)
			}
			if r.Root != c.root {
				t.Errorf("Root = %q, want %q", r.Root, c.root)
			}
			var want time.Time // none until the snapshot's age is judged
			if c.age != 0 {
				want = now.Add(-c.age)
			}
			if !r.CollectedAt.Equal(want) {
				t.Errorf("CollectedAt = %v, want %v", r.CollectedAt, want)
			}

			got, ok := ReadSnapshot(schema.ToolClaudeCode, SnapshotMaxAge, now, snapRoot)
			if served := c.state == SnapshotFresh || c.state == SnapshotStale; ok != served {
				t.Fatalf("ReadSnapshot ok = %v for a %s snapshot; want %v", ok, c.state, served)
			}
			if ok && got.Stale != (c.state == SnapshotStale) {
				t.Errorf("ReadSnapshot Stale = %v for a %s snapshot", got.Stale, c.state)
			}
		})
	}
}
