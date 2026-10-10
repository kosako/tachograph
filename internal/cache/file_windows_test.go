//go:build windows

package cache

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// openReader opens path the way the cache's readers do and closes it at the
// end of the test unless the caller closes it first.
func openReader(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// A rename over a file a reader holds open fails with an error the write
// retries (#371).
func TestRenameOverAnOpenFileIsTransient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	writeFile(t, path, "old")
	openReader(t, path)

	tmp := filepath.Join(dir, "status.json.tmp-1")
	writeFile(t, tmp, "new")
	err := os.Rename(tmp, path)
	if err == nil {
		t.Skip("this Windows renames over a file a reader holds open")
	}
	if !transientRenameError(err) {
		t.Errorf("transientRenameError(%v) = false; want the write to retry it", err)
	}
}

// replaceFile goes through once the reader in its way closes the file.
func TestReplaceFileWaitsForAReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	writeFile(t, path, "old")
	f := openReader(t, path)
	go func() {
		time.Sleep(20 * time.Millisecond)
		f.Close()
	}()

	tmp := filepath.Join(dir, "status.json.tmp-1")
	writeFile(t, tmp, "new")
	if err := replaceFile(tmp, path); err != nil {
		t.Fatalf("replaceFile with a reader closing after 20ms: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
		t.Errorf("after replaceFile: %q, %v; want the new file", b, err)
	}
}

// A write whose rename stays blocked past the retry budget fails, leaves
// the cache file as it was, and removes its temp file.
func TestWriteJSONGivesUpOnAHeldFile(t *testing.T) {
	dir := setCacheDir(t)
	path := filepath.Join(dir, "held.json")
	writeFile(t, path, `"old"`)
	openReader(t, path)

	start := time.Now()
	err := WriteJSON("held.json", "new")
	if !transientRenameError(err) {
		t.Fatalf("WriteJSON over a held file = %v; want the rename's transient error", err)
	}
	if took := time.Since(start); took > 5*renameRetryBudget {
		t.Errorf("WriteJSON took %v; want it to stop near the %v budget", took, renameRetryBudget)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != `"old"` {
		t.Errorf("held file = %q, %v; want it unchanged", b, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("leftover temp files after a failed write: %v", leftovers)
	}
}

func TestTransientRenameError(t *testing.T) {
	link := func(errno syscall.Errno) error {
		return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errno}
	}
	cases := []struct {
		err  error
		want bool
	}{
		{link(syscall.ERROR_ACCESS_DENIED), true},
		{link(errSharingViolation), true},
		{link(syscall.ERROR_FILE_NOT_FOUND), false},
		{link(syscall.ERROR_PATH_NOT_FOUND), false},
		{errors.New("other"), false},
	}
	for _, c := range cases {
		if got := transientRenameError(c.err); got != c.want {
			t.Errorf("transientRenameError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
