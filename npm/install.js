// postinstall: download the tacho binary matching this platform from the
// GitHub release for this package's version, and extract it into bin/.
//
// Archives are produced by GoReleaser (tar.gz everywhere except Windows zip).
// We extract via the system `tar`, which is bsdtar on macOS/Windows (handles
// both formats) and GNU tar on Linux (handles our tar.gz). This keeps the
// package dependency-free.

const fs = require("fs");
const os = require("os");
const path = require("path");
const crypto = require("crypto");
const { execFileSync } = require("child_process");
const { assetFor, downloadURL, checksumsURL, checksumFor } = require("./asset");

const { version } = require("./package.json");
const binDir = path.join(__dirname, "bin");

// Abort hung downloads instead of letting `npm install` block forever; the
// timeout covers headers and body, and the catch at the bottom points users
// at the go-install fallback.
const FETCH_TIMEOUT_MS = 120_000;

// fetchBuffer downloads url fully and maps failures (HTTP errors, timeouts)
// to messages that name what was being fetched.
async function fetchBuffer(url, what) {
  try {
    const res = await fetch(url, {
      redirect: "follow",
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
    });
    if (!res.ok) {
      throw new Error(`${what} failed: ${res.status} ${res.statusText} for ${url}`);
    }
    return Buffer.from(await res.arrayBuffer());
  } catch (err) {
    if (err.name === "TimeoutError") {
      throw new Error(`${what} timed out after ${FETCH_TIMEOUT_MS / 1000}s for ${url}`);
    }
    throw err;
  }
}

async function main() {
  const { asset, binary } = assetFor(process.platform, process.arch);

  const buf = await fetchBuffer(downloadURL(version, asset), "download");

  // Verify the archive against GoReleaser's published checksums.txt before
  // extracting and running it, so a corrupted/tampered/partial download is
  // caught rather than executed.
  const sums = await fetchBuffer(checksumsURL(version), "checksums download");
  const want = checksumFor(sums.toString("utf8"), asset);
  if (!want) {
    throw new Error(`no checksum listed for ${asset}`);
  }
  const got = crypto.createHash("sha256").update(buf).digest("hex");
  if (got !== want) {
    throw new Error(`checksum mismatch for ${asset}: expected ${want}, got ${got}`);
  }

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "tacho-"));
  const archive = path.join(tmp, asset);
  fs.writeFileSync(archive, buf);

  fs.mkdirSync(binDir, { recursive: true });
  // bsdtar/GNU tar both extract by extension; -C lands the binary in bin/.
  execFileSync("tar", ["-xf", archive, "-C", binDir], { stdio: "inherit" });
  fs.rmSync(tmp, { recursive: true, force: true });

  const binPath = path.join(binDir, binary);
  if (!fs.existsSync(binPath)) {
    throw new Error(`extracted archive but ${binary} not found in ${binDir}`);
  }
  if (process.platform !== "win32") {
    fs.chmodSync(binPath, 0o755);
  }
  console.log(`tachograph: installed ${binary} ${version}`);
}

main().catch((err) => {
  console.error(`\n${err.message}\n`);
  console.error(
    "You can install from source instead:\n" +
      "  go install github.com/kosako/tachograph/cmd/tacho@latest\n",
  );
  process.exit(1);
});
