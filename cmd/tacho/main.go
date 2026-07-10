package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kosako/tachograph/internal/cache"
	"github.com/kosako/tachograph/internal/cmuxbar"
	"github.com/kosako/tachograph/internal/collector/claude"
	"github.com/kosako/tachograph/internal/config"
	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/menubar"
	"github.com/kosako/tachograph/internal/pricing"
	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
	"github.com/kosako/tachograph/internal/swiftbar"
)

// version is injected at release-build time via -ldflags "-X main.version=…"
// (GoReleaser). It's empty for `go install` and local builds, which fall back
// to build info — GoReleaser uses plain `go build`, so its binaries carry no
// module version and need this explicit injection.
var version string

// fallbackVersion is reported when neither an injected version nor a build-info
// module version is available — local `go build` / `go run`.
const fallbackVersion = "dev"

// buildVersion resolves the version to display, preferring the injected value,
// then the module version the Go toolchain embeds at install time (so
// `go install ...@v0.1.0` reports "v0.1.0"), then the fallback.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		return normalizeVersion(info.Main.Version)
	}
	return fallbackVersion
}

// normalizeVersion maps the build-info main module version to what we print:
// an empty or "(devel)" value (local builds) becomes fallbackVersion.
func normalizeVersion(v string) string {
	if v == "" || v == "(devel)" {
		return fallbackVersion
	}
	return v
}

const usage = `usage:
  tacho                 one-shot compact status
  tacho watch [-n sec]  refresh continuously
  tacho status --json   unified schema JSON (see docs/schema.md)
  tacho statusline      Claude Code statusLine adapter (reads stdin JSON)
  tacho version         print the installed version
  tacho cmux push       push status pills to the cmux sidebar once
  tacho cmux clear      remove tacho's pills from the cmux sidebar
  tacho swiftbar        SwiftBar/xbar plugin output (see contrib/tacho.30s.sh)
  tacho config show     print the current configuration
  tacho config set K V  set a config value (e.g. menubar.metric cost)
  tacho setup claude    print/install the Claude Code statusLine config
  tacho doctor          diagnose install path, data sources, cache, and integrations
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version":
		fmt.Println("tacho " + buildVersion())
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
	case "setup":
		os.Exit(runSetup(args))
	case "doctor":
		os.Exit(runDoctor(args))
	case "":
		if len(args) > 0 && args[0] == "--version" {
			fmt.Println("tacho " + buildVersion())
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
	swiftbar.MenuDark = systemDark() // dropdown follows the system appearance
	if exe, err := os.Executable(); err == nil {
		swiftbar.BinPath = exe // so dropdown settings click the same binary
	}
	s := core.Status(core.Options{Now: now})
	shown := cfg.FilterStatus(s)
	if *pngOut != "" {
		b64, ok := menubar.PNGBase64(shown, dark, cfg.Menubar.Metric)
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
  tacho config statusline-preset <name>  write a preset to statusline.tmpl
  tacho config statusline-preset --list  list available presets

keys:
  tools           comma-separated: claude-code,codex  (which tools to show)
  menubar.style   meter | number
  menubar.metric  ` + "limit_5h | limit_weekly | cost | tokens" + `
`

func runConfig(args []string) int {
	sub := "show"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "show":
		c, err := config.LoadStrict()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tacho: invalid config, showing defaults:", err)
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		fmt.Println(string(b))
		if err != nil {
			return 1
		}
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
	case "toggle-tool":
		if len(args) != 1 {
			fmt.Fprint(os.Stderr, configUsage)
			return 2
		}
		return configToggleTool(args[0])
	case "statusline-preset":
		return configStatuslinePreset(args)
	default:
		fmt.Fprint(os.Stderr, configUsage)
		return 2
	}
}

// configToggleTool adds or removes a tool, keeping canonical order.
func configToggleTool(name string) int {
	if name != schema.ToolClaudeCode && name != schema.ToolCodex {
		fmt.Fprintf(os.Stderr, "tacho: unknown tool %q\n", name)
		return 2
	}
	c, err := config.LoadStrict()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacho: refusing to overwrite an unreadable config, fix or remove it first:", err)
		return 1
	}
	enabled := map[string]bool{}
	for _, t := range c.Tools {
		enabled[t] = true
	}
	enabled[name] = !enabled[name]
	// Non-nil empty slice so an all-off selection persists as "tools": [] (an
	// explicit "show nothing"), not "tools": null (which reloads as defaults).
	c.Tools = []string{}
	for _, t := range []string{schema.ToolClaudeCode, schema.ToolCodex} {
		if enabled[t] {
			c.Tools = append(c.Tools, t)
		}
	}
	if err := config.Save(c); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	return 0
}

// configStatuslinePreset lists the preset catalog or writes a chosen preset to
// ~/.config/tachograph/statusline.tmpl so `tacho statusline` picks it up.
func configStatuslinePreset(args []string) int {
	if len(args) == 0 || args[0] == "--list" || args[0] == "-l" {
		for _, p := range render.Presets {
			fmt.Printf("%-9s %s\n", p.Name, p.Desc)
			fmt.Printf("          %s\n", p.Template)
		}
		return 0
	}
	name := args[0]
	tmpl, ok := render.PresetTemplate(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "tacho: unknown preset %q (want one of: %s)\n", name, strings.Join(render.PresetNames(), ", "))
		return 2
	}
	dir := config.Dir()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "tacho: cannot locate the config directory")
		return 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	path := filepath.Join(dir, "statusline.tmpl")
	if err := os.WriteFile(path, []byte(tmpl+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	fmt.Println("Wrote preset " + name + " to " + path)
	fmt.Println(tmpl)
	return 0
}

func configSet(key, val string) int {
	c, err := config.LoadStrict()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacho: refusing to overwrite an unreadable config, fix or remove it first:", err)
		return 1
	}
	switch key {
	case "tools":
		tools := []string{} // non-nil so an empty selection persists as [] (not null → defaults)
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
		if !render.ValidMenubarMetric(val) {
			fmt.Fprintf(os.Stderr, "tacho: invalid menu bar metric %q (want one of: %s)\n", val, strings.Join(render.MenubarMetrics, ", "))
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

// systemDark reports macOS dark mode. Unlike the menu bar (which follows the
// wallpaper), the dropdown menu follows the system appearance, so this is the
// right signal for the dropdown text color.
func systemDark() bool {
	out, _ := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	return strings.Contains(string(out), "Dark")
}

func runOnce(args []string) int {
	fs := flag.NewFlagSet("tacho", flag.ExitOnError)
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	noCache := fs.Bool("no-cache", false, "bypass the TTL cache")
	fs.Parse(args)

	now := time.Now()
	s := config.Load().FilterStatus(core.Status(core.Options{Now: now, NoCache: *noCache}))
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
		s := watchStatus(now)
		// Clear screen and home the cursor between refreshes.
		fmt.Print("\x1b[H\x1b[2J")
		fmt.Printf("tachograph  %s  (every %ds, ctrl-c to quit)\n\n", now.Format("15:04:05"), *interval)
		fmt.Println(render.StatusLines(s, now, st))
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

func watchStatus(now time.Time) schema.Status {
	return config.Load().FilterStatus(core.Status(core.Options{Now: now, NoCache: true}))
}

// runStatusline is the R1 renderer: it consumes the session JSON Claude
// Code pipes in, snapshots it so other renderers can reuse the rate limits
// (the piggyback design from issue #4), and prints one templated line.
func runStatusline(args []string) int {
	return runStatuslineWithIO(args, os.Stdin, os.Stdout, time.Now())
}

func runStatuslineWithIO(args []string, stdin io.Reader, stdout io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("statusline", flag.ExitOnError)
	tmplFlag := fs.String("template", "", "override the statusline template")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	fs.Parse(args)

	input, _ := io.ReadAll(stdin)

	claudeTool := claude.Collect(claude.Options{Now: now, StatuslineInput: input})
	// The live payload knows only the current session; total today's portion of
	// it from the transcript so {claude.*.session.today} works.
	core.AddSessionToday(&claudeTool, now, pricing.Load())
	if shouldWriteStatuslineSnapshot(input, claudeTool) {
		preserveSnapshotLimits(&claudeTool, now)
		_ = cache.WriteSnapshot(claudeTool)
	}
	s := core.Status(core.Options{Now: now}) // codex side rides the TTL cache
	for i := range s.Tools {
		if s.Tools[i].Tool == schema.ToolClaudeCode {
			// Keep today's all-session aggregate that core attached; the
			// statusline payload only knows the current session.
			claudeTool.Daily = s.Tools[i].Daily
			s.Tools[i] = claudeTool
		}
	}

	tmpl := *tmplFlag
	if tmpl == "" {
		tmpl = loadTemplate()
	}
	fmt.Fprintln(stdout, render.Template(tmpl, s, now, style(*noColor)))

	// R3 piggyback: inside a cmux terminal, mirror the status to the
	// sidebar. Fire-and-forget so the statusline stays fast.
	if cmuxbar.Detect() {
		if cli := cmuxbar.FindCLI(); cli != "" {
			_ = cmuxbar.Push(cli, config.Load().FilterStatus(s), now, false)
		}
	}
	return 0
}

func shouldWriteStatuslineSnapshot(input []byte, t schema.Tool) bool {
	if strings.TrimSpace(string(input)) == "" || !t.Available || t.Error != nil {
		return false
	}
	if len(t.Limits) > 0 || t.Model != nil || t.Plan != nil || t.Credits != nil {
		return true
	}
	if t.Session != nil {
		if t.Session.ID != nil || t.Session.CWD != nil || t.Session.ContextWindow != nil ||
			t.Session.ContextUsedPct != nil || t.Session.Tokens != nil || t.Session.TranscriptPath != nil {
			return true
		}
	}
	if t.Fallback != nil {
		return t.Fallback.SessionTokens != nil || t.Fallback.EstimatedCostUSD != nil
	}
	return false
}

func preserveSnapshotLimits(t *schema.Tool, now time.Time) {
	if len(t.Limits) > 0 {
		return
	}
	if snap, ok := cache.ReadSnapshot(t.Tool, cache.SnapshotMaxAge, now); ok && len(snap.Limits) > 0 {
		t.Limits = snap.Limits
	}
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
		s := config.Load().FilterStatus(core.Status(core.Options{Now: now}))
		err = cmuxbar.Push(cli, s, now, true)
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
			if t := render.FirstTemplateLine(string(b)); t != "" {
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
