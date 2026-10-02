package main

import (
	"io"
	"os"
	"testing"
)

// capture runs fn with *target (os.Stdout or os.Stderr) redirected and
// returns what was written. The pipe is drained concurrently so output larger
// than its buffer can't block fn, and both ends are always closed.
func capture(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	orig := *target
	*target = w
	func() {
		defer func() {
			*target = orig
			w.Close()
		}()
		fn()
	}()
	return <-out
}
