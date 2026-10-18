package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// No sessions directory yet means no rollouts, not a failure: daily reads it
// as a real zero.
func TestListRolloutsMissingSessions(t *testing.T) {
	files, err := ListRollouts(filepath.Join(t.TempDir(), "sessions"))
	if err != nil || files != nil {
		t.Fatalf("ListRollouts = %v, %v; want nil, nil", files, err)
	}
}

// Only non-directory .jsonl entries of YYYY/MM/DD directories are rollouts,
// listed in directory-name order with their day directory and mtime.
func TestListRolloutsLayout(t *testing.T) {
	sessions := filepath.Join(t.TempDir(), "sessions")
	d23 := filepath.Join(sessions, "2026", "05", "23")
	d24 := filepath.Join(sessions, "2026", "05", "24")
	mkdirs(t, d23, d24, filepath.Join(d24, "nested.jsonl"))
	mod23 := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)
	mod24 := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	writeRollout(t, d24, "b.jsonl", "{}", mod24)
	writeRollout(t, d23, "a.jsonl", "{}", mod23)
	writeRollout(t, d24, "notes.txt", "{}", mod24)
	writeRollout(t, filepath.Join(sessions, "2026", "05"), "month.jsonl", "{}", mod24)
	writeRollout(t, filepath.Join(sessions, "2026"), "year.jsonl", "{}", mod24)
	writeRollout(t, sessions, "top.jsonl", "{}", mod24)

	files, err := ListRollouts(sessions)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []RolloutFile{
		{filepath.Join(d23, "a.jsonl"), d23, mod23},
		{filepath.Join(d24, "b.jsonl"), d24, mod24},
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i := range want {
		if files[i].Path != want[i].Path || files[i].Day != want[i].Day || !files[i].Mod.Equal(want[i].Mod) {
			t.Errorf("files[%d] = %v, want %v", i, files[i], want[i])
		}
	}
}

// A directory that can't be listed doesn't stop the walk: the rest is still
// listed for the collector, and the failure is reported for daily (#187) —
// a day's directory and a month's alike, each the only one unreadable.
func TestListRolloutsKeepsWalkingPastUnreadableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory that permission bits make unreadable")
	}
	for _, c := range []struct {
		level, dir string   // the unreadable directory, under sessions
		want       []string // the rollouts still listed, under sessions
	}{
		{"day", "2026/05/23", []string{"2026/05/24/b.jsonl", "2026/06/01/c.jsonl"}},
		{"month", "2026/06", []string{"2026/05/23/a.jsonl", "2026/05/24/b.jsonl"}},
	} {
		t.Run(c.level, func(t *testing.T) {
			sessions := filepath.Join(t.TempDir(), "sessions")
			mod := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
			for _, f := range []string{"2026/05/23/a.jsonl", "2026/05/24/b.jsonl", "2026/06/01/c.jsonl"} {
				day := filepath.Join(sessions, filepath.Dir(filepath.FromSlash(f)))
				mkdirs(t, day)
				writeRollout(t, day, filepath.Base(f), "{}", mod)
			}
			dir := filepath.Join(sessions, filepath.FromSlash(c.dir))
			if err := os.Chmod(dir, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

			files, err := ListRollouts(sessions)
			if err == nil {
				t.Errorf("err = nil, want the failure to list %s", c.dir)
			}
			var got []string
			for _, f := range files {
				rel, _ := filepath.Rel(sessions, f.Path)
				got = append(got, filepath.ToSlash(rel))
			}
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Errorf("files = %v, want %v", got, c.want)
			}
		})
	}
}

// Anything but a missing sessions directory is a failure, not a real zero.
func TestListRolloutsSessionsNotADirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a plain file where the directory goes reads as missing on Windows, so it can't stand in for an unreadable directory there")
	}
	sessions := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(sessions, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ListRollouts(sessions)
	if err == nil || files != nil {
		t.Fatalf("ListRollouts = %v, %v; want nil and an error", files, err)
	}
}
