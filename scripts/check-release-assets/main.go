// Command check-release-assets checks a release's archives before the npm
// wrapper is published (#351): every archive the wrapper can download is
// listed in checksums.txt with a matching SHA-256, holds the tacho binary at
// its top level (where npm/install.js looks after extracting), and that
// binary was built for the archive's OS and architecture from the release's
// tag. It reads the archives from a directory — the release's assets as the
// release workflow downloads them, or GoReleaser's dist/ — and touches no
// network. It also fails on an archive it doesn't know, and with
// -npm-assets compares its platforms with the archives the npm wrapper
// downloads (#447).
//
//	go run ./scripts/check-release-assets [-npm-assets file] <dir> <tag>
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
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// A GoReleaser snapshot (CI's per-PR build, #353) isn't built from a
	// tag, so its binaries carry a pseudo-version: everything but the
	// version is checked then.
	skipVersion := flag.Bool("skip-version", false, "don't check the binaries' version (a snapshot, built off any tag)")
	npmAssets := flag.String("npm-assets", "", "also compare the platforms with the npm wrapper's archives, listed in `file` (\"<asset> <binary>\" lines)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: check-release-assets [-npm-assets file] <dir> <tag>\n       check-release-assets [-npm-assets file] -skip-version <dir>")
	}
	flag.Parse()
	args := flag.Args()
	var dir, tag string
	switch {
	case *skipVersion && len(args) == 1:
		dir = args[0]
	case !*skipVersion && len(args) == 2:
		dir, tag = args[0], normalizeTag(args[1])
	default:
		flag.Usage()
		os.Exit(2)
	}
	errs := check(dir, tag)
	if *npmAssets != "" {
		errs = append(errs, checkNPMAssets(*npmAssets)...)
	}
	if len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintln(os.Stderr, "check-release-assets:", err)
		}
		os.Exit(1)
	}
	what := tag
	if tag == "" {
		what = "a snapshot (version not checked)"
	}
	fmt.Printf("check-release-assets: %d archives OK for %s\n", len(platforms), what)
}

// normalizeTag is the release tag for a version, v-prefixed (as npm/asset.js
// derives it).
func normalizeTag(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// check returns every problem found in dir's archives for tag, or none. An
// empty tag leaves the binaries' version unchecked (a snapshot). Besides
// checking each platform's archive, it fails on an archive it doesn't know —
// in checksums.txt, or a tachograph_* file in dir — so a target added to
// .goreleaser.yaml without adding it here (and to npm/asset.js) isn't
// published unchecked (#447).
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
	return append(errs, unknownArchives(dir, sums)...)
}

// unknownArchives reports the archives in checksums.txt, and the regular
// files named tachograph_* in dir, that aren't a platform's archive. In
// GoReleaser's dist/ and in a downloaded release, those files are the
// archives (the per-build directories are named after the build, tacho_*).
func unknownArchives(dir string, sums map[string]string) []error {
	known := map[string]bool{}
	for _, p := range platforms {
		known[assetName(p.goos, p.goarch)] = true
	}
	unknown := map[string]bool{}
	for name := range sums {
		if !known[name] {
			unknown[name] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []error{err}
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), "tachograph_") && !known[e.Name()] {
			unknown[e.Name()] = true
		}
	}
	names := make([]string, 0, len(unknown))
	for name := range unknown {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		errs = append(errs, fmt.Errorf("%s: not an archive this check knows; add its platform to platforms here and to npm/asset.js", name))
	}
	return errs
}

// checkNPMAssets compares the archives the npm wrapper downloads, as listed
// in the file at path ("<asset> <binary>" lines, from npm/asset.js's
// allAssets), with platforms: a target one side has and the other doesn't
// would ship unchecked or be downloaded from nowhere (#447).
func checkNPMAssets(path string) []error {
	b, err := os.ReadFile(path)
	if err != nil {
		return []error{err}
	}
	want := map[string]string{}
	for _, p := range platforms {
		want[assetName(p.goos, p.goarch)] = binaryName(p.goos)
	}
	got := map[string]string{}
	var errs []error
	for i, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return []error{fmt.Errorf("%s line %d is not \"<asset> <binary>\": %q", path, i+1, line)}
		}
		if _, dup := got[f[0]]; dup {
			return []error{fmt.Errorf("%s lists %s more than once", path, f[0])}
		}
		got[f[0]] = f[1]
	}
	for _, name := range sortedKeys(want) {
		switch bin, ok := got[name]; {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: a release archive the npm wrapper doesn't download", name))
		case bin != want[name]:
			errs = append(errs, fmt.Errorf("%s: the npm wrapper looks for %s in it, the archive holds %s", name, bin, want[name]))
		}
	}
	for _, name := range sortedKeys(got) {
		if _, ok := want[name]; !ok {
			errs = append(errs, fmt.Errorf("%s: the npm wrapper downloads it, but no release archive is checked for it", name))
		}
	}
	return errs
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checksumLine is the one line shape checksums.txt may hold: GoReleaser's
// lowercase SHA-256, two spaces, and a file name of printable ASCII. The npm
// installer (checksumFor in npm/asset.js) parses the same pattern, so a file
// that passes here reads the same there (#442).
var checksumLine = regexp.MustCompile(`^([0-9a-f]{64})  ([!-~]+)$`)

// readChecksums parses GoReleaser's checksums.txt into file → digest. Empty
// lines are skipped; any other line out of shape, or a file listed twice, is
// an error rather than a line one parser skips or picks and the other
// doesn't: the npm installer takes the first line and a map the last, so a
// release whose lines disagree would pass here and fail every install.
func readChecksums(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for i, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		m := checksumLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("checksums.txt line %d is not \"<sha256>  <file>\": %q", i+1, line)
		}
		if _, dup := sums[m[2]]; dup {
			return nil, fmt.Errorf("checksums.txt lists %s more than once", m[2])
		}
		sums[m[2]] = m[1]
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
// It reads the whole archive — every entry, and a tar.gz to its gzip trailer
// — so damage anywhere fails here as it would fail npm/install.js, which
// extracts it all, even when the binary itself reads fine.
func extract(data []byte, name, binary string) ([]byte, error) {
	var bin []byte
	found := false
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			content, err := readZipFile(f) // checks the entry's CRC-32
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			if f.Name == binary && !f.FileInfo().IsDir() {
				bin, found = content, true
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", hdr.Name, err)
			}
			if hdr.Name == binary && hdr.Typeflag == tar.TypeReg {
				bin, found = content, true
			}
		}
		// The tar ends before the gzip stream does; reading the rest checks
		// the gzip trailer's CRC-32 and size.
		if _, err := io.Copy(io.Discard, gz); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, fmt.Errorf("no %s at the archive's top level", binary)
	}
	return bin, nil
}

// readZipFile reads a zip entry fully, which checks its CRC-32.
func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
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
	if tag != "" && info.Main.Version != tag {
		return fmt.Errorf("%s: binary built from module version %q, want %s", name, info.Main.Version, tag)
	}
	return nil
}
