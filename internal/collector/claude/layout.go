package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Where Claude Code keeps its transcripts, an external contract owned by
// Claude Code: each session's main transcript is
// <root>/projects/<project>/<session>.jsonl, and the session's nested
// transcripts (subagents, workflows) sit under
// <root>/projects/<project>/<session>/ at any depth. Every reader of the
// layout (this collector's transcript route, internal/daily, internal/core,
// tacho doctor) goes through the functions here, so a change to it is made
// in one place (#446). Which transcripts a reader wants (the newest one, or
// all of them) and what a gap means stay the reader's call.

// ProjectsDir is the directory holding the project directories under the
// Claude config root.
func ProjectsDir(root string) string {
	return filepath.Join(root, "projects")
}

// SessionDir is the directory holding the nested transcripts of the session
// whose main transcript is at transcriptPath. It may not exist: a session
// without subagents or workflows has none.
func SessionDir(transcriptPath string) string {
	return strings.TrimSuffix(transcriptPath, ".jsonl")
}

// IsTranscript reports whether a directory entry is a transcript: a
// non-directory .jsonl entry.
func IsTranscript(e fs.DirEntry) bool {
	return !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl"
}

// WalkTranscripts calls fn for every transcript under projects (ProjectsDir),
// main and nested, project by project in lexical order. Entries of projects
// that aren't directories are skipped. A missing projects directory holds no
// transcripts and is not an error. The walk stops at the first error and
// returns it: projects or a directory under it can't be listed, or fn
// returns one.
func WalkTranscripts(projects string, fn func(path string, f fs.DirEntry) error) error {
	dirs, err := os.ReadDir(projects)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		err := filepath.WalkDir(filepath.Join(projects, d.Name()), func(path string, f fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !IsTranscript(f) {
				return nil
			}
			return fn(path, f)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
