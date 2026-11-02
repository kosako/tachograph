package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// goreleaserVersions returns the `version:` input of every
// goreleaser/goreleaser-action step in a workflow, as "file:line: value" for
// the message, and the values. A step without one gets "" (the action's
// default, a floating version).
func goreleaserVersions(t *testing.T, path string) (where []string, versions []string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	uses := regexp.MustCompile(`^(\s*)(- )?uses:\s*goreleaser/goreleaser-action@`)
	version := regexp.MustCompile(`^\s*version:\s*(\S+)\s*$`)
	for i, line := range lines {
		m := uses.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// The step's keys sit two deeper than its "- "; the step ends at the
		// next line no deeper than the "- " (the next step, or the job's
		// next key). "uses:" is either the step's first key, after the
		// "- ", or a later one, two deeper than it.
		dash := len(m[1])
		if m[2] == "" {
			dash -= 2
		}
		found := ""
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" || strings.HasPrefix(strings.TrimSpace(next), "#") {
				continue
			}
			if indent := len(next) - len(strings.TrimLeft(next, " ")); indent <= dash {
				break
			}
			if v := version.FindStringSubmatch(next); v != nil {
				found = v[1]
				break
			}
		}
		where = append(where, filepath.Base(path)+":"+strconv.Itoa(i+1)+": "+found)
		versions = append(versions, found)
	}
	return where, versions
}

// Every GoReleaser step in the workflows runs the same pinned version, and
// AGENTS.md names it: the PR checks and the release then build with the
// same GoReleaser, and raising it means changing every pin (#447; pinned in
// #351). Dependabot raises the action's ref, not this input.
func TestGoReleaserPinsAgree(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("workflows: %v, %v", files, err)
	}
	var where, versions []string
	perFile := map[string]int{}
	for _, f := range files {
		w, v := goreleaserVersions(t, f)
		where, versions = append(where, w...), append(versions, v...)
		perFile[filepath.Base(f)] += len(v)
	}
	// ci.yml checks the config and builds the snapshot; release.yml checks
	// and releases.
	if perFile["ci.yml"] < 2 || perFile["release.yml"] < 2 {
		t.Fatalf("GoReleaser steps found: %v; want 2 or more in each of ci.yml and release.yml", where)
	}
	pin := versions[0]
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(pin) {
		t.Fatalf("GoReleaser pins: %v; want an exact vX.Y.Z", where)
	}
	for _, v := range versions {
		if v != pin {
			t.Fatalf("GoReleaser pins disagree: %v", where)
		}
	}
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "GoReleaser is pinned (`" + pin + "`)"; !strings.Contains(string(agents), want) {
		t.Errorf("AGENTS.md doesn't say %q, the workflows' pin", want)
	}
}

// goreleaserVersions finds each step's input, and "" for a step without
// one, whichever way the step is written.
func TestGoReleaserVersionsParsesSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.yml")
	body := `jobs:
  a:
    steps:
      - uses: goreleaser/goreleaser-action@abc # v7
        with:
          # a comment
          version: v2.1.0
          args: check
      - name: no version
        uses: goreleaser/goreleaser-action@abc
        with:
          args: check
      - uses: actions/checkout@abc
        with:
          version: v9.9.9
      - uses: goreleaser/goreleaser-action@abc
        with:
          version: v2.2.0
      - name: version after uses
        uses: goreleaser/goreleaser-action@abc
        with:
          args: check
          version: v2.3.0
  b:
    steps:
      - uses: goreleaser/goreleaser-action@abc
  c:
    with:
      version: v9.9.8
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, got := goreleaserVersions(t, path)
	want := []string{"v2.1.0", "", "v2.2.0", "v2.3.0", ""}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("versions = %q, want %q", got, want)
	}
}
