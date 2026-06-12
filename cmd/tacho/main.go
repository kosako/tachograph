package main

import (
	"fmt"
	"os"
)

const version = "0.0.1-dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("tacho " + version)
		return
	}
	fmt.Fprintln(os.Stderr, "tacho: under construction — see https://github.com/kosako/tachograph")
	os.Exit(1)
}
