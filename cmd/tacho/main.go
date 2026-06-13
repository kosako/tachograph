package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/cmuxbar"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/menubar"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
	"github.com/kosako/tachograph/internal/swiftbar"
)

const version = "0.0.1-dev"

const usage = `usage:
  tacho                 one-shot compact status
  tacho watch [-n sec]  refresh continuously
  tacho status --json   unified schema JSON (see docs/schema.md)
  tacho statusline      Claude Code statusLine adapter (reads stdin JSON)
  tacho cmux push       push status pills to the cmux sidebar once
  tacho cmux clear      remove tacho's pills from the cmux sidebar
  tacho swiftbar        SwiftBar/xbar plugin output (see contrib/tacho.30s.sh)
  tacho config show     print the current configuration
  tacho config set K V  set a config value (e.g. menubar.metric cost)
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version":
		fmt.Println("tacho " + version)
	case "status":
		os.Exit(runStatus(args))
	case "watch":
		os.Exit(runWatch(args))
	case "statusline":
		os.Exit(runStatusline(args))
	case "cmux":
		os.Exit(runCmux(args))
	case "swiftbar":
		os.Exit(runSwiftbar(args))
	case "config":
		os.Exit(runConfig(args))
	case "":
		if len(args) > 0 && args[0] == "--version" {
			fmt.Println("tacho " + version)
			return
		}
		os.Exit(runOnce(args))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func style(noColor bool) render.Style {
	return render.Style{Color: !noColor && os.Getenv("NO_COLOR") == ""}
}

func runSwiftbar(args []string) int {
	fs := flag.NewFlagSet("swiftbar", flag.ExitOnError)
	pngOut := fs.String("png", "", "write the menu bar gauge image to this PNG file (preview)")
	fs.Parse(args)

	now := time.Now()
	dark := appearanceDark()
	cfg := config.Load()
	s := core.Status(core.Options{Now: now})
	if *pngOut != "" {
		b64, ok := menubar.PNGBase64(s, dark, cfg.Menubar.Metric)
		if !ok {
			fmt.Fprintln(os.Stderr, "tacho: nothing to render")
			return 1
		}
		data, _ := base64.StdEncoding.DecodeString(b64)
		if err := os.WriteFile(*pngOut, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "tacho:", err)
			return 1
		}
		return 0
	}
	fmt.Print(swiftbar.Render(s, now, dark, cfg))
	return 0
}

const configUsage = `usage:
  tacho config show              print the current configuration
  tacho config path              print the config file path
  tacho config set <key> <val>   set a value and save

keys:
  tools           comma-separated: claude-code,codex  (which tools to show)
  menubar.style   meter | number
  menubar.metric  ` + "limit_5h | limit_weekly | context | cost | tokens" + `
`

func runConfig(args []string) int {
	sub := "show"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "show":
		b, _ := json.MarshalIndent(config.Load(), "", "  ")
		fmt.Println(string(b))
		return 0
	case "path":
		fmt.Println(config.Path())
		return 0
	case "set":
		if len(args) != 2 {
			fmt.Fprint(os.Stderr, configUsage)
			return 2
		}
		return configSet(args[0], args[1])
	default:
		fmt.Fprint(os.Stderr, configUsage)
		return 2
	}
}

func configSet(key, val string) int {
	c := config.Load()
	switch key {
	case "tools":
		var tools []string
		for _, t := range strings.Split(val, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if t != schema.ToolClaudeCode && t != schema.ToolCodex {
				fmt.Fprintf(os.Stderr, "tacho: unknown tool %q (want claude-code or codex)\n", t)
				return 2
			}
			tools = append(tools, t)
		}
		c.Tools = tools
	case "menubar.style":
		if val != config.StyleMeter && val != config.StyleNumber {
			fmt.Fprintf(os.Stderr, "tacho: invalid style %q (want meter or number)\n", val)
			return 2
		}
		c.Menubar.Style = val
	case "menubar.metric":
		if !render.ValidMetric(val) {
			fmt.Fprintf(os.Stderr, "tacho: invalid metric %q\n", val)
			return 2
		}
		c.Menubar.Metric = val
	default:
		fmt.Fprintf(os.Stderr, "tacho: unknown key %q\n", key)
		fmt.Fprint(os.Stderr, configUsage)
		return 2
	}
	if err := config.Save(c); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	return 0
}

// appearanceDark decides the logo/track ink for the colored menu bar gauge.
// The menu bar's light/dark look follows the wallpaper, not the Light/Dark
// mode setting, and there's no reliable CLI signal for it — so we default to
// a dark menu bar (white ink), which is correct for Dark mode and for the
// common case of a wallpaper-tinted bar. Users with a light menu bar set
// TACHO_APPEARANCE=light.
func appearanceDark() bool {
	return os.Getenv("TACHO_APPEARANCE") != "light"
}

func runOnce(args []string) int {
	fs := flag.NewFlagSet("tacho", flag.ExitOnError)
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	noCache := fs.Bool("no-cache", false, "bypass the TTL cache")
	fs.Parse(args)

	now := time.Now()
	s := core.Status(core.Options{Now: now, NoCache: *noCache})
	fmt.Println(render.StatusLines(s, now, style(*noColor)))
	return 0
}

func runWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	interval := fs.Int("n", 5, "refresh interval in seconds")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	fs.Parse(args)
	if *interval < 1 {
		*interval = 1
	}

	st := style(*noColor)
	for {
		now := time.Now()
		s := core.Status(core.Options{Now: now})
		// Clear screen and home the cursor between refreshes.
		fmt.Print("\x1b[H\x1b[2J")
		fmt.Printf("tachograph  %s  (every %ds, ctrl-c to quit)\n\n", now.Format("15:04:05"), *interval)
		fmt.Println(render.StatusLines(s, now, st))
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

// runStatusline is the R1 renderer: it consumes the session JSON Claude
// Code pipes in, snapshots it so other renderers can reuse the rate limits
// (the piggyback design from issue #4), and prints one templated line.
func runStatusline(args []string) int {
	fs := flag.NewFlagSet("statusline", flag.ExitOnError)
	tmplFlag := fs.String("template", "", "override the statusline template")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	fs.Parse(args)

	stdin, _ := io.ReadAll(os.Stdin)
	now := time.Now()

	claudeTool := claude.Collect(claude.Options{Now: now, StatuslineInput: stdin})
	if claudeTool.Available && claudeTool.Error == nil {
		_ = cache.WriteSnapshot(claudeTool)
	}
	s := core.Status(core.Options{Now: now}) // codex side rides the TTL cache
	for i := range s.Tools {
		if s.Tools[i].Tool == schema.ToolClaudeCode {
			s.Tools[i] = claudeTool
		}
	}

	tmpl := *tmplFlag
	if tmpl == "" {
		tmpl = loadTemplate()
	}
	fmt.Println(render.Template(tmpl, s, now, style(*noColor)))

	// R3 piggyback: inside a cmux terminal, mirror the status to the
	// sidebar. Fire-and-forget so the statusline stays fast.
	if cmuxbar.Detect() {
		if cli := cmuxbar.FindCLI(); cli != "" {
			_ = cmuxbar.Push(cli, s, now, false)
		}
	}
	return 0
}

func runCmux(args []string) int {
	if len(args) < 1 || (args[0] != "push" && args[0] != "clear") {
		fmt.Fprintln(os.Stderr, "usage: tacho cmux <push|clear>")
		return 2
	}
	cli := cmuxbar.FindCLI()
	if cli == "" {
		fmt.Fprintln(os.Stderr, "tacho: cmux CLI not found (is cmux installed? set TACHO_CMUX_BIN to override)")
		return 1
	}
	var err error
	if args[0] == "clear" {
		err = cmuxbar.Clear(cli, true)
	} else {
		now := time.Now()
		err = cmuxbar.Push(cli, core.Status(core.Options{Now: now}), now, true)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	return 0
}

// loadTemplate reads ~/.config/tachograph/statusline.tmpl (XDG and
// TACHO_CONFIG_DIR aware), falling back to the built-in default.
func loadTemplate() string {
	dir := os.Getenv("TACHO_CONFIG_DIR")
	if dir == "" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "tachograph")
		} else if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config", "tachograph")
		}
	}
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "statusline.tmpl")); err == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t
			}
		}
	}
	return render.DefaultTemplate
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit the unified schema JSON")
	noCache := fs.Bool("no-cache", false, "bypass the TTL cache")
	fs.Parse(args)

	if !*jsonOut {
		fmt.Fprintln(os.Stderr, "tacho status: use --json (or run bare `tacho` for the compact view)")
		return 2
	}
	s := core.Status(core.Options{NoCache: *noCache})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	return 0
}
