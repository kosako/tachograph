package main

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/schema"
)

func TestWatchStatusBypassesTTLCache(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())

	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := cache.WriteStatus(&schema.Status{
		SchemaVersion: schema.Version,
		GeneratedAt:   "cached-status",
		Tools:         []schema.Tool{{Tool: schema.ToolCodex, Available: true}},
	}, core.Roots(core.Options{})); err != nil {
		t.Fatal(err)
	}
	stampStatusCache(t, now) // fresh at now: only bypassing the cache keeps it out

	got, _ := watchStatus(now)
	if got.GeneratedAt == "cached-status" {
		t.Fatal("watchStatus used the TTL cache")
	}
}

// -n is a whole number of seconds: below one it runs every second, and past
// a day it is refused, as such a value is most likely mistyped and, far past
// it, overflows time.Duration into a negative sleep that redraws without
// pause (#415).
func TestWatchInterval(t *testing.T) {
	for _, c := range []struct {
		sec  int
		want time.Duration
	}{
		{-5, time.Second},
		{0, time.Second},
		{1, time.Second},
		{5, 5 * time.Second},
		{maxWatchInterval, 24 * time.Hour},
	} {
		if got, err := watchInterval(c.sec); err != nil || got != c.want {
			t.Errorf("watchInterval(%d) = %v, %v; want %v", c.sec, got, err, c.want)
		}
	}
	for _, sec := range []int{maxWatchInterval + 1, math.MaxInt} {
		if got, err := watchInterval(sec); err == nil {
			t.Errorf("watchInterval(%d) = %v, want an error", sec, got)
		}
	}
}

// `tacho watch -n` past a day exits 2 with the reason before drawing
// anything, rather than redrawing without pause. The dirs a draw would read
// are temp dirs.
func TestRunWatchRefusesIntervalPastADay(t *testing.T) {
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	var code int
	var stdout string
	stderr := capture(t, &os.Stderr, func() {
		stdout = capture(t, &os.Stdout, func() {
			code = runWatch([]string{"-n", strconv.Itoa(math.MaxInt)})
		})
	})
	if code != 2 {
		t.Errorf("runWatch exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "-n must be at most 86400") {
		t.Errorf("stderr = %q, want the limit", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing drawn", stdout)
	}
}
