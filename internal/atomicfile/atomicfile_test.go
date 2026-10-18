package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// noTempFiles fails the test if a temp file of Write is left in dir.
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("leftover temp files: %v", leftovers)
	}
}

// Write creates the file or replaces it whole, with the mode it is given:
// 0600 for the cache, the config, and the settings, 0755 for the SwiftBar
// plugin, which must be executable.
func TestWrite(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		old  string
		perm os.FileMode
	}{
		{"new.json", "", 0o600},
		{"replaced.json", "an older and longer content", 0o600},
		{"plugin.sh", "", 0o755},
	} {
		path := filepath.Join(dir, c.name)
		if c.old != "" {
			if err := os.WriteFile(path, []byte(c.old), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := Write(path, []byte("new"), c.perm); err != nil {
			t.Fatalf("Write(%s): %v", c.name, err)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
			t.Errorf("%s = %q, %v; want %q", c.name, b, err, "new")
		}
		if runtime.GOOS == "windows" {
			continue // Windows has no Unix permission bits to check
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != c.perm {
			t.Errorf("%s mode = %v; want %v", c.name, got, c.perm)
		}
	}
	noTempFiles(t, dir)
}

// A write whose rename fails returns the error, leaves what is at path
// alone, and removes its temp file, on every system: here a directory takes
// the file's place, which no rename can replace.
func TestWriteRemovesTempWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocked.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o600); err == nil {
		t.Fatal("Write over a directory succeeded")
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Errorf("after the failed write, %s = %v, %v; want the directory still there", path, fi, err)
	}
	noTempFiles(t, dir)
}

// symlinkOrSkip makes link a symlink to dest. Windows lets only privileged
// accounts (or developer mode) create one, so there a refusal skips the test
// instead of failing it.
func symlinkOrSkip(t *testing.T, dest, link string) {
	t.Helper()
	if err := os.Symlink(dest, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("can't create a symlink on windows: %v", err)
		}
		t.Fatal(err)
	}
}

// readLink returns where the symlink at path leads, failing the test when
// path is no longer a symlink.
func readLink(t *testing.T, path string) string {
	t.Helper()
	dest, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("%s is no longer a symlink: %v", path, err)
	}
	return dest
}

// WriteThrough replaces the file a symlink leads to — by an absolute link, a
// relative one, or a chain of them — in that file's own directory, with the
// mode it is given, and leaves every link as it was: settings kept in a
// dotfiles repository and linked into place stay linked (#413).
func TestWriteThroughFollowsLinks(t *testing.T) {
	for _, c := range []struct {
		name            string
		relative, chain bool
	}{
		{"absolute link", false, false},
		{"relative link", true, false},
		{"chain of links", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dotfiles, home := t.TempDir(), t.TempDir()
			target := filepath.Join(dotfiles, "config.json")
			if err := os.WriteFile(target, []byte("an older and longer content"), 0o644); err != nil {
				t.Fatal(err)
			}
			dest := target
			if c.relative {
				rel, err := filepath.Rel(home, target) // ../<dotfiles>/config.json: both sit in one test dir
				if err != nil {
					t.Fatal(err)
				}
				dest = rel
			}
			if c.chain {
				mid := filepath.Join(home, "mid.json")
				symlinkOrSkip(t, dest, mid)
				dest = mid
			}
			link := filepath.Join(home, "config.json")
			symlinkOrSkip(t, dest, link)

			if err := WriteThrough(link, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := readLink(t, link); got != dest {
				t.Errorf("link now leads to %q, want %q", got, dest)
			}
			if c.chain {
				if got := readLink(t, dest); got != target {
					t.Errorf("middle link now leads to %q, want %q", got, target)
				}
			}
			if b, err := os.ReadFile(target); err != nil || string(b) != "new" {
				t.Errorf("target = %q, %v; want %q", b, err, "new")
			}
			if fi, err := os.Stat(target); err != nil {
				t.Error(err)
			} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 { // no Unix permission bits there
				t.Errorf("target mode = %v; want 0600", fi.Mode().Perm())
			}
			noTempFiles(t, dotfiles)
			noTempFiles(t, home)
		})
	}
}

// Without a link in the way, WriteThrough is Write: it creates a missing
// file and replaces an existing one.
func TestWriteThroughWithoutLink(t *testing.T) {
	dir := t.TempDir()
	for _, old := range []string{"", "an older and longer content"} {
		path := filepath.Join(dir, "config.json")
		if old != "" {
			if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := WriteThrough(path, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
			t.Errorf("after replacing %q: %q, %v; want %q", old, b, err, "new")
		}
	}
	noTempFiles(t, dir)
}

// A link WriteThrough can't write through is an error that changes nothing:
// one leading nowhere or round in a loop (no file is created where it
// points), or one to a file in a directory that can't take the temp file,
// such as a read-only store (#413).
func TestWriteThroughFailsWithoutChanges(t *testing.T) {
	t.Run("leads nowhere", func(t *testing.T) {
		dotfiles, home := t.TempDir(), t.TempDir()
		target := filepath.Join(dotfiles, "config.json")
		link := filepath.Join(home, "config.json")
		symlinkOrSkip(t, target, link)
		if err := WriteThrough(link, []byte("new"), 0o600); err == nil {
			t.Fatal("WriteThrough through a dangling link succeeded")
		}
		if got := readLink(t, link); got != target {
			t.Errorf("link now leads to %q, want %q", got, target)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the link's target was created (stat err %v)", err)
		}
		noTempFiles(t, dotfiles)
		noTempFiles(t, home)
	})
	t.Run("loop", func(t *testing.T) {
		home := t.TempDir()
		a, b := filepath.Join(home, "a.json"), filepath.Join(home, "b.json")
		symlinkOrSkip(t, b, a)
		symlinkOrSkip(t, a, b)
		if err := WriteThrough(a, []byte("new"), 0o600); err == nil {
			t.Fatal("WriteThrough through a loop of links succeeded")
		}
		if readLink(t, a) != b || readLink(t, b) != a {
			t.Error("the links changed")
		}
		noTempFiles(t, home)
	})
	t.Run("read-only directory", func(t *testing.T) {
		dotfiles, home := t.TempDir(), t.TempDir()
		target := filepath.Join(dotfiles, "config.json")
		if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, "config.json")
		symlinkOrSkip(t, target, link)
		if err := os.Chmod(dotfiles, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dotfiles, 0o755) })
		if f, err := os.CreateTemp(dotfiles, "probe-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			t.Skip("the directory stays writable (running as root?)")
		}
		if err := WriteThrough(link, []byte("new"), 0o600); err == nil {
			t.Fatal("WriteThrough into a read-only directory succeeded")
		}
		if got := readLink(t, link); got != target {
			t.Errorf("link now leads to %q, want %q", got, target)
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != "old" {
			t.Errorf("target = %q, %v; want it unchanged", b, err)
		}
		noTempFiles(t, dotfiles)
		noTempFiles(t, home)
	})
}

// Write, which the cache uses, replaces a symlink at path with the file
// itself and leaves what it led to alone: a link planted in a shared cache
// dir can't steer the write elsewhere (#413).
func TestWriteReplacesLink(t *testing.T) {
	elsewhere, dir := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "status.json")
	symlinkOrSkip(t, target, path)
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s = %v, %v; want a regular file", path, fi, err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Errorf("the link's old target = %q, %v; want it untouched", b, err)
	}
}

func TestRetry(t *testing.T) {
	errTransient := errors.New("in the way")
	errOther := errors.New("broken")
	transient := func(err error) bool { return errors.Is(err, errTransient) }

	// run calls retry with f returning errs in turn (nil once they run out)
	// and reports f's calls and the waits retry slept.
	run := func(errs []error, budget time.Duration) (error, int, []time.Duration) {
		calls := 0
		var sleeps []time.Duration
		err := retry(func() error {
			calls++
			if calls <= len(errs) {
				return errs[calls-1]
			}
			return nil
		}, transient, budget, func(d time.Duration) { sleeps = append(sleeps, d) })
		return err, calls, sleeps
	}

	t.Run("transient failures until success", func(t *testing.T) {
		err, calls, sleeps := run([]error{errTransient, errTransient}, time.Second)
		if err != nil || calls != 3 {
			t.Fatalf("err = %v, calls = %d; want nil after 3 calls", err, calls)
		}
		if len(sleeps) != 2 || sleeps[0] != time.Millisecond || sleeps[1] < time.Millisecond || sleeps[1] >= 2*time.Millisecond {
			t.Errorf("sleeps = %v; want 1ms, then [1ms, 2ms)", sleeps)
		}
	})

	t.Run("stops within the budget", func(t *testing.T) {
		always := make([]error, 1000)
		for i := range always {
			always[i] = errTransient
		}
		const budget = 50 * time.Millisecond
		err, calls, sleeps := run(always, budget)
		if !errors.Is(err, errTransient) {
			t.Fatalf("err = %v; want the transient error", err)
		}
		var slept time.Duration
		for _, d := range sleeps {
			slept += d
		}
		if slept > budget || calls < 2 || calls != len(sleeps)+1 {
			t.Errorf("slept %v over %d calls (%d sleeps); want more than one call within %v", slept, calls, len(sleeps), budget)
		}
		// The waits grow, so the budget takes far fewer than 50 of them.
		if len(sleeps) >= 25 {
			t.Errorf("%d sleeps within %v; want growing waits", len(sleeps), budget)
		}
	})

	t.Run("other errors at once", func(t *testing.T) {
		err, calls, sleeps := run([]error{errOther}, time.Second)
		if !errors.Is(err, errOther) || calls != 1 || len(sleeps) != 0 {
			t.Errorf("err = %v, calls = %d, sleeps = %v; want the error after 1 call, no sleep", err, calls, sleeps)
		}
	})

	t.Run("no budget, no retry", func(t *testing.T) {
		err, calls, sleeps := run([]error{errTransient}, 0)
		if !errors.Is(err, errTransient) || calls != 1 || len(sleeps) != 0 {
			t.Errorf("err = %v, calls = %d, sleeps = %v; want the error after 1 call, no sleep", err, calls, sleeps)
		}
	})
}
