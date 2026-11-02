// Maps the running platform to the GoReleaser release asset that carries the
// matching `tacho` binary. Kept pure and dependency-free so it can be unit
// tested without touching the network or filesystem.

const PLATFORMS = { darwin: "darwin", linux: "linux", win32: "windows" };
const ARCHES = { x64: "amd64", arm64: "arm64" };

// assetFor returns { asset, binary } for the given Node platform/arch, or
// throws with a clear message when the combination has no published build.
function assetFor(platform, arch) {
  const os = PLATFORMS[platform];
  const goarch = ARCHES[arch];
  if (!os || !goarch) {
    throw new Error(
      `tachograph: no prebuilt binary for ${platform}/${arch}. ` +
        `Install from source instead: go install github.com/kosako/tachograph/cmd/tacho@latest`,
    );
  }
  const ext = os === "windows" ? "zip" : "tar.gz";
  return {
    asset: `tachograph_${os}_${goarch}.${ext}`,
    binary: os === "windows" ? "tacho.exe" : "tacho",
  };
}

// tagFor normalizes a package version to its release tag (v-prefixed).
function tagFor(version) {
  return version.startsWith("v") ? version : `v${version}`;
}

// downloadURL builds the GitHub release download URL for a version + asset.
function downloadURL(version, asset) {
  return `https://github.com/kosako/tachograph/releases/download/${tagFor(version)}/${asset}`;
}

// checksumsURL is the GoReleaser checksums.txt for the matching release.
function checksumsURL(version) {
  return `https://github.com/kosako/tachograph/releases/download/${tagFor(version)}/checksums.txt`;
}

// checksumFor parses GoReleaser's "<sha256>  <filename>" lines and returns the
// hex digest for asset, or null when it isn't listed. It throws when asset is
// listed more than once, rather than picking one of the lines: the release
// check (scripts/check-release-assets) rejects such a checksums.txt too (#442).
function checksumFor(text, asset) {
  let found = null;
  for (const line of text.split("\n")) {
    const [hash, name] = line.trim().split(/\s+/);
    if (name === asset && hash) {
      if (found !== null) {
        throw new Error(`checksums.txt lists ${asset} more than once`);
      }
      found = hash;
    }
  }
  return found;
}

module.exports = { assetFor, downloadURL, checksumsURL, checksumFor, PLATFORMS, ARCHES };
