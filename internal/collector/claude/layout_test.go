package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeEmpty(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// WalkTranscripts visits every main and nested transcript of every project,
// and nothing else: not other files, not a directory named like a
// transcript, not a transcript lying directly in projects (#446).
func TestWalkTranscripts(t *testing.T) {
	root := t.TempDir()
	projects := ProjectsDir(root)
	for _, p := range []string{
		"a/s1.jsonl",
		"a/s1/subagents/agent-1.jsonl",
		"a/s1/workflows/w/agent-2.jsonl",
		"a/notes.txt",
		"a/dir.jsonl/inner.jsonl",
		"b/s2.jsonl",
		"stray.jsonl",
	} {
		writeEmpty(t, filepath.Join(projects, filepath.FromSlash(p)))
	}

	var got []string
	err := WalkTranscripts(projects, func(path string, f fs.DirEntry) error {
		rel, _ := filepath.Rel(projects, path)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "a/dir.jsonl/inner.jsonl a/s1/subagents/agent-1.jsonl a/s1/workflows/w/agent-2.jsonl a/s1.jsonl b/s2.jsonl"
	if strings.Join(got, " ") != want {
		t.Errorf("visited %q, want %q", strings.Join(got, " "), want)
	}
	// A session's nested transcripts are under its SessionDir.
	main := filepath.Join(projects, "a", "s1.jsonl")
	if want := filepath.Join(projects, "a", "s1"); SessionDir(main) != want {
		t.Errorf("SessionDir(%q) = %q, want %q", main, SessionDir(main), want)
	}
}

// No projects directory yet is no transcripts, not an error; fn's error
// stops the walk and is returned.
func TestWalkTranscriptsErrors(t *testing.T) {
	calls := 0
	if err := WalkTranscripts(filepath.Join(t.TempDir(), "projects"), func(string, fs.DirEntry) error {
		calls++
		return nil
	}); err != nil || calls != 0 {
		t.Errorf("missing projects: err = %v, calls = %d; want nil and 0", err, calls)
	}

	projects := ProjectsDir(t.TempDir())
	writeEmpty(t, filepath.Join(projects, "a", "s1.jsonl"))
	writeEmpty(t, filepath.Join(projects, "b", "s2.jsonl"))
	stop := errors.New("stop")
	calls = 0
	err := WalkTranscripts(projects, func(string, fs.DirEntry) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("fn error: err = %v, calls = %d; want stop after 1", err, calls)
	}
}

// A project directory that can't be listed is an error: the caller can't
// tell what it holds.
func TestWalkTranscriptsUnreadableProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions don't deny listing on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root lists any directory")
	}
	projects := ProjectsDir(t.TempDir())
	locked := filepath.Join(projects, "a")
	writeEmpty(t, filepath.Join(locked, "s1.jsonl"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if err := WalkTranscripts(projects, func(string, fs.DirEntry) error { return nil }); err == nil {
		t.Error("err = nil, want the unreadable project's error")
	}
}
