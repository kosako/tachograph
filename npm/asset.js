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

// allAssets returns assetFor's result for every platform × arch the wrapper
// supports. CI hands the list to scripts/check-release-assets, which fails
// when it and the release's platforms disagree (#447).
function allAssets() {
  const out = [];
  for (const platform of Object.keys(PLATFORMS)) {
    for (const arch of Object.keys(ARCHES)) {
      out.push(assetFor(platform, arch));
    }
  }
  return out;
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

// CHECKSUM_LINE is the one line shape checksums.txt may hold: GoReleaser's
// lowercase SHA-256, two spaces, and a file name of printable ASCII. The
// release check (readChecksums in scripts/check-release-assets) parses the
// same pattern before publishing, so a file it passed reads the same here
// (#442).
const CHECKSUM_LINE = /^([0-9a-f]{64}) {2}([!-~]+)$/;

// checksumFor parses GoReleaser's checksums.txt and returns the hex digest for
// asset, or null when it isn't listed. Empty lines are skipped; it throws on
// any other line out of shape, and on a file listed more than once, rather
// than picking one of the lines. The release check refuses the same files.
function checksumFor(text, asset) {
  const sums = new Map();
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (lines[i] === "") continue;
    const m = CHECKSUM_LINE.exec(lines[i]);
    if (!m) {
      throw new Error(`checksums.txt line ${i + 1} is not "<sha256>  <file>": ${JSON.stringify(lines[i])}`);
    }
    if (sums.has(m[2])) {
      throw new Error(`checksums.txt lists ${m[2]} more than once`);
    }
    sums.set(m[2], m[1]);
  }
  return sums.has(asset) ? sums.get(asset) : null;
}

module.exports = { assetFor, allAssets, downloadURL, checksumsURL, checksumFor, PLATFORMS, ARCHES };
