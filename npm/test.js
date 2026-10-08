// Tests for the npm wrapper: the platform→asset mapping, and the postinstall
// (install.js) run as npm runs it with the network and tar stubbed. Run:
// node test.js
const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const { test } = require("node:test");
const { assetFor, downloadURL, checksumsURL, checksumFor } = require("./asset");

test("assetFor maps known platforms", () => {
  // Full platform × arch matrix: macOS, Linux, and Windows on amd64 + arm64.
  assert.deepStrictEqual(assetFor("darwin", "arm64"), {
    asset: "tachograph_darwin_arm64.tar.gz",
    binary: "tacho",
  });
  assert.deepStrictEqual(assetFor("darwin", "x64"), {
    asset: "tachograph_darwin_amd64.tar.gz",
    binary: "tacho",
  });
  assert.deepStrictEqual(assetFor("linux", "x64"), {
    asset: "tachograph_linux_amd64.tar.gz",
    binary: "tacho",
  });
  assert.deepStrictEqual(assetFor("linux", "arm64"), {
    asset: "tachograph_linux_arm64.tar.gz",
    binary: "tacho",
  });
  assert.deepStrictEqual(assetFor("win32", "x64"), {
    asset: "tachograph_windows_amd64.zip",
    binary: "tacho.exe",
  });
  assert.deepStrictEqual(assetFor("win32", "arm64"), {
    asset: "tachograph_windows_arm64.zip",
    binary: "tacho.exe",
  });
});

test("assetFor rejects unsupported combos", () => {
  assert.throws(() => assetFor("freebsd", "arm64"), /no prebuilt binary/);
  assert.throws(() => assetFor("linux", "ia32"), /no prebuilt binary/);
});

test("downloadURL normalizes the tag", () => {
  const want =
    "https://github.com/kosako/tachograph/releases/download/v0.1.1/tachograph_linux_amd64.tar.gz";
  assert.strictEqual(downloadURL("0.1.1", "tachograph_linux_amd64.tar.gz"), want);
  assert.strictEqual(downloadURL("v0.1.1", "tachograph_linux_amd64.tar.gz"), want);
});

test("checksumsURL points at the release checksums.txt", () => {
  const want =
    "https://github.com/kosako/tachograph/releases/download/v0.1.1/checksums.txt";
  assert.strictEqual(checksumsURL("0.1.1"), want);
  assert.strictEqual(checksumsURL("v0.1.1"), want);
});

test("checksumFor parses GoReleaser checksums.txt", () => {
  const text =
    "aaaa1111  tachograph_darwin_arm64.tar.gz\n" +
    "bbbb2222  tachograph_linux_amd64.tar.gz\n";
  assert.strictEqual(checksumFor(text, "tachograph_linux_amd64.tar.gz"), "bbbb2222");
  assert.strictEqual(checksumFor(text, "tachograph_windows_amd64.zip"), null);
});

// INSTALL_STUB is preloaded (node --require) into the postinstall run: it
// replaces fetch with the scenario's responses for this platform's asset and
// checksums.txt, and tar (child_process.execFileSync, which install.js takes
// once it loads) with a stub that records the call and then writes the
// binary, writes nothing, or fails. install.js itself runs unchanged.
const INSTALL_STUB = `
const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const cp = require("child_process");
const s = JSON.parse(process.env.TACHO_INSTALL_STUB);
const { asset, binary } = require(s.assetModule).assetFor(process.platform, process.arch);
const archive = Buffer.from("archive bytes");
const sha = crypto.createHash("sha256").update(archive).digest("hex");
const sums = {
  listed: sha + "  " + asset + "\\n",
  mismatch: "0".repeat(64) + "  " + asset + "\\n",
  missing: sha + "  tachograph_other_asset.tar.gz\\n",
}[s.sums];
globalThis.fetch = async (url) => {
  const which = url.endsWith("/checksums.txt") ? "sums" : "download";
  if (s.timeout === which) {
    const err = new Error("The operation was aborted due to timeout");
    err.name = "TimeoutError";
    throw err;
  }
  const status = (which === "sums" ? s.sumsStatus : s.downloadStatus) || 200;
  if (status !== 200) return new Response("", { status, statusText: "Stub Error" });
  return new Response(which === "sums" ? sums : archive);
};
cp.execFileSync = (cmd, args, opts) => {
  fs.writeFileSync(s.extractMarker, "");
  if (s.extract === "fail") throw new Error("stub tar failed");
  if (s.extract === "ok") fs.writeFileSync(path.join(opts.cwd, binary), "binary");
};
`;

// runInstall runs a copy of the package's install.js the way npm's
// postinstall does (node install.js) with INSTALL_STUB preloaded and the
// temp dir (os.tmpdir) pointed at a fresh directory, and reports what it
// left behind.
function runInstall(scenario) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tacho-install-test-"));
  try {
    const pkg = path.join(root, "pkg");
    fs.mkdirSync(pkg);
    for (const f of ["install.js", "asset.js", "package.json"]) {
      fs.copyFileSync(path.join(__dirname, f), path.join(pkg, f));
    }
    const tmp = path.join(root, "tmp");
    fs.mkdirSync(tmp);
    const stub = path.join(root, "stub.js");
    fs.writeFileSync(stub, INSTALL_STUB);
    const marker = path.join(root, "extract-called");
    const res = spawnSync(process.execPath, ["--require", stub, path.join(pkg, "install.js")], {
      encoding: "utf8",
      env: {
        ...process.env,
        TMPDIR: tmp,
        TMP: tmp,
        TEMP: tmp,
        TACHO_INSTALL_STUB: JSON.stringify({
          ...scenario,
          assetModule: path.join(pkg, "asset.js"),
          extractMarker: marker,
        }),
      },
    });
    const binPath = path.join(pkg, "bin", assetFor(process.platform, process.arch).binary);
    const installed = fs.existsSync(binPath);
    return {
      status: res.status,
      stdout: res.stdout,
      stderr: res.stderr,
      installed: installed ? fs.readFileSync(binPath, "utf8") : null,
      mode: installed ? fs.statSync(binPath).mode & 0o777 : null,
      extracted: fs.existsSync(marker),
      leftovers: fs.readdirSync(tmp),
    };
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

test("install places the binary when the checksum matches", () => {
  const r = runInstall({ sums: "listed", extract: "ok" });
  assert.strictEqual(r.status, 0, r.stderr);
  assert.match(r.stdout, /tachograph: installed tacho(\.exe)? /);
  assert.strictEqual(r.installed, "binary");
  if (process.platform !== "win32") {
    assert.strictEqual(r.mode, 0o755);
  }
  assert.deepStrictEqual(r.leftovers, []);
});

// Every failure exits 1 with the go install hint and leaves neither a binary
// nor a temp dir; a checksum that is missing or doesn't match stops before
// the archive is extracted at all (#352).
for (const [name, scenario, want, extracted] of [
  ["a checksum mismatch", { sums: "mismatch", extract: "ok" }, /checksum mismatch for tachograph_/, false],
  ["a missing checksum", { sums: "missing", extract: "ok" }, /no checksum listed for tachograph_/, false],
  ["a failed download", { sums: "listed", extract: "ok", downloadStatus: 404 }, /^download failed: 404 Stub Error/m, false],
  ["a failed checksums download", { sums: "listed", extract: "ok", sumsStatus: 500 }, /^checksums download failed: 500 Stub Error/m, false],
  ["a download timeout", { sums: "listed", extract: "ok", timeout: "download" }, /^download timed out after 120s/m, false],
  ["a checksums timeout", { sums: "listed", extract: "ok", timeout: "sums" }, /^checksums download timed out after 120s/m, false],
  ["a failed extraction", { sums: "listed", extract: "fail" }, /stub tar failed/, true],
  ["an archive without the binary", { sums: "listed", extract: "empty" }, /extracted archive but tacho(\.exe)? not found in it/, true],
]) {
  test(`install fails on ${name}`, () => {
    const r = runInstall(scenario);
    assert.strictEqual(r.status, 1, r.stdout);
    assert.match(r.stderr, want);
    assert.match(r.stderr, /go install github\.com\/kosako\/tachograph\/cmd\/tacho@latest/);
    assert.strictEqual(r.extracted, extracted);
    assert.strictEqual(r.installed, null);
    assert.deepStrictEqual(r.leftovers, []);
  });
}
