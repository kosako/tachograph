package main

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/schema"
)

// menubar.history=hide skips the history pass of the SwiftBar tick — no
// daily-history.json is read or written and the dropdown has no history
// section — while show (the default) computes and caches it (#302). The
// agents' data dirs are empty temp dirs, so no real transcripts are read;
// the cache and config dirs are temporary too.
func TestRunSwiftbarHistoryHiddenSkipsCache(t *testing.T) {
	cacheDir, cfgDir := t.TempDir(), t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", cacheDir)
	t.Setenv("TACHO_CONFIG_DIR", cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("SWIFTBAR_PLUGIN_PATH", "") // not under SwiftBar: no notifications
	t.Setenv("TACHO_SWIFTBAR_TEXT", "1")
	histFile := filepath.Join(cacheDir, "daily-history.json")

	tick := func(visibility string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"menubar":{"history":"`+visibility+`"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		return capture(t, &os.Stdout, func() {
			if code := runSwiftbar(nil); code != 0 {
				t.Errorf("runSwiftbar with history %s = %d, want 0", visibility, code)
			}
		})
	}

	out := tick("hide")
	if _, err := os.Stat(histFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("daily-history.json exists after a tick with the history hidden (stat err: %v)", err)
	}
	if strings.Contains(out, "日の cost/tokens") || strings.Contains(out, "日計") {
		t.Errorf("history section rendered with the history hidden:\n%s", out)
	}
	if !strings.Contains(out, "----☐ 直近 7 日 | bash=") {
		t.Errorf("sections menu missing the unchecked history entry:\n%s", out)
	}

	out = tick("show")
	if _, err := os.Stat(histFile); err != nil {
		t.Errorf("daily-history.json not written after a tick with the history shown: %v", err)
	}
	if !strings.Contains(out, "日の cost/tokens") || !strings.Contains(out, "----☑ 直近 7 日 | bash=") {
		t.Errorf("history section or its checked entry missing with the history shown:\n%s", out)
	}
}

// notifyTick runs the notification pass of one SwiftBar tick, with
// configJSON as config.json and SWIFTBAR_PLUGIN_PATH set to plugin, and
// returns how often the record's lock was taken and the URLs sent. Claude's
// 5h window is at 20% left. The cache, config, and agents' dirs are temp
// dirs, and the lock and the sender are stand-ins: no real notification is
// sent.
func notifyTick(t *testing.T, configJSON, plugin string) (locks int, sent []string) {
	t.Helper()
	cfgDir, home := t.TempDir(), t.TempDir()
	t.Setenv("TACHO_CACHE_DIR", t.TempDir())
	t.Setenv("TACHO_CONFIG_DIR", cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home os.UserHomeDir reads on Windows
	t.Setenv("SWIFTBAR_PLUGIN_PATH", plugin)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	used := 80.0
	shown := schema.Status{Tools: []schema.Tool{{Tool: schema.ToolClaudeCode, Available: true, Limits: []schema.Limit{
		{Window: schema.WindowFiveHour, UsedPct: &used},
	}}}}
	lock := func() (func(), error) { locks++; return func() {}, nil }
	send := func(u string) error { sent = append(sent, u); return nil }
	notifyLimits(shown, config.Load(), time.Now(), lock, send)
	return locks, sent
}

// pluginFile writes a stand-in SwiftBar plugin and returns its path.
func pluginFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tacho.30s.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Under SwiftBar with thresholds set, a crossing is sent naming the plugin by
// its full, symlink-resolved path, the plugin id every SwiftBar version
// matches: 2.1.1 silently drops a notification naming it any other way. The
// expected path is resolved too, as the temp dir itself may sit behind a
// symlink (/var on macOS).
func TestNotifyLimitsNamesResolvedPlugin(t *testing.T) {
	const thresholds = `{"notify":{"thresholds":[50]}}`
	check := func(t *testing.T, plugin, target string) {
		t.Helper()
		want, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatal(err)
		}
		locks, sent := notifyTick(t, thresholds, plugin)
		if locks != 1 || len(sent) != 1 {
			t.Fatalf("locks %d, sent %q, want one pass sending one notification", locks, sent)
		}
		u, err := url.Parse(sent[0])
		if err != nil {
			t.Fatal(err)
		}
		if got := u.Query().Get("plugin"); got != want {
			t.Errorf("notification names plugin %q, want %q (URL %q)", got, want, sent[0])
		}
	}
	t.Run("plugin file", func(t *testing.T) {
		p := pluginFile(t)
		check(t, p, p)
	})
	t.Run("symlinked plugin", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs privileges on windows")
		}
		target := pluginFile(t)
		link := filepath.Join(t.TempDir(), "tacho.30s.sh")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		check(t, link, target)
	})
}

// Outside SwiftBar (no SWIFTBAR_PLUGIN_PATH) or without thresholds the tick
// skips the notification pass entirely: nothing is sent, and the record's
// lock isn't even taken, so a plugin that doesn't notify never touches the
// record.
func TestNotifyLimitsNeedsPluginAndThresholds(t *testing.T) {
	for _, c := range []struct {
		name, configJSON string
		underSwiftBar    bool
	}{
		{"no thresholds", `{}`, true},
		{"empty thresholds", `{"notify":{"thresholds":[]}}`, true},
		{"not under SwiftBar", `{"notify":{"thresholds":[50]}}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			plugin := ""
			if c.underSwiftBar {
				plugin = pluginFile(t)
			}
			if locks, sent := notifyTick(t, c.configJSON, plugin); locks != 0 || len(sent) != 0 {
				t.Errorf("locks %d, sent %q, want no pass", locks, sent)
			}
		})
	}
}
