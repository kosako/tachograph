package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kosako/tachograph/internal/core"
)

const version = "0.0.1-dev"

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "--version", "version":
		fmt.Println("tacho " + version)
	case "status":
		os.Exit(runStatus(args[1:]))
	default:
		fmt.Fprintln(os.Stderr, "usage: tacho status [--json] [--no-cache]")
		os.Exit(2)
	}
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit the unified schema JSON")
	noCache := fs.Bool("no-cache", false, "bypass the TTL cache")
	fs.Parse(args)

	s := core.Status(core.Options{NoCache: *noCache})
	if !*jsonOut {
		// Human rendering arrives with the R2 renderer; JSON is the contract.
		fmt.Fprintln(os.Stderr, "tacho status: only --json is supported for now")
		return 2
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		fmt.Fprintln(os.Stderr, "tacho:", err)
		return 1
	}
	return 0
}
