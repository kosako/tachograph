package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

func runDoctor(args []string) int {
	// doctor takes no flags; -h used to be ignored and ran the diagnosis.
	if len(args) > 0 && isHelpFlag(args[0]) {
		fmt.Print(usage)
		return 0
	}
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
			switch {
			case exe == "" || sameExecutable(p, exe):
				fmt.Println("  on PATH:   yes (" + p + ")")
			case sameInstall(p, exe):
				fmt.Println("  on PATH:   yes (" + p + ", the npm launcher for this binary)")
			default:
				fmt.Println("  on PATH:   yes (" + p + ")")
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
	reportJSONFile("config.json", filepath.Join(config.Dir(), "config.json"), config.Validate)
	for _, w := range configValueWarnings() {
		fmt.Println("    warning:  " + w)
	}
	reportFile("statusline.tmpl", filepath.Join(config.Dir(), "statusline.tmpl"))
	reportJSONFile("pricing.json", filepath.Join(config.Dir(), "pricing.json"), pricing.Validate)
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
		warning, note := statusLineWarning(cmd, exe)
		if warning != "" {
			fmt.Println("  warning:   " + warning)
		}
		if note != "" {
			fmt.Println("  note:      " + note)
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
	reportCacheFile("status.json", filepath.Join(dir, "status.json"), cache.StatusTTL, now,
		"run `tacho` or `tacho status --json` once")
	reportSnapshot(now)
}

func reportCacheFile(label, path string, maxAge time.Duration, now time.Time, hint string) {
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

	// Judged as the reader does: a file modified in the future (the clock
	// was set back since) isn't served however recent it looks (#415).
	state := "fresh"
	if !cache.StatusFresh(info.ModTime(), now, maxAge) {
		state = "expired"
		if info.ModTime().After(now) {
			state = "not used"
		}
	}
	fmt.Printf("  %-16s present (modified %s, %s)\n", key, doctorAge(info.ModTime(), now), state)
}

// reportSnapshot reports the Claude snapshot as the readers judge it, by the
// same checks (cache.InspectSnapshot): a snapshot written for another config
// dir or schema version is there but never used, which its modification
// time can't tell (#379). Its age is its collected_at, as the readers use.
func reportSnapshot(now time.Time) {
	const key = "Claude snapshot:"
	const rewrite = "the next `tacho statusline` run rewrites it"
	root := core.Roots(core.Options{})[schema.ToolClaudeCode] // the root the readers use (#321)
	r := cache.InspectSnapshot(schema.ToolClaudeCode, cache.SnapshotMaxAge, now, root)
	switch r.State {
	case cache.SnapshotMissing:
		fmt.Printf("  %-16s missing — run Claude Code with `tacho statusline` configured\n", key)
	case cache.SnapshotUnreadable:
		fmt.Printf("  %-16s unreadable: %v\n", key, r.Err)
	case cache.SnapshotInvalid:
		fmt.Printf("  %-16s present but not used — not a valid snapshot; %s\n", key, rewrite)
	case cache.SnapshotOldSchema:
		fmt.Printf("  %-16s present but not used — written for schema %q, this tacho reads %s; %s\n",
			key, r.SchemaVersion, schema.Version, rewrite)
	case cache.SnapshotOtherRoot:
		// Root mismatch covers three cases, told apart so the line doesn't
		// claim another config dir when one side has none to compare.
		switch here, ok := cache.NormalizeRoot(root); {
		case !ok:
			fmt.Printf("  %-16s present but not used — this run's Claude config dir can't be resolved\n", key)
		case r.Root == "":
			fmt.Printf("  %-16s present but not used — it records no Claude config dir (written by a tacho before v0.9.0, or one that couldn't resolve it)\n", key)
		default:
			fmt.Printf("  %-16s present but not used — observed from another Claude config dir (%s, not %s)\n", key, r.Root, here)
		}
	case cache.SnapshotExpired:
		fmt.Printf("  %-16s present (collected %s, expired)\n", key, doctorAge(r.CollectedAt, now))
	case cache.SnapshotStale:
		fmt.Printf("  %-16s present (collected %s, stale but usable)\n", key, doctorAge(r.CollectedAt, now))
	default:
		fmt.Printf("  %-16s present (collected %s, fresh)\n", key, doctorAge(r.CollectedAt, now))
	}
}

func reportIntegrations() {
	fmt.Println("integrations:")
	reportSwiftBarPlugin()
}

func reportSwiftBarPlugin() {
	if path := findSwiftBarPlugin(); path != "" {
		fmt.Println("  SwiftBar:   plugin found (" + path + ")")
		return
	}
	fmt.Println("  SwiftBar:   plugin not found in the SwiftBar / xbar plugin folders — run `tacho setup swiftbar --write` (ignore this if you don't use SwiftBar)")
}

// findSwiftBarPlugin returns the tacho plugin file in the first plugin folder
// that has one, or "" (#265). A plugin is any tacho.*.sh: the README invites
// renaming the interval (tacho.1m.sh). SWIFTBAR_PLUGIN_PATH is the running
// plugin file itself, set only when doctor runs from inside a plugin.
func findSwiftBarPlugin() string {
	if p := os.Getenv("SWIFTBAR_PLUGIN_PATH"); p != "" && isTachoPluginName(filepath.Base(p)) && isRegularFile(p) {
		return p
	}
	for _, dir := range swiftBarPluginDirs() {
		// A missing or unreadable folder just has no plugin to report.
		if p, _ := tachoPluginIn(dir); p != "" {
			return p
		}
	}
	return ""
}

// tachoPluginIn returns the first tacho plugin file (tacho.*.sh) in dir, ""
// when there is none, or the error listing dir.
func tachoPluginIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if p := filepath.Join(dir, e.Name()); isTachoPluginName(e.Name()) && isRegularFile(p) {
			return p, nil
		}
	}
	return "", nil
}

// swiftBarPluginDirs lists the plugin folders to search: SwiftBar's
// SWIFTBAR_PLUGINS_PATH (set for plugins), its user-chosen PluginDirectory
// setting, then the SwiftBar and xbar defaults. xbar passes plugins no
// folder variable of its own.
func swiftBarPluginDirs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	add(os.Getenv("SWIFTBAR_PLUGINS_PATH"))
	add(swiftBarPluginDirectory())
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, "Library", "Application Support", "SwiftBar", "Plugins"))
		add(filepath.Join(home, "Library", "Application Support", "xbar", "plugins"))
	}
	return out
}

// swiftBarPluginDirectory reads SwiftBar's PluginDirectory setting (the
// folder picked in its preferences); "" off macOS or when unset. It's a var so
// tests don't read the machine's real preferences.
var swiftBarPluginDirectory = func() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("defaults", "read", "com.ameba.SwiftBar", "PluginDirectory").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isTachoPluginName(name string) bool {
	ok, _ := filepath.Match("tacho.*.sh", name)
	return ok
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
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

// configValueWarnings lists the config.json values the read path ignores
// (unknown tools, styles, metrics, limit displays, out-of-range thresholds)
// with what tacho shows instead (#230). Nothing when the file is missing or
// can't be decoded — reportJSONFile covers those.
func configValueWarnings() []string {
	c, err := config.LoadStrict()
	if err != nil {
		return nil
	}
	return c.Warnings()
}

// reportJSONFile is reportFile plus a decode check, so a broken config.json /
// pricing.json is visible here instead of being silently ignored by the
// lenient render-path loaders. validate is the loader's own decode check
// (config.Validate / pricing.Validate), which also catches a wrongly typed
// value: the loaders ignore the whole file for that too (#260).
func reportJSONFile(label, path string, validate func([]byte) error) {
	fmt.Println("  " + label + ":  " + jsonFileState(path, validate))
}

// jsonFileState classifies a JSON config file for the doctor report. A nil
// validate checks JSON syntax only.
func jsonFileState(path string, validate func([]byte) error) string {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "(default)"
	}
	if err != nil {
		return "unreadable — " + err.Error()
	}
	// Syntax only: decoding into json.RawMessage checks the JSON grammar
	// without converting values, so a number that overflows float64 in a
	// field the loader skips (e.g. "extra": 1e1000) isn't misreported — the
	// loaders accept such a file, and validate is what decides the rest.
	var raw json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return "present but INVALID JSON (ignored) — " + err.Error()
	}
	if validate != nil {
		if err := validate(b); err != nil {
			return "present but INVALID (ignored) — " + err.Error()
		}
	}
	return "present"
}
