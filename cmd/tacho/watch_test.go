package main

import (
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/cache"
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
	}); err != nil {
		t.Fatal(err)
	}

	got := watchStatus(now)
	if got.GeneratedAt == "cached-status" {
		t.Fatal("watchStatus used the TTL cache")
	}
}
