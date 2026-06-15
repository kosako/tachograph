package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kosako/tachograph/internal/config"
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
	command := setup.Command(tachoOnPath(), exe)
	snippet := setup.Snippet(command)
	path := claudeSettingsPath()

	if !*write {
		fmt.Println("Add this to " + path + ":")
		fmt.Println()
		fmt.Println(snippet)
		fmt.Println()
		if strings.HasPrefix(command, "tacho ") {
			fmt.Println("(tacho is on your PATH, so the bare command works.)")
		} else {
			fmt.Println("(tacho is not on your PATH, so the absolute path is baked in.)")
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
	if len(existing) > 0 {
		if err := os.WriteFile(path+".bak", existing, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "tacho: could not write backup:", err)
			return 1
		}
		fmt.Println("Backed up existing settings to " + path + ".bak")
	}
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	fmt.Println("Wrote statusLine to " + path + " (command: " + command + ")")
	fmt.Println("Restart Claude Code to pick it up.")
	return 0
}

func runDoctor(args []string) int {
	exe := resolveExe()
	onPath := tachoOnPath()

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

// resolveExe returns the absolute path to the running binary, following
// symlinks so the snippet points at the real file.
func resolveExe() string {
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

func reportFile(label, path string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Println("  " + label + ":  present")
	} else {
		fmt.Println("  " + label + ":  (default)")
	}
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
