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

async function main() {
  const { asset, binary } = assetFor(process.platform, process.arch);
  const url = downloadURL(version, asset);

  const res = await fetch(url, { redirect: "follow" });
  if (!res.ok) {
    throw new Error(`download failed: ${res.status} ${res.statusText} for ${url}`);
  }
  const buf = Buffer.from(await res.arrayBuffer());

  // Verify the archive against GoReleaser's published checksums.txt before
  // extracting and running it, so a corrupted/tampered/partial download is
  // caught rather than executed.
  const sums = await fetch(checksumsURL(version), { redirect: "follow" });
  if (!sums.ok) {
    throw new Error(`checksums download failed: ${sums.status} ${sums.statusText}`);
  }
  const want = checksumFor(await sums.text(), asset);
  if (!want) {
    throw new Error(`no checksum listed for ${asset}`);
  }
  const got = crypto.createHash("sha256").update(buf).digest("hex");
  if (got !== want) {
    throw new Error(`checksum mismatch for ${asset}: expected ${want}, got ${got}`);
  }

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "tacho-"));
  fs.writeFileSync(path.join(tmp, asset), buf);

  // bsdtar/GNU tar both extract by extension. Run inside tmp with relative
  // names only: GNU tar (which can shadow bsdtar in PATH, e.g. Git for
  // Windows' usr/bin) parses the colon in absolute Windows paths ("C:\...")
  // as a remote host:path spec and fails with "Cannot connect to C:".
  execFileSync("tar", ["-xf", asset], { cwd: tmp, stdio: "inherit" });

  const extracted = path.join(tmp, binary);
  if (!fs.existsSync(extracted)) {
    throw new Error(`extracted archive but ${binary} not found in it`);
  }
  // Copy just the binary; the archive also carries LICENSE/README, which
  // don't belong in bin/.
  fs.mkdirSync(binDir, { recursive: true });
  const binPath = path.join(binDir, binary);
  fs.copyFileSync(extracted, binPath);
  fs.rmSync(tmp, { recursive: true, force: true });
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
