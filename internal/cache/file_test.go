package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A write whose rename fails leaves no temp file behind, on every system
// (#371): here a directory takes the cache file's place, which no rename can
// replace.
func TestWriteJSONRemovesTempWhenRenameFails(t *testing.T) {
	dir := setCacheDir(t)
	if err := os.Mkdir(filepath.Join(dir, "blocked.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON("blocked.json", map[string]int{"a": 1}); err == nil {
		t.Fatal("WriteJSON over a directory succeeded")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("leftover temp files after a failed write: %v", leftovers)
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
