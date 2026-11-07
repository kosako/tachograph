package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/agentpath"
	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/collector/codex"
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
	if unexpectedArgs("tacho doctor", args) {
		return 2
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

	reportIntegrations(exe)
	fmt.Println()

	reportCurrentStatus(now)
	fmt.Println()

	reportClaudeStatusLine(claudeSettingsPath(), exe)
	return 0
}

// reportClaudeStatusLine reports the statusLine in Claude Code's settings at
// path ("" when the home directory can't be located), and whether its
// command resolves to this tacho (exe).
func reportClaudeStatusLine(path, exe string) {
	if path == "" {
		fmt.Println("Claude Code statusLine:")
		fmt.Println("  unavailable — cannot locate the home directory (set HOME or CLAUDE_CONFIG_DIR)")
		return
	}
	fmt.Println("Claude Code statusLine (" + path + "):")
	switch cmd, err := claudeStatusLineCommand(path); {
	case err != nil:
		fmt.Printf("  settings.json can't be read: %v — fix its permissions or file type, then `tacho setup claude --write`\n", err)
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
}

func reportDataSources(now time.Time) {
	fmt.Println("data sources:")
	if root, ok := agentpath.ClaudeRoot(""); ok {
		reportJSONLTree("Claude projects", claude.ProjectsDir(root), now,
			"run Claude Code once, or set CLAUDE_CONFIG_DIR")
	} else {
		fmt.Println("  Claude projects: unavailable — cannot locate the home directory")
	}
	if root, ok := agentpath.CodexRoot(""); ok {
		reportJSONLTree("Codex sessions", codex.SessionsDir(root), now,
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
	info, err := os.Stat(root)
	if err != nil {
		return time.Time{}, 0, err
	}
	if !info.IsDir() {
		// A file (or a link to one) where the tree should be: the collectors
		// can't list it, so it isn't a data source with one log (#440).
		return time.Time{}, 0, fmt.Errorf("%s is not a directory", root)
	}
	// WalkDir doesn't follow a symlink, not even the root: a projects or
	// sessions directory linked into place would count no files, while the
	// collectors (os.ReadDir) read through the link. Walk from where it
	// leads (#440).
	walkRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return time.Time{}, 0, err
	}
	var newest time.Time
	count := 0
	err = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
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
	reportStatusCache(now)
	reportSnapshot(now)
}

// reportStatusCache reports status.json as the reader judges it, by the same
// checks (cache.InspectStatus): a file just modified but not valid, written
// for another schema version, or assembled for another profile's config
// dirs is there but never used, which its modification time can't tell
// (#446). A file modified in the future (the clock was set back since) isn't
// used however recent it looks (#415).
func reportStatusCache(now time.Time) {
	const key = "status.json:"
	const rewrite = "the next `tacho` run rewrites it"
	roots := core.Roots(core.Options{}) // the roots core.Status reads (#321)
	r := cache.InspectStatus(cache.StatusTTL, now, roots)
	switch r.State {
	case cache.StatusFileMissing:
		fmt.Printf("  %-16s missing — run `tacho` or `tacho status --json` once\n", key)
	case cache.StatusFileUnreadable:
		fmt.Printf("  %-16s unreadable: %v\n", key, r.Err)
	case cache.StatusFileExpired:
		fmt.Printf("  %-16s present (modified %s, expired)\n", key, doctorAge(r.ModTime, now))
	case cache.StatusFileFuture:
		fmt.Printf("  %-16s present (modified %s, not used)\n", key, doctorAge(r.ModTime, now))
	case cache.StatusFileInvalid:
		fmt.Printf("  %-16s present (modified %s) but not used — not valid JSON; %s\n", key, doctorAge(r.ModTime, now), rewrite)
	case cache.StatusFileOldSchema:
		written := fmt.Sprintf("written for schema %q", r.SchemaVersion)
		if r.SchemaVersion == "" {
			written = "it records no schema version"
		}
		fmt.Printf("  %-16s present (modified %s) but not used — %s, this tacho reads %s; %s\n",
			key, doctorAge(r.ModTime, now), written, schema.Version, rewrite)
	case cache.StatusFileOtherRoot:
		// A roots mismatch covers three cases, told apart as for the
		// snapshot: a set with no roots matches nothing, so a run that
		// resolves none never uses the file it writes either.
		why := "assembled for other Claude Code / Codex config dirs (another profile?); " + rewrite
		switch {
		case len(roots) == 0:
			why = "this run's Claude Code and Codex config dirs can't be resolved, so no status cache is used"
		case len(r.Roots) == 0:
			why = "it records no config dirs (written by a tacho before v0.9.0, or by a run that couldn't resolve them); " + rewrite
		}
		fmt.Printf("  %-16s present (modified %s) but not used — %s\n", key, doctorAge(r.ModTime, now), why)
	default:
		fmt.Printf("  %-16s present (modified %s, fresh)\n", key, doctorAge(r.ModTime, now))
	}
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
	case cache.SnapshotFuture:
		fmt.Printf("  %-16s present but not used — collected in the future (%s; the clock was set back?); %s\n",
			key, r.CollectedAt.Local().Format(time.RFC3339), rewrite)
	case cache.SnapshotExpired:
		fmt.Printf("  %-16s present (collected %s, expired)\n", key, doctorAge(r.CollectedAt, now))
	case cache.SnapshotStale:
		fmt.Printf("  %-16s present (collected %s, stale but usable)\n", key, doctorAge(r.CollectedAt, now))
	default:
		fmt.Printf("  %-16s present (collected %s, fresh)\n", key, doctorAge(r.CollectedAt, now))
	}
}

func reportIntegrations(exe string) {
	fmt.Println("integrations:")
	reportSwiftBarPlugin(exe)
}

// reportSwiftBarPlugin reports the tacho plugin doctor finds, and whether
// the program its exec line runs is there and is this tacho (exe), as the
// statusLine's check does (#482): a plugin left pointing at a tacho since
// moved or removed (an npm install's path after switching Node) fails on
// every run while its file is still found.
func reportSwiftBarPlugin(exe string) {
	plugin := findSwiftBarPlugin()
	if plugin == "" {
		fmt.Println("  SwiftBar:   plugin not found in the SwiftBar / xbar plugin folders — run `tacho setup swiftbar --write` (ignore this if you don't use SwiftBar)")
		return
	}
	fmt.Println("  SwiftBar:   plugin found (" + plugin + ")")
	var warning, note string
	if script, err := readPluginScript(plugin); err != nil {
		note = "not checked — can't read it: " + err.Error()
	} else {
		warning, note = swiftBarPluginWarning(script, exe)
	}
	if warning != "" {
		fmt.Println("    warning:  " + warning)
	}
	if note != "" {
		fmt.Println("    note:     " + note)
	}
}

// maxPluginScript bounds how much of a plugin doctor reads. The one tacho
// writes is under 1 KiB; a larger file is left unchecked (see
// pluginExecCommand).
const maxPluginScript = 64 << 10

// readPluginScript reads the plugin at path, up to one byte past
// maxPluginScript so that a larger file shows as one.
func readPluginScript(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPluginScript+1))
	return string(b), err
}

// swiftBarPluginWarning is doctor's diagnosis of a tacho plugin's script
// (both "" when fine), with statusLineWarning's verdicts: a warning when the
// program its exec line runs is missing or not executable — a shell exits
// when exec fails, so the plugin fails on every run — or is a different
// tacho than this one (exe). The program is told only for an absolute path
// in a script shaped like the one `tacho setup swiftbar` writes (see
// pluginExecCommand); any other gets a note instead, as a statusLine whose
// program can't be told does (#362): a warning would be a guess, and its
// advice to re-run setup would replace the user's own plugin.
func swiftBarPluginWarning(script, exe string) (warning, note string) {
	const unknown = "not checked — tacho can't tell which program this plugin runs"
	command, ok := pluginExecCommand(script)
	if !ok {
		return "", unknown
	}
	// The plugin is a bash or sh script, whichever OS doctor runs on.
	bin, ok := firstTokenFor(command, "darwin")
	switch {
	case !ok || bin == "" || strings.HasPrefix(bin, "-"):
		// "" is an unterminated quote; -a, -c, -l are exec's options.
		return "", unknown
	case !strings.Contains(bin, "/"):
		return "", "not checked — the plugin looks `" + bin + "` up on the PATH SwiftBar gives it, not your shell's; `tacho setup swiftbar --write` writes an absolute path instead"
	case !strings.HasPrefix(bin, "/"):
		return "", unknown // relative to SwiftBar's working directory, not doctor's
	}
	// LookPath runs a path as is and checks the file is executable.
	if _, err := exec.LookPath(bin); err != nil {
		return "the plugin runs " + bin + ", which is missing or not executable — re-run `tacho setup swiftbar --write`", ""
	}
	if runsAnotherTacho(bin, exe) {
		return "it runs a different tacho (" + bin + ") than this one — re-run `tacho setup swiftbar --write` to point it here", ""
	}
	return "", ""
}

// pluginExecCommand returns what follows exec in a plugin script shaped like
// the one `tacho setup swiftbar` writes, which the shell runs straight
// through to its exec line: a bash or sh shebang, then only comments, blank
// lines, and export lines that each set one variable (the PATH line, and the
// display settings the README has users add), then exec as the last
// command. ok is false for any other script: another command, a branch, a
// line continuation, or an export whose value runs or expands anything but a
// plain $NAME could exit or reach another exec first. (A BASH_ENV in
// SwiftBar's environment, which doctor can't see, could still redefine exec;
// tacho doesn't second-guess that.)
func pluginExecCommand(script string) (command string, ok bool) {
	if len(script) > maxPluginScript || strings.ContainsRune(script, '\r') {
		return "", false
	}
	lines := strings.Split(script, "\n")
	if !isShShebang(lines[0]) {
		return "", false
	}
	found := false
	for _, line := range lines[1:] {
		line = strings.TrimLeft(line, " \t")
		switch {
		case line == "" || line[0] == '#':
			continue // a comment ends at the newline, even after a backslash
		case found:
			return "", false // a command after exec
		case strings.HasSuffix(line, `\`):
			return "", false // the line may continue on the next one
		}
		if rest, isExec := cutWord(line, "exec"); isExec {
			command, found = rest, true
			continue
		}
		if rest, isExport := cutWord(line, "export"); !isExport || !isPlainAssignment(rest) {
			return "", false
		}
	}
	return command, found
}

// isShShebang reports whether line is a shebang that runs bash or sh, as
// tacho's plugin does, with no option (-n, -u, and others change what runs).
func isShShebang(line string) bool {
	interp, ok := strings.CutPrefix(line, "#!")
	if !ok {
		return false
	}
	isSh := func(name string) bool { return name == "bash" || name == "sh" }
	switch f := strings.Fields(interp); {
	case len(f) == 1 && strings.HasPrefix(f[0], "/"):
		return isSh(f[0][strings.LastIndexByte(f[0], '/')+1:])
	case len(f) == 2 && f[0] == "/usr/bin/env":
		return isSh(f[1])
	}
	return false
}

// cutWord returns the rest of line after its first word, when that word is
// word followed by a blank.
func cutWord(line, word string) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(line, word)
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	return strings.TrimLeft(rest, " \t"), true
}

// isPlainAssignment reports whether s, an export's arguments, sets one
// variable (NAME=value, then at most a comment) to a value that runs and
// expands nothing but $NAME: unquoted inert characters, single quotes, and
// double quotes with no backslash or backquote.
func isPlainAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq < 0 || !isShellName(s[:eq]) {
		return false
	}
	for i := eq + 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' || c == '\t':
			rest := strings.TrimLeft(s[i:], " \t")
			return rest == "" || rest[0] == '#'
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return false
			}
			i += j + 1
		case c == '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 || !isPlainQuoted(s[i+1:i+1+j]) {
				return false
			}
			i += j + 1
		case c == '$':
			n := shellNameLen(s[i+1:])
			if n == 0 {
				return false // ${…}, $(…), $1, $?, …
			}
			i += n
		case !isInertShellByte(c):
			return false
		}
	}
	return true
}

// isPlainQuoted reports whether s, the inside of double quotes, expands
// nothing but $NAME. A backslash is refused too: \" would end the quotes
// elsewhere than where isPlainAssignment looked.
func isPlainQuoted(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '`':
			return false
		case '$':
			n := shellNameLen(s[i+1:])
			if n == 0 {
				return false
			}
			i += n
		}
	}
	return true
}

// shellNameLen returns the length of the shell variable name s starts with,
// or 0 when it starts with none.
func shellNameLen(s string) int {
	n := 0
	for ; n < len(s); n++ {
		c := s[n]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (n == 0 || c < '0' || c > '9') {
			break
		}
	}
	return n
}

// isInertShellByte reports whether c, unquoted in a variable's value, is a
// character the shell takes literally (or, for ~, expands to a home).
func isInertShellByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.IndexByte("_./:,@%+=-~", c) >= 0
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
