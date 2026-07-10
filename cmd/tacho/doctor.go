package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/cmuxbar"
	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

func runDoctor(args []string) int {
	exe := resolveExe()
	onPath := tachoOnPath()
	now := time.Now()

	fmt.Println("tachograph doctor")
	fmt.Println()

	fmt.Println("binary:")
	fmt.Println("  version:   " + buildVersion())
	if exe != "" {
		fmt.Println("  running:   " + exe)
	} else {
		fmt.Println("  running:   (unknown)")
	}
	if onPath {
		if p, err := exec.LookPath("tacho"); err == nil {
			fmt.Println("  on PATH:   yes (" + p + ")")
			if exe != "" && !sameExecutable(p, exe) {
				fmt.Println("  warning:   the `tacho` on PATH is a different binary than the one running")
			}
		} else {
			fmt.Println("  on PATH:   yes")
		}
	} else {
		fmt.Println("  on PATH:   no — `tacho` won't resolve; use an absolute path")
		if gobin := goBin(); gobin != "" {
			fmt.Println("  go bin:    " + gobin + "  (add to PATH, or this is where `go install` put it)")
		}
	}
	fmt.Println()

	fmt.Println("config (" + config.Dir() + "):")
	reportFile("config.json", filepath.Join(config.Dir(), "config.json"))
	reportFile("statusline.tmpl", filepath.Join(config.Dir(), "statusline.tmpl"))
	reportFile("pricing.json", filepath.Join(config.Dir(), "pricing.json"))
	fmt.Println()

	reportDataSources(now)
	fmt.Println()

	reportCache(now)
	fmt.Println()

	reportIntegrations()
	fmt.Println()

	reportCurrentStatus(now)
	fmt.Println()

	path := claudeSettingsPath()
	fmt.Println("Claude Code statusLine (" + path + "):")
	switch cmd := claudeStatusLineCommand(path); {
	case cmd == "(missing)":
		fmt.Println("  not configured — run `tacho setup claude --write`")
	case cmd == "(no statusLine)":
		fmt.Println("  settings.json exists but has no statusLine — run `tacho setup claude --write`")
	case cmd == "(unreadable)":
		fmt.Println("  settings.json is not valid JSON — fix it, then `tacho setup claude`")
	default:
		fmt.Println("  command:   " + cmd)
		if !statusLineResolves(cmd) {
			fmt.Println("  warning:   that command does not resolve — re-run `tacho setup claude --write`")
		}
	}
	return 0
}

func reportDataSources(now time.Time) {
	fmt.Println("data sources:")
	if root, ok := agentpath.ClaudeRoot(""); ok {
		reportJSONLTree("Claude projects", filepath.Join(root, "projects"), now,
			"run Claude Code once, or set CLAUDE_CONFIG_DIR")
	} else {
		fmt.Println("  Claude projects: unavailable — cannot locate the home directory")
	}
	if root, ok := agentpath.CodexRoot(""); ok {
		reportJSONLTree("Codex sessions", filepath.Join(root, "sessions"), now,
			"run Codex once, or set CODEX_HOME")
	} else {
		fmt.Println("  Codex sessions:  unavailable — cannot locate the home directory")
	}
}

func reportJSONLTree(label, path string, now time.Time, hint string) {
	newest, count, err := newestJSONL(path)
	key := fmt.Sprintf("%s:", label)
	switch {
	case err != nil:
		if os.IsNotExist(err) {
			fmt.Printf("  %-16s missing (%s) — %s\n", key, path, hint)
			return
		}
		fmt.Printf("  %-16s unreadable (%s): %v\n", key, path, err)
	case count == 0:
		fmt.Printf("  %-16s present (%s), but no .jsonl files — %s\n", key, path, hint)
	default:
		fmt.Printf("  %-16s present (%d .jsonl, newest %s)\n", key, count, doctorAge(newest, now))
	}
}

func newestJSONL(root string) (time.Time, int, error) {
	if _, err := os.Stat(root); err != nil {
		return time.Time{}, 0, err
	}
	var newest time.Time
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		count++
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, count, err
}

func reportCache(now time.Time) {
	fmt.Println("cache:")
	dir, err := cache.Dir()
	if err != nil {
		fmt.Println("  dir:       unavailable — " + err.Error())
		return
	}
	fmt.Println("  dir:       " + dir)
	reportCacheFile("status.json", filepath.Join(dir, "status.json"), cache.StatusTTL, 0, now,
		"run `tacho` or `tacho status --json` once")
	reportCacheFile("Claude snapshot", filepath.Join(dir, "snapshot-"+schema.ToolClaudeCode+".json"),
		cache.SnapshotMaxAge, schema.StaleAfterMinutes*time.Minute, now,
		"run Claude Code with `tacho statusline` configured")
}

func reportCacheFile(label, path string, maxAge, staleAfter time.Duration, now time.Time, hint string) {
	info, err := os.Stat(path)
	key := fmt.Sprintf("%s:", label)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("  %-16s missing — %s\n", key, hint)
			return
		}
		fmt.Printf("  %-16s unreadable: %v\n", key, err)
		return
	}

	age := now.Sub(info.ModTime())
	state := "fresh"
	switch {
	case maxAge > 0 && age > maxAge:
		state = "expired"
	case staleAfter > 0 && age > staleAfter:
		state = "stale but usable"
	}
	fmt.Printf("  %-16s present (modified %s, %s)\n", key, doctorAge(info.ModTime(), now), state)
}

func reportIntegrations() {
	fmt.Println("integrations:")
	if cli := cmuxbar.FindCLI(); cli != "" {
		fmt.Println("  cmux CLI:   " + cli)
	} else {
		fmt.Println("  cmux CLI:   not found — install cmux or set TACHO_CMUX_BIN")
	}
	if cmuxbar.Detect() {
		fmt.Println("  cmux env:   inside a cmux workspace")
	} else {
		fmt.Println("  cmux env:   not inside cmux")
	}
	reportSwiftBarPlugin()
}

func reportSwiftBarPlugin() {
	for _, path := range swiftBarPluginCandidates() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fmt.Println("  SwiftBar:   plugin found (" + path + ")")
			return
		}
	}
	fmt.Println("  SwiftBar:   plugin not found in common folders — copy contrib/tacho.30s.sh to your SwiftBar plugin folder")
}

func swiftBarPluginCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	for _, env := range []string{"SWIFTBAR_PLUGIN_DIR", "SWIFTBAR_PLUGIN_PATH", "XBAR_PLUGIN_DIR", "XBAR_PLUGIN_PATH"} {
		if d := os.Getenv(env); d != "" {
			add(filepath.Join(d, "tacho.30s.sh"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, "Library", "Application Support", "SwiftBar", "Plugins", "tacho.30s.sh"))
		add(filepath.Join(home, "Library", "Application Support", "xbar", "plugins", "tacho.30s.sh"))
	}
	return out
}

func reportCurrentStatus(now time.Time) {
	fmt.Println("current status:")
	s := core.Status(core.Options{Now: now, NoCache: true})
	for _, t := range s.Tools {
		reportToolStatus(t, now)
	}
}

func reportToolStatus(t schema.Tool, now time.Time) {
	label := doctorToolName(t.Tool) + ":"
	switch {
	case !t.Available:
		fmt.Printf("  %-8s not found — %s\n", label, doctorUnavailableHint(t.Tool))
	case t.Error != nil:
		fmt.Printf("  %-8s error %s — %s\n", label, t.Error.Code, doctorErrorHint(t.Tool, t.Error.Code))
	default:
		status := "ok"
		if t.Stale && t.CollectedAt != nil {
			if age := render.Age(*t.CollectedAt, now); age != "" {
				status = "ok, but stale (" + age + ")"
			}
		}
		fmt.Printf("  %-8s %s\n", label, status)
	}
}

func doctorToolName(tool string) string {
	if tool == schema.ToolClaudeCode {
		return "Claude"
	}
	if tool == schema.ToolCodex {
		return "Codex"
	}
	return tool
}

func doctorUnavailableHint(tool string) string {
	switch tool {
	case schema.ToolClaudeCode:
		return "run Claude Code once, or check CLAUDE_CONFIG_DIR"
	case schema.ToolCodex:
		return "run Codex once, or check CODEX_HOME"
	default:
		return "check the tool installation and data directory"
	}
}

func doctorErrorHint(tool, code string) string {
	switch code {
	case "home_dir":
		return "check HOME, CLAUDE_CONFIG_DIR, or CODEX_HOME"
	case "read_error":
		return "check log file permissions and retry"
	}
	if tool == schema.ToolClaudeCode {
		switch code {
		case "statusline_parse":
			return "Claude Code sent invalid statusLine JSON; re-run `tacho setup claude --write`"
		case "no_usage":
			return "send one Claude Code message so the transcript has usage entries"
		}
	}
	if tool == schema.ToolCodex {
		switch code {
		case "no_token_count":
			return "run a Codex turn that writes a token_count event under CODEX_HOME/sessions"
		}
	}
	return "run `tacho status --json` for the full error message"
}

func doctorAge(ts, now time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := now.Sub(ts)
	if d < 0 {
		return "in the future"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func reportFile(label, path string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Println("  " + label + ":  present")
	} else {
		fmt.Println("  " + label + ":  (default)")
	}
}
