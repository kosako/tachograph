package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		if strings.HasPrefix(command, "tacho ") {
			fmt.Println("(the tacho on your PATH is this binary, so the bare command works.)")
		} else {
			fmt.Println("(this binary doesn't resolve as `tacho` on your PATH, so the absolute path is baked in.)")
		}
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
			if err := os.WriteFile(bak, existing, 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "tacho: could not write backup:", err)
				return 1
			}
			fmt.Println("Backed up existing settings to " + bak)
		}
	}
	if err := os.WriteFile(path, merged, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	if err := os.Chmod(path, 0o600); err != nil {
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
	bin := firstToken(command)
	if bin == "" {
		return false
	}
	if strings.ContainsAny(bin, "/") {
		info, err := os.Stat(bin)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// firstToken returns the first whitespace- or quote-delimited token of a shell
// command, enough to identify the binary.
func firstToken(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if command[0] == '"' {
		if i := strings.IndexByte(command[1:], '"'); i >= 0 {
			return command[1 : 1+i]
		}
		return strings.Trim(command, `"`)
	}
	if i := strings.IndexAny(command, " \t"); i >= 0 {
		return command[:i]
	}
	return command
}
