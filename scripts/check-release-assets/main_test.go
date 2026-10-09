package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// fakeBinary is a stand-in binary whose content stubBuildInfo reads back as
// its platform and main module version, so the tests don't cross-compile
// six real binaries.
func fakeBinary(goos, goarch, version string) []byte {
	return []byte(goos + "\n" + goarch + "\n" + version)
}

// stubBuildInfo replaces readBuildInfo with a reader of fakeBinary files.
func stubBuildInfo(t *testing.T) {
	t.Helper()
	orig := readBuildInfo
	readBuildInfo = func(path string) (*buildinfo.BuildInfo, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f := strings.SplitN(string(b), "\n", 3)
		if len(f) != 3 {
			return nil, fmt.Errorf("not a fake binary")
		}
		return &debug.BuildInfo{Main: debug.Module{Version: f[2]}, Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: f[0]}, {Key: "GOARCH", Value: f[1]},
		}}, nil
	}
	t.Cleanup(func() { readBuildInfo = orig })
}

// archive packs files (name → content) the way GoReleaser does for goos: a
// zip for Windows, a tar.gz elsewhere.
func archive(t *testing.T, goos string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for name, content := range files {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			w.Write(content)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(content)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeRelease writes a release for tag into dir: every platform's archive,
// as GoReleaser lays it out (the binary next to LICENSE and the READMEs),
// and checksums.txt. edit, when non-nil, may change a platform's archive
// files before packing.
func writeRelease(t *testing.T, dir, tag string, edit func(goos, goarch string, files map[string][]byte)) {
	t.Helper()
	var sums strings.Builder
	for _, p := range platforms {
		files := map[string][]byte{
			binaryName(p.goos): fakeBinary(p.goos, p.goarch, tag),
			"LICENSE":          []byte("license"),
			"README.md":        []byte("readme"),
		}
		if edit != nil {
			edit(p.goos, p.goarch, files)
		}
		data := archive(t, p.goos, files)
		name := assetName(p.goos, p.goarch)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAcceptsAGoodRelease(t *testing.T) {
	stubBuildInfo(t)
	dir := t.TempDir()
	writeRelease(t, dir, "v1.2.3", nil)
	if errs := check(dir, "v1.2.3"); len(errs) != 0 {
		t.Errorf("check = %v, want no problems", errs)
	}
	if got := normalizeTag("1.2.3"); got != "v1.2.3" {
		t.Errorf("normalizeTag(1.2.3) = %q", got)
	}
}

// Each way an archive can be wrong is reported, naming the archive; one bad
// archive doesn't hide the others' state.
func TestCheckReportsBadArchives(t *testing.T) {
	stubBuildInfo(t)
	cases := []struct {
		name  string
		edit  func(dir string)
		want  string
		count int
	}{
		{"missing archive", func(dir string) { os.Remove(filepath.Join(dir, "tachograph_linux_arm64.tar.gz")) },
			"tachograph_linux_arm64.tar.gz", 1},
		{"not in checksums.txt", func(dir string) {
			p := filepath.Join(dir, "checksums.txt")
			b, _ := os.ReadFile(p)
			var kept []string
			for _, line := range strings.Split(string(b), "\n") {
				if !strings.Contains(line, "darwin_amd64") {
					kept = append(kept, line)
				}
			}
			os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644)
		}, "tachograph_darwin_amd64.tar.gz: not listed in checksums.txt", 1},
		{"checksum mismatch", func(dir string) {
			os.WriteFile(filepath.Join(dir, "tachograph_windows_arm64.zip"), []byte("tampered"), 0o644)
		}, "tachograph_windows_arm64.zip: SHA-256", 1},
		{"no checksums.txt", func(dir string) { os.Remove(filepath.Join(dir, "checksums.txt")) }, "checksums.txt", 1},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeRelease(t, dir, "v1.2.3", nil)
		c.edit(dir)
		errs := check(dir, "v1.2.3")
		if len(errs) != c.count || !strings.Contains(fmt.Sprint(errs), c.want) {
			t.Errorf("%s: check = %v, want %d problem(s) mentioning %q", c.name, errs, c.count, c.want)
		}
	}

	// What's inside the archive: the binary must sit at the top level, be
	// built for the archive's platform, and carry the release's version.
	for _, c := range []struct {
		name string
		edit func(goos, goarch string, files map[string][]byte)
		want string
	}{
		{"binary in a subdirectory", func(goos, goarch string, files map[string][]byte) {
			if goos == "darwin" && goarch == "arm64" {
				files["tachograph/tacho"] = files["tacho"]
				delete(files, "tacho")
			}
		}, "tachograph_darwin_arm64.tar.gz: no tacho at the archive's top level"},
		{"Windows binary without .exe", func(goos, goarch string, files map[string][]byte) {
			if goos == "windows" && goarch == "amd64" {
				files["tacho"] = files["tacho.exe"]
				delete(files, "tacho.exe")
			}
		}, "tachograph_windows_amd64.zip: no tacho.exe at the archive's top level"},
		{"binary for another arch", func(goos, goarch string, files map[string][]byte) {
			if goos == "linux" && goarch == "arm64" {
				files["tacho"] = fakeBinary("linux", "amd64", "v1.2.3")
			}
		}, "tachograph_linux_arm64.tar.gz: binary built for linux/amd64, want linux/arm64"},
		{"binary for another OS", func(goos, goarch string, files map[string][]byte) {
			if goos == "darwin" && goarch == "amd64" {
				files["tacho"] = fakeBinary("linux", "amd64", "v1.2.3")
			}
		}, "tachograph_darwin_amd64.tar.gz: binary built for linux/amd64, want darwin/amd64"},
		{"another version", func(goos, goarch string, files map[string][]byte) {
			if goos == "windows" && goarch == "arm64" {
				files["tacho.exe"] = fakeBinary("windows", "arm64", "v1.2.2")
			}
		}, "tachograph_windows_arm64.zip: binary built from module version \"v1.2.2\""},
		{"pre-release of the version", func(goos, goarch string, files map[string][]byte) {
			if goos == "linux" && goarch == "amd64" {
				files["tacho"] = fakeBinary("linux", "amd64", "v1.2.3-rc1")
			}
		}, "tachograph_linux_amd64.tar.gz: binary built from module version \"v1.2.3-rc1\""},
	} {
		dir := t.TempDir()
		writeRelease(t, dir, "v1.2.3", c.edit)
		errs := check(dir, "v1.2.3")
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), c.want) {
			t.Errorf("%s: check = %v, want one problem mentioning %q", c.name, errs, c.want)
		}
	}
}

// rewriteAsset replaces a release archive's content and its checksums.txt
// line, as a release whose archive was damaged before its checksum was taken.
func rewriteAsset(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "checksums.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasSuffix(line, "  "+name) {
			line = fmt.Sprintf("%x  %s", sha256.Sum256(data), name)
		}
		lines = append(lines, line)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The whole archive is read, as npm/install.js extracts it all: damage after
// the binary — a later entry, or the gzip trailer — fails the check even
// though the binary itself reads fine and the checksum matches.
func TestCheckReadsTheWholeArchive(t *testing.T) {
	stubBuildInfo(t)

	dir := t.TempDir()
	writeRelease(t, dir, "v1.2.3", nil)
	name := assetName("linux", "arm64")
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff // the gzip trailer's CRC-32
	rewriteAsset(t, dir, name, data)
	if errs := check(dir, "v1.2.3"); len(errs) != 1 || !strings.Contains(errs[0].Error(), name) {
		t.Errorf("damaged gzip trailer: check = %v, want one problem naming %s", errs, name)
	}

	// A zip whose LICENSE, stored after the binary, is damaged.
	dir = t.TempDir()
	writeRelease(t, dir, "v1.2.3", nil)
	name = assetName("windows", "amd64")
	payload := []byte("LICENSE-PAYLOAD-0123456789")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name    string
		content []byte
	}{
		{"tacho.exe", fakeBinary("windows", "amd64", "v1.2.3")},
		{"LICENSE", payload},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(f.content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipped := buf.Bytes()
	i := bytes.Index(zipped, payload)
	if i < 0 {
		t.Fatal("stored LICENSE not found in the zip")
	}
	zipped[i] ^= 0xff
	rewriteAsset(t, dir, name, zipped)
	if errs := check(dir, "v1.2.3"); len(errs) != 1 || !strings.Contains(errs[0].Error(), name+": LICENSE") {
		t.Errorf("damaged later zip entry: check = %v, want one problem naming %s: LICENSE", errs, name)
	}
}

// The premise of the version check, through debug/buildinfo rather than the
// stub: a binary built as GoReleaser builds it — from a clean checkout with
// the tag on HEAD, with -trimpath (which keeps -ldflags, and so the -X
// main.version, out of the build information) — carries the tag as its main
// module version, and its own platform.
func TestCheckBinaryOnATaggedBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	// A caller's git environment (a hook running the tests sets GIT_DIR and
	// GIT_INDEX_FILE) must not reach the temporary repository: every GIT_
	// variable is dropped. These traps would catch one that leaked through.
	trap := filepath.Join(t.TempDir(), "trap")
	t.Setenv("GIT_DIR", filepath.Join(trap, "git-dir"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(trap, "index"))
	t.Setenv("GIT_WORK_TREE", trap)

	repo := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GOENV=off", "GOFLAGS=-buildvcs=true", "GOWORK=off", "CGO_ENABLED=0")
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = repo, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	files := map[string]string{
		"go.mod":  "module example.com/tagged\n\ngo 1.24\n",
		"main.go": "package main\n\nvar version string\n\nfunc main() { println(version) }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		run(gitBin, append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	git("tag", "v1.2.3")

	bin := filepath.Join(t.TempDir(), "tacho")
	run(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath",
		"-ldflags", "-s -w -X main.version=v9.9.9", "-o", bin, ".")
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBinary("host", data, "v1.2.3", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Errorf("checkBinary on the tagged build = %v", err)
	}
	other := "amd64"
	if runtime.GOARCH == "amd64" {
		other = "arm64"
	}
	if err := checkBinary("host", data, "v1.2.3", runtime.GOOS, other); err == nil {
		t.Errorf("checkBinary accepted a %s binary as %s", runtime.GOARCH, other)
	}
	if err := checkBinary("host", data, "v9.9.9", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Error("checkBinary took the -X main.version (kept out of the build information by -trimpath) for the module version")
	}
	if _, err := os.Stat(trap); !os.IsNotExist(err) {
		t.Errorf("the caller's git environment reached the build: %s exists (%v)", trap, err)
	}
}
