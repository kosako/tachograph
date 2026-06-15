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

// downloadURL builds the GitHub release download URL for a version + asset.
function downloadURL(version, asset) {
  const tag = version.startsWith("v") ? version : `v${version}`;
  return `https://github.com/kosako/tachograph/releases/download/${tag}/${asset}`;
}

module.exports = { assetFor, downloadURL, PLATFORMS, ARCHES };
