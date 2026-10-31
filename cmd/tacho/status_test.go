package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/schema"
)

// tacho status --json is the way out of the public JSON contract
// (docs/schema.md): one JSON document alone on stdout, from the TTL cache
// unless --no-cache, and exit 2 without --json (#445).
func TestRunStatusJSON(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	if err := cache.WriteStatus(&schema.Status{
		SchemaVersion: schema.Version,
		GeneratedAt:   "cached-status",
		Tools:         []schema.Tool{{Tool: schema.ToolCodex, Available: true}},
	}, core.Roots(core.Options{})); err != nil {
		t.Fatal(err)
	}
	stampStatusCache(t, time.Now()) // fresh: the cache answers unless bypassed

	status := func(args ...string) (int, schema.Status) {
		t.Helper()
		var code int
		out := capture(t, &os.Stdout, func() { code = run(append([]string{"status"}, args...)) })
		dec := json.NewDecoder(strings.NewReader(out))
		var s schema.Status
		if err := dec.Decode(&s); err != nil {
			t.Fatalf("status %q: stdout isn't JSON: %v\n%s", args, err, out)
		}
		var rest json.RawMessage
		if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
			t.Fatalf("status %q: more on stdout after the JSON document (%v)\n%s", args, err, out)
		}
		return code, s
	}

	if code, s := status("--json"); code != 0 || s.GeneratedAt != "cached-status" {
		t.Errorf("status --json = %d, generated_at %q; want 0 and the cached document", code, s.GeneratedAt)
	}
	code, s := status("--json", "--no-cache")
	if code != 0 || s.GeneratedAt == "cached-status" || s.SchemaVersion != schema.Version {
		t.Errorf("status --json --no-cache = %d, generated_at %q, schema %q; want 0 and a fresh document of schema %s",
			code, s.GeneratedAt, s.SchemaVersion, schema.Version)
	}

	var stdout string
	stderr := capture(t, &os.Stderr, func() {
		stdout = capture(t, &os.Stdout, func() { code = run([]string{"status"}) })
	})
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--json") {
		t.Errorf("status without --json = %d, stdout %q, stderr %q; want 2, nothing on stdout, and the --json hint", code, stdout, stderr)
	}
}
