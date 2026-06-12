package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kosako/tachograph/internal/core"
	"github.com/kosako/tachograph/internal/render"
)

const version = "0.0.1-dev"

const usage = `usage:
  tacho                 one-shot compact status
  tacho watch [-n sec]  refresh continuously
  tacho status --json   unified schema JSON (see docs/schema.md)
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
