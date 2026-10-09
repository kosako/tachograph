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

// The real build information, through debug/buildinfo rather than the stub:
// a binary built here with GoReleaser's flags (-trimpath, which keeps
// -ldflags out of it) reads back as this platform and its main module
// version. Without VCS stamping that version is "(devel)"; a release build
// from the tagged checkout carries the tag instead.
func TestCheckBinaryReadsRealBuildInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	bin := filepath.Join(t.TempDir(), "tacho")
	cmd := exec.Command(gobin, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.version=v9.9.9", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBinary("host", data, "(devel)", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Errorf("checkBinary on a real binary = %v", err)
	}
	other := "amd64"
	if runtime.GOARCH == "amd64" {
		other = "arm64"
	}
	if err := checkBinary("host", data, "(devel)", runtime.GOOS, other); err == nil {
		t.Errorf("checkBinary accepted a %s binary as %s", runtime.GOARCH, other)
	}
	if err := checkBinary("host", data, "v9.9.9", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Error("checkBinary took the -X main.version (not in the build information under -trimpath) for the module version")
	}
}
