// Command check-release-assets checks a release's archives before the npm
// wrapper is published (#351): every archive the wrapper can download is
// listed in checksums.txt with a matching SHA-256, holds the tacho binary at
// its top level (where npm/install.js looks after extracting), and that
// binary was built for the archive's OS and architecture from the release's
// tag. It reads the archives from a directory — the release's assets as the
// release workflow downloads them, or GoReleaser's dist/ — and touches no
// network.
//
//	go run ./scripts/check-release-assets <dir> <tag>
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// platforms are the builds a release ships: .goreleaser.yaml's goos × goarch,
// which npm/asset.js maps Node's platform and arch onto.
var platforms = []struct{ goos, goarch string }{
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"linux", "amd64"}, {"linux", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// assetName is the archive npm/asset.js downloads for a platform.
func assetName(goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return "tachograph_" + goos + "_" + goarch + "." + ext
}

// binaryName is the file npm/install.js takes out of the archive.
func binaryName(goos string) string {
	if goos == "windows" {
		return "tacho.exe"
	}
	return "tacho"
}

// readBuildInfo reads the build information Go embeds in a binary; tests
// replace it so they needn't cross-compile six binaries.
var readBuildInfo = buildinfo.ReadFile

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: check-release-assets <dir> <tag>")
		os.Exit(2)
	}
	tag := normalizeTag(os.Args[2])
	if errs := check(os.Args[1], tag); len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintln(os.Stderr, "check-release-assets:", err)
		}
		os.Exit(1)
	}
	fmt.Printf("check-release-assets: %d archives OK for %s\n", len(platforms), tag)
}

// normalizeTag is the release tag for a version, v-prefixed (as npm/asset.js
// derives it).
func normalizeTag(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// check returns every problem found in dir's archives for tag, or none.
func check(dir, tag string) []error {
	sums, err := readChecksums(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return []error{err}
	}
	var errs []error
	for _, p := range platforms {
		if err := checkAsset(dir, tag, p.goos, p.goarch, sums); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// readChecksums parses GoReleaser's checksums.txt ("<sha256>  <file>" lines)
// into file → digest.
func readChecksums(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			sums[f[1]] = f[0]
		}
	}
	return sums, nil
}

// checkAsset checks one platform's archive: listed and matching in sums,
// holding the binary at its top level, built for goos/goarch with tag.
func checkAsset(dir, tag, goos, goarch string, sums map[string]string) error {
	name := assetName(goos, goarch)
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("%s: not listed in checksums.txt", name)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s: SHA-256 %x doesn't match checksums.txt's %s", name, got, want)
	}
	bin, err := extract(data, name, binaryName(goos))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return checkBinary(name, bin, tag, goos, goarch)
}

// extract returns the content of the archive's top-level file named binary.
func extract(data []byte, name, binary string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == binary && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("no %s at the archive's top level", binary)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s at the archive's top level", binary)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == binary && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

// checkBinary checks the binary's embedded build information: GOOS and
// GOARCH, and the main module's version, which Go stamps from the tagged
// checkout GoReleaser builds — so it is the tag. The -X main.version
// GoReleaser also injects can't be read here: -trimpath keeps -ldflags out
// of the build information. `tacho version` falls back to the module version
// when main.version is unset, so both name the same release.
func checkBinary(name string, bin []byte, tag, goos, goarch string) error {
	f, err := os.CreateTemp("", "check-release-assets-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(bin)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	info, err := readBuildInfo(f.Name())
	if err != nil {
		return fmt.Errorf("%s: reading the binary's build information: %w", name, err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != goos || settings["GOARCH"] != goarch {
		return fmt.Errorf("%s: binary built for %s/%s, want %s/%s", name, settings["GOOS"], settings["GOARCH"], goos, goarch)
	}
	if info.Main.Version != tag {
		return fmt.Errorf("%s: binary built from module version %q, want %s", name, info.Main.Version, tag)
	}
	return nil
}
