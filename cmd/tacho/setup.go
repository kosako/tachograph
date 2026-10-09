package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/setup"
)

const setupUsage = `usage:
  tacho setup claude            print the ~/.claude/settings.json statusLine snippet
  tacho setup claude --write    merge it into ~/.claude/settings.json (backs up first)
  tacho setup swiftbar          print a SwiftBar plugin that runs this tacho by absolute path
  tacho setup swiftbar --write  write it into SwiftBar's plugin folder (backs up first)
`

func runSetup(args []string) int {
	if len(args) > 0 && args[0] == "swiftbar" {
		return runSetupSwiftBar(args[1:])
	}
	if len(args) == 0 || args[0] != "claude" {
		fmt.Fprint(os.Stderr, setupUsage)
		return 2
	}
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	write := fs.Bool("write", false, "merge into ~/.claude/settings.json")
	fs.Parse(args[1:])

	exe := resolveExe()
	if exe == "" {
		// Without a resolved self there is no safe command to write: a bare
		// `tacho` could be a different install shadowing this one (#193).
		fmt.Fprintln(os.Stderr, "tacho: cannot determine the running binary's path; nothing safe to configure")
		return 1
	}
	command := setup.Command(pathTachoIsSelf(exe), exe)
	snippet := setup.Snippet(command)
	path := claudeSettingsPath()

	if !*write {
		fmt.Println("Add this to " + path + ":")
		fmt.Println()
		fmt.Println(snippet)
		fmt.Println()
		fmt.Println(setupNote(command, exe))
		fmt.Println("Re-run with --write to merge it in automatically.")
		return 0
	}

	if path == "" {
		fmt.Fprintln(os.Stderr, "tacho: cannot locate the home directory")
		return 1
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	merged, err := setup.MergeSettings(existing, command)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		fmt.Fprintln(os.Stderr, "tacho: leaving "+path+" untouched; paste the snippet manually with `tacho setup claude`")
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	// Back up only once: the merge is idempotent, so re-running --write would
	// otherwise overwrite the original backup with tacho's own merged output and
	// lose the user's pre-tacho statusLine. Keep the first .bak.
	if bak := path + ".bak"; len(existing) > 0 {
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			if err := writeFileAtomic(bak, existing, 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "tacho: could not write backup:", err)
				return 1
			}
			fmt.Println("Backed up existing settings to " + bak)
		}
	}
	if err := writeFileAtomic(path, merged, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	fmt.Println("Wrote statusLine to " + path + " (command: " + command + ")")
	fmt.Println("Restart Claude Code to pick it up.")
	return 0
}

// runSetupSwiftBar prints or installs the SwiftBar plugin with this binary's
// absolute path on its exec line: SwiftBar runs plugins with launchd's
// minimal PATH, which misses most install locations (#270).
func runSetupSwiftBar(args []string) int {
	fs := flag.NewFlagSet("setup swiftbar", flag.ExitOnError)
	write := fs.Bool("write", false, "write it into SwiftBar's plugin folder")
	fs.Parse(args)

	exe := resolveExe()
	if exe == "" {
		fmt.Fprintln(os.Stderr, "tacho: cannot determine the running binary's path; nothing safe to configure")
		return 1
	}
	plugin := setup.SwiftBarPlugin(exe)
	if !*write {
		// Only the script goes to stdout, so it can be redirected to a file.
		fmt.Print(plugin)
		fmt.Fprintln(os.Stderr, "tacho: save this as tacho.30s.sh in your SwiftBar plugin folder and make it executable, or re-run with --write")
		return 0
	}

	dir := swiftBarPluginDirectory()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "tacho: SwiftBar's plugin folder isn't set; choose one in SwiftBar first, or save the output of `tacho setup swiftbar` there yourself")
		return 1
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		fmt.Fprintln(os.Stderr, "tacho: SwiftBar's plugin folder "+dir+" is not a directory")
		return 1
	}
	// Replace an installed tacho plugin in place, keeping its name (the name
	// carries the refresh interval); otherwise add tacho.30s.sh. A folder that
	// can't be listed could hide an installed plugin, so nothing is written.
	path, err := tachoPluginIn(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacho: cannot read SwiftBar's plugin folder:", err)
		return 1
	}
	if path == "" {
		path = filepath.Join(dir, "tacho.30s.sh")
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	if len(existing) > 0 && string(existing) != plugin {
		// The replaced script may carry export lines the user added. Keep it
		// outside the plugin folder, where SwiftBar won't try to run it.
		cfgDir := config.Dir()
		if cfgDir == "" {
			fmt.Fprintln(os.Stderr, "tacho: cannot locate the config directory for the backup; leaving "+path+" untouched")
			return 1
		}
		bak := filepath.Join(cfgDir, "swiftbar-plugin.bak")
		if err := os.MkdirAll(filepath.Dir(bak), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "tacho: could not write backup:", err)
			return 1
		}
		if err := writeFileAtomic(bak, existing, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "tacho: could not write backup:", err)
			return 1
		}
		fmt.Println("Backed up the previous plugin to " + bak + " (copy back any export lines you added)")
	}
	if err := writeFileAtomic(path, []byte(plugin), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	fmt.Println("Wrote the SwiftBar plugin to " + path + " (runs " + exe + ")")
	fmt.Println("SwiftBar picks it up on its next refresh.")
	return 0
}

// resolveExe returns the absolute path to the running binary, following
// symlinks so the snippet points at the real file. It's a var so tests can
// simulate an unresolvable binary.
var resolveExe = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// tachoOnPath reports whether a bare `tacho` resolves on the PATH.
func tachoOnPath() bool {
	_, err := exec.LookPath("tacho")
	return err == nil
}

// pathTachoIsSelf reports whether the bare `tacho` on the PATH resolves to
// this very binary. Mere presence isn't enough: a different (often older)
// install on the PATH would make a bare snippet silently run that one instead
// of the binary the user just invoked (#193).
func pathTachoIsSelf(exe string) bool {
	if exe == "" {
		return false
	}
	p, err := exec.LookPath("tacho")
	if err != nil {
		return false
	}
	return sameExecutable(p, exe)
}

// setupNote explains the command setup chose. An npm install keeps the
// binary's absolute path (a bare `tacho` would go through the Node launcher
// on every status line refresh), and that path sits under the Node version's
// directory, so the note says when to re-run setup (#259).
func setupNote(command, exe string) string {
	if strings.HasPrefix(command, "tacho ") {
		return "(the tacho on your PATH is this binary, so the bare command works.)"
	}
	if p, err := exec.LookPath("tacho"); err == nil && sameInstall(p, exe) {
		return "(the tacho on your PATH is the npm launcher for this binary; the binary's absolute path is\n" +
			" baked in so the status line skips Node's startup. It lives under your Node version's directory:\n" +
			" re-run `tacho setup claude --write` after switching Node versions or reinstalling.)"
	}
	return "(this binary doesn't resolve as `tacho` on your PATH, so the absolute path is baked in.)"
}

// sameInstall reports whether the `tacho` found on the PATH (p) runs this
// binary (exe): the same file, or the npm launcher that spawns it (#259).
func sameInstall(p, exe string) bool {
	if sameExecutable(p, exe) {
		return true
	}
	for _, t := range npmLauncherTargets(p) {
		if sameExecutable(t, exe) {
			return true
		}
	}
	return false
}

// npmLauncherTargets returns where the platform binary an npm launcher runs
// is, or nil when p isn't one. npm links `tacho` to the package's
// bin/tacho.js — a symlink on unix, a tacho.cmd / tacho.ps1 shim on Windows —
// and the launcher spawns the binary postinstall placed next to tacho.js. A
// Windows shim in node_modules/.bin belongs to a local install (the package
// is its sibling, ../tachograph); anywhere else it is the global prefix (the
// package is under node_modules/tachograph). Only the one layout the shim's
// location implies is returned, so an unrelated tacho elsewhere never counts.
// Besides tacho.cmd / tacho.ps1, npm on Windows also writes an extensionless
// `tacho` sh shim for Git Bash; on Windows the binary itself is tacho.exe, so
// an extensionless tacho there can only be that shim.
func npmLauncherTargets(p string) []string {
	return npmLauncherTargetsFor(p, runtime.GOOS)
}

func npmLauncherTargetsFor(p, goos string) []string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	bin := "tacho"
	if goos == "windows" {
		bin = "tacho.exe"
	}
	dir := filepath.Dir(p)
	switch name := strings.ToLower(filepath.Base(p)); {
	case name == "tacho.js":
		return []string{filepath.Join(dir, bin)}
	case name == "tacho.cmd", name == "tacho.ps1", name == "tacho" && goos == "windows":
		if filepath.Base(dir) == ".bin" && filepath.Base(filepath.Dir(dir)) == "node_modules" {
			return []string{filepath.Join(dir, "..", "tachograph", "bin", bin)}
		}
		return []string{filepath.Join(dir, "node_modules", "tachograph", "bin", bin)}
	}
	return nil
}

// sameExecutable reports whether two paths refer to the same file after
// following symlinks (so a /usr/local/bin symlink to the real install still
// counts as the same binary).
func sameExecutable(a, b string) bool {
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// goBin reports where `go install` places binaries: GOBIN when set, else
// the bin directory of the first GOPATH entry (go install uses only the
// first). It asks the go toolchain (`go env` also honors the go env file);
// when go isn't callable it reads the same variables from the environment,
// then falls back to go's default GOPATH, ~/go (#264).
func goBin() string {
	if out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output(); err == nil {
		// One value per line, and an empty value is an empty line: drop only
		// the final newline. The values themselves are used verbatim.
		s := strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
		if lines := strings.Split(s, "\n"); len(lines) == 2 {
			if b := goBinFrom(lines[0], lines[1]); b != "" {
				return b
			}
		}
	}
	if b := goBinFrom(os.Getenv("GOBIN"), os.Getenv("GOPATH")); b != "" {
		return b
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "bin")
	}
	return ""
}

// goBinFrom resolves go install's target directory from GOBIN and GOPATH
// values; "" when both are empty.
func goBinFrom(gobin, gopath string) string {
	if gobin != "" {
		return gobin
	}
	if first := filepath.SplitList(gopath); len(first) > 0 && first[0] != "" {
		return filepath.Join(first[0], "bin")
	}
	return ""
}

// claudeSettingsPath returns ~/.claude/settings.json, honoring
// CLAUDE_CONFIG_DIR the way Claude Code itself does.
func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// writeFileAtomic writes b via a temp file + rename in the target directory,
// so an interrupted write can't leave a truncated settings.json behind
// (#194 L-02, the same contract as the cache writes). The file gets perm:
// 0600 for settings and backups (what CreateTemp gave them before), 0755 for
// the SwiftBar plugin, which must be executable.
func writeFileAtomic(path string, b []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// claudeStatusLineCommand extracts the configured statusLine command, or a
// sentinel string describing why there isn't one.
func claudeStatusLineCommand(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(missing)"
	}
	var s struct {
		StatusLine *struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return "(unreadable)"
	}
	if s.StatusLine == nil || s.StatusLine.Command == "" {
		return "(no statusLine)"
	}
	return s.StatusLine.Command
}

// statusLineResolves checks that the first token of the command exists as an
// executable (either an absolute/relative path or a PATH lookup).
func statusLineResolves(command string) bool {
	bin, _ := statusLineBinary(command)
	return bin != ""
}

// statusLineBinary resolves the executable a statusLine command runs, or ""
// when it doesn't resolve. checked is false when whether it resolves is
// unknown: tacho can't tell which program the command runs (see firstToken),
// or the shell may still run what tacho doesn't find. "Doesn't resolve" is
// left to what tacho can tell for sure (#362):
//
//   - outside Windows, an absolute path (~ expanded) that is missing or not
//     executable (#340), or a name off the PATH that isn't a bash builtin or a
//     function the shell inherits (see shellMayDefine);
//   - on Windows, a path from a drive letter (C:\… or C:/…, as setup writes
//     it) that is missing even with an executable extension added.
//
// A relative path, and a name the PATH finds through a relative entry, are
// unknown: Claude Code resolves them from its own working directory, not
// doctor's. So are, on Windows, a name off the PATH — PowerShell, the shell
// there without Git Bash, has cmdlets, functions, and aliases off it
// (Get-Date), and Git Bash adds its own directories to it — and a path not
// from a drive letter (/c/… is Git Bash's).
func statusLineBinary(command string) (bin string, checked bool) {
	return statusLineBinaryFor(command, runtime.GOOS)
}

func statusLineBinaryFor(command, goos string) (bin string, checked bool) {
	bin, ok := firstTokenFor(command, goos)
	if !ok {
		return "", false
	}
	if bin == "" {
		return "", true
	}
	if goos == "windows" {
		switch {
		case isDrivePath(bin):
			return windowsExecutable(bin), true
		case strings.ContainsAny(bin, `/\:`):
			return "", false
		}
		if p, err := exec.LookPath(bin); err == nil {
			return p, true
		}
		return "", false
	}
	if strings.Contains(bin, "/") {
		if !strings.HasPrefix(bin, "/") {
			return "", false
		}
		// LookPath runs a path as is and checks the file is executable, so a
		// script missing its execute bit doesn't pass as resolved (#340).
		if p, err := exec.LookPath(bin); err == nil {
			return p, true
		}
		return "", true
	}
	p, err := exec.LookPath(bin)
	switch {
	case err == nil:
		return p, true
	case errors.Is(err, exec.ErrDot), shellBuiltins[bin], shellMayDefine(bin):
		return "", false
	}
	return "", true
}

// isDrivePath reports whether s is a Windows path from a drive letter.
func isDrivePath(s string) bool {
	return len(s) >= 3 && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z') && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// windowsExecutable returns the file a Windows path runs, or "" when there's
// none: the path itself when it's a regular file (Windows has no execute bit,
// and a POSIX shell's script has no extension), else the path with an
// extension the shell adds — .exe for Git Bash, PATHEXT's for PowerShell.
func windowsExecutable(path string) string {
	for _, ext := range append([]string{"", ".exe"}, strings.Split(os.Getenv("PATHEXT"), ";")...) {
		if info, err := os.Stat(path + ext); err == nil && !info.IsDir() {
			return path + ext
		}
	}
	return ""
}

// shellMayDefine reports whether bash may run name as a function its
// environment gives it: one exported with export -f, or anything the BASH_ENV
// file defines.
func shellMayDefine(name string) bool {
	if os.Getenv("BASH_ENV") != "" {
		return true
	}
	for _, k := range []string{"BASH_FUNC_" + name + "%%", "BASH_FUNC_" + name + "()"} {
		if _, ok := os.LookupEnv(k); ok {
			return true
		}
	}
	return false
}

// statusLineWarning is doctor's diagnosis of the configured statusLine
// command (both "" when fine). Besides a command that no longer resolves, it
// warns about one that runs a different tacho than this binary — e.g. an npm
// install's absolute path left pointing into an old Node version's directory,
// which keeps running that old tacho (#259). Commands that aren't tacho itself
// (a user's own script) are not second-guessed. A command whose program tacho
// can't tell from the text gets a note instead of a warning (#362): a warning
// would be a guess, and its advice to re-run setup would replace the user's
// own statusLine.
func statusLineWarning(command, exe string) (warning, note string) {
	bin, checked := statusLineBinary(command)
	if !checked {
		return "", "not checked — tacho can't tell which program this shell command runs"
	}
	if bin == "" {
		return "that command does not resolve — re-run `tacho setup claude --write`", ""
	}
	if exe == "" || !isTachoExecutable(bin) || sameInstall(bin, exe) {
		return "", ""
	}
	return "it runs a different tacho (" + bin + ") than this one — re-run `tacho setup claude --write` to point it here", ""
}

// isTachoExecutable reports whether path names tacho itself or its npm
// launcher, as opposed to some other program a statusLine may run.
func isTachoExecutable(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "tacho", "tacho.exe", "tacho.js", "tacho.cmd", "tacho.ps1":
		return true
	}
	return false
}

// firstToken returns the first word of a statusLine command as a POSIX shell
// reads it — Claude Code runs the command in one (Git Bash on Windows, when
// installed): line continuations joined, quotes and backslash escapes
// removed, which also undoes the double quoting setup.Command applies
// (#194 L-01), and a leading ~ expanded to the home directory (#362). ok is
// false when the program the command runs can't be told from the text: the
// word holds an expansion, a glob or brace pattern, ~user, a variable
// assignment, a redirection, a subshell, or a comment, or another command
// follows a control operator (missing || ~/.claude/statusline.sh runs the
// script when the first command fails). An unterminated quote is a syntax
// error the shell won't run, so it yields "", which doesn't resolve.
//
// On Windows the shell is Git Bash or, without it, PowerShell, and a word
// they read differently is unknown too: an unquoted backslash, a path
// separator to PowerShell (.\tools\statusline.exe) but an escape to Git Bash,
// which is also why line continuations are left alone there; and a ~ when
// HOME and USERPROFILE disagree (see shellHomeFor).
func firstToken(command string) (string, bool) {
	return firstTokenFor(command, runtime.GOOS)
}

func firstTokenFor(command, goos string) (string, bool) {
	if goos != "windows" {
		command = joinLineContinuations(command)
	}
	command = strings.TrimSpace(command)
	var b strings.Builder
	i, n := 0, len(command)
	if n > 0 && command[0] == '~' {
		if n > 1 && command[1] != '/' && !endsShellWord(command[1]) {
			return "", false // ~user, ~+, ~-
		}
		home := shellHomeFor(goos, os.Getenv("HOME"), os.Getenv("USERPROFILE"))
		if home == "" {
			return "", false
		}
		b.WriteString(home)
		i++
	}
word:
	for i < n {
		c := command[i]
		switch {
		case endsShellWord(c):
			break word
		case c == '#' && i == 0:
			return "", false // a comment, not a command
		case strings.IndexByte("$`*?[{<>()", c) >= 0:
			return "", false
		case c == '=' && isShellName(strings.TrimSuffix(command[:i], "+")):
			return "", false // an assignment before the command (bash also has NAME+=)
		case c == '\\':
			if goos == "windows" {
				return "", false
			}
			if i+1 < n {
				i++
				c = command[i]
			}
			b.WriteByte(c)
		case c == '\'':
			j := strings.IndexByte(command[i+1:], '\'')
			if j < 0 {
				return "", true
			}
			b.WriteString(command[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			// Inside double quotes a backslash escapes only " $ ` and \, and
			// $ and ` still expand.
			for i++; ; i++ {
				if i >= n {
					return "", true
				}
				d := command[i]
				if d == '"' {
					break
				}
				if d == '$' || d == '`' {
					return "", false
				}
				if d == '\\' && i+1 < n && strings.IndexByte("\"$`\\", command[i+1]) >= 0 {
					i++
					d = command[i]
				}
				b.WriteByte(d)
			}
		default:
			b.WriteByte(c)
		}
		i++
	}
	if commandFollows(command[i:]) {
		return "", false
	}
	return b.String(), true
}

// joinLineContinuations removes each backslash-newline outside single quotes
// and comments, as a shell does before it splits the command into words. A
// comment runs to the newline, so a backslash ending it doesn't pull the next
// line in.
func joinLineContinuations(s string) string {
	if !strings.Contains(s, "\\\n") {
		return s
	}
	var b strings.Builder
	single, double, wordStart := false, false, true
	for i := 0; i < len(s); i++ {
		c := s[i]
		start := wordStart
		wordStart = false
		switch {
		case single:
			single = c != '\''
		case c == '\\' && i+1 < len(s):
			if s[i+1] == '\n' {
				i++
				wordStart = start
				continue
			}
			b.WriteByte(c)
			i++
			c = s[i]
		case double:
			double = c != '"'
		case c == '\'':
			single = true
		case c == '"':
			double = true
		case c == '#' && start:
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				j = len(s) - i
			}
			b.WriteString(s[i : i+j])
			i += j - 1
			continue
		case strings.IndexByte(" \t\n;&|()<>", c) >= 0:
			wordStart = true
		}
		b.WriteByte(c)
	}
	return b.String()
}

// commandFollows reports whether rest, what follows a command's first word,
// holds another command after a control operator (; & | or a newline) outside
// quotes. A trailing operator (tacho statusline;), a redirection's & or |
// (2>&1, &>file, >|file), and a comment don't count.
func commandFollows(rest string) bool {
	afterOp, wordStart := false, true
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c == ' ' || c == '\t':
			wordStart = true
			continue
		case c == '\n', c == ';',
			c == '&' && !(i > 0 && strings.IndexByte("<>", rest[i-1]) >= 0) && !(i+1 < len(rest) && rest[i+1] == '>'),
			c == '|' && !(i > 0 && rest[i-1] == '>'):
			afterOp, wordStart = true, true
			continue
		case c == '#' && wordStart:
			if j := strings.IndexByte(rest[i:], '\n'); j >= 0 {
				i += j - 1 // the newline ending the comment is an operator
				continue
			}
			return false
		}
		if afterOp {
			return true
		}
		wordStart = false
		switch c {
		case '\\':
			i++
		case '\'':
			j := strings.IndexByte(rest[i+1:], '\'')
			if j < 0 {
				return false
			}
			i += j + 1
		case '"':
			for i++; i < len(rest) && rest[i] != '"'; i++ {
				if rest[i] == '\\' {
					i++
				}
			}
		}
	}
	return false
}

// endsShellWord reports whether c ends an unquoted shell word: a blank or a
// command separator.
func endsShellWord(c byte) bool {
	return strings.IndexByte(" \t\n;&|", c) >= 0
}

// shellHomeFor is the directory a leading ~ expands to, or "" when it can't
// be told: HOME, as in a POSIX shell. On Windows Git Bash reads HOME, set from
// USERPROFILE when it's unset, while PowerShell reads USERPROFILE, so there
// it's USERPROFILE unless a HOME that differs leaves it to the shell.
func shellHomeFor(goos, home, profile string) string {
	if goos != "windows" {
		return home
	}
	if home != "" && !strings.EqualFold(home, profile) {
		return ""
	}
	return profile
}

// isShellName reports whether s is a shell variable name.
func isShellName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// shellBuiltins are the words bash (Git Bash, and /bin/sh on macOS) handles
// itself rather than running a program found on the PATH: its reserved words
// (compgen -k) and builtins (compgen -b) as of bash 5.3. The other POSIX
// shells' builtins are among them.
var shellBuiltins = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		! [[ ]] { } case coproc do done elif else esac fi for function if in
		select then time until while
		. : [ alias bg bind break builtin caller cd command compgen complete
		compopt continue declare dirs disown echo enable eval exec exit export
		false fc fg getopts hash help history jobs kill let local logout
		mapfile popd printf pushd pwd read readarray readonly return set shift
		shopt source suspend test times trap true type typeset ulimit umask
		unalias unset wait`) {
		m[w] = true
	}
	return m
}()
