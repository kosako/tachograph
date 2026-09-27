package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kosako/tachograph/internal/setup"
)

const setupUsage = `usage:
  tacho setup claude          print the ~/.claude/settings.json statusLine snippet
  tacho setup claude --write  merge it into ~/.claude/settings.json (backs up first)
`

func runSetup(args []string) int {
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
			if err := writeFileAtomic(bak, existing); err != nil {
				fmt.Fprintln(os.Stderr, "tacho: could not write backup:", err)
				return 1
			}
			fmt.Println("Backed up existing settings to " + bak)
		}
	}
	if err := writeFileAtomic(path, merged); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	fmt.Println("Wrote statusLine to " + path + " (command: " + command + ")")
	fmt.Println("Restart Claude Code to pick it up.")
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

// goBin reports where `go install` places binaries: $(go env GOPATH)/bin,
// falling back to ~/go/bin when the go toolchain isn't callable.
func goBin() string {
	if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return filepath.Join(p, "bin")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "bin")
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
// (#194 L-02, the same contract as the cache writes). CreateTemp's 0600 mode
// carries over to the renamed file, matching the previous explicit chmod.
func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
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
	return statusLineBinary(command) != ""
}

// statusLineBinary resolves the executable a statusLine command runs, or ""
// when it doesn't resolve.
func statusLineBinary(command string) string {
	bin := firstToken(command)
	if bin == "" {
		return ""
	}
	if strings.ContainsAny(bin, "/") {
		if info, err := os.Stat(bin); err != nil || info.IsDir() {
			return ""
		}
		return bin
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	return p
}

// statusLineWarning is doctor's diagnosis of the configured statusLine
// command ("" when fine). Besides a command that no longer resolves, it flags
// one that runs a different tacho than this binary — e.g. an npm install's
// absolute path left pointing into an old Node version's directory, which
// keeps running that old tacho (#259). Commands that aren't tacho itself (a
// user's own script) are not second-guessed.
func statusLineWarning(command, exe string) string {
	bin := statusLineBinary(command)
	if bin == "" {
		return "that command does not resolve — re-run `tacho setup claude --write`"
	}
	if exe == "" || !isTachoExecutable(bin) || sameInstall(bin, exe) {
		return ""
	}
	return "it runs a different tacho (" + bin + ") than this one — re-run `tacho setup claude --write` to point it here"
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

// firstToken returns the first whitespace- or quote-delimited token of a shell
// command, enough to identify the binary.
func firstToken(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if command[0] == '"' {
		// Undo the double-quote escaping setup.Command applies (\" \$ \` \\),
		// so a quoted path round-trips into the real binary path (#194 L-01).
		var b strings.Builder
		for i := 1; i < len(command); i++ {
			c := command[i]
			if c == '\\' && i+1 < len(command) {
				if n := command[i+1]; n == '"' || n == '$' || n == '`' || n == '\\' {
					b.WriteByte(n)
					i++
					continue
				}
			}
			if c == '"' {
				return b.String()
			}
			b.WriteByte(c)
		}
		return b.String() // unterminated quote: best effort
	}
	if i := strings.IndexAny(command, " \t"); i >= 0 {
		return command[:i]
	}
	return command
}
