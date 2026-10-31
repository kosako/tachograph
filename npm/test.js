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
// binary, writes nothing, or fails. install.js itself runs unchanged. A
// network failure is thrown the way Node's fetch throws it: a TypeError
// "fetch failed" (or "terminated" for a body cut off) whose cause is the
// socket's error, an AggregateError of one per address tried, or a proxy's
// connection error whose own cause is the TLS error.
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
const socketError = (message, code) => Object.assign(new Error(message), { code });
const causes = {
  refused: () => socketError("connect ECONNREFUSED 192.0.2.1:443", "ECONNREFUSED"),
  dns: () => socketError("getaddrinfo ENOTFOUND github.com", "ENOTFOUND"),
  tls: () => socketError("certificate has expired", "CERT_HAS_EXPIRED"),
  addresses: () => Object.assign(new AggregateError([
    socketError("connect ECONNREFUSED 192.0.2.1:443", "ECONNREFUSED"),
    socketError("connect ECONNREFUSED 2001:db8::1:443", "ECONNREFUSED"),
  ], ""), { code: "ECONNREFUSED" }),
  proxy: () => Object.assign(new Error("Secure Proxy Connection failed"), {
    code: "UND_ERR_PRX_TLS",
    cause: socketError("Hostname/IP does not match certificate's altnames", "ERR_TLS_CERT_ALTNAME_INVALID"),
  }),
  loop: () => {
    const e = socketError("connect ECONNRESET", "ECONNRESET");
    e.cause = e;
    return e;
  },
  none: () => undefined,
};
// AbortSignal.timeout is replaced so a timeout needn't take 120 seconds: it
// records the time asked for, and the signal aborts as a real one does once
// its stub timer fires. Only timedOut fires it, so a timeout scenario fails
// the install only through the signal install.js hands fetch (#444).
const timers = new Map();
AbortSignal.timeout = (ms) => {
  const c = new AbortController();
  const fire = () => c.abort(new DOMException("The operation was aborted due to timeout", "TimeoutError"));
  timers.set(c.signal, { ms, fire });
  return c.signal;
};
// timedOut waits on fetch's signal as fetch does, and fires its timer.
function timedOut(signal) {
  const timer = timers.get(signal);
  if (!timer) throw new Error("stub: fetch was given no timeout signal");
  if (timer.ms !== 120000) throw new Error("stub: a " + timer.ms + "ms timeout, want 120000");
  return new Promise((_, reject) => {
    signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    setImmediate(timer.fire);
  });
}
globalThis.fetch = async (url, options = {}) => {
  const which = url.endsWith("/checksums.txt") ? "sums" : "download";
  if (s.timeout === which) return timedOut(options.signal); // waiting for the headers
  if (s.bodyTimeout === which) {
    return { ok: true, status: 200, arrayBuffer: () => timedOut(options.signal) }; // reading the body
  }
  if (s.network === which) {
    const cause = causes[s.cause]();
    throw new TypeError("fetch failed", cause ? { cause } : undefined);
  }
  if (s.dropped === which) {
    return {
      ok: true,
      status: 200,
      arrayBuffer: async () => {
        throw new TypeError("terminated", { cause: socketError("other side closed", "UND_ERR_SOCKET") });
      },
    };
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
  ["a timeout reading the download", { sums: "listed", extract: "ok", bodyTimeout: "download" }, /^download timed out after 120s/m, false],
  ["a timeout reading the checksums", { sums: "listed", extract: "ok", bodyTimeout: "sums" }, /^checksums download timed out after 120s/m, false],
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

// A network-level failure says what was fetched, from where, and why — the
// cause Node keeps behind its bare "fetch failed" — so a proxy, DNS, or TLS
// problem on the user's side can be told apart (#382).
{
  const { version } = require("./package.json");
  const download = downloadURL(version, assetFor(process.platform, process.arch).asset);
  const sums = checksumsURL(version);
  for (const [name, scenario, want] of [
    ["a refused connection", { network: "download", cause: "refused" },
      `download failed: fetch failed (connect ECONNREFUSED 192.0.2.1:443) for ${download}`],
    ["an unresolved host", { network: "sums", cause: "dns" },
      `checksums download failed: fetch failed (getaddrinfo ENOTFOUND github.com) for ${sums}`],
    ["a TLS error", { network: "download", cause: "tls" },
      `download failed: fetch failed (CERT_HAS_EXPIRED: certificate has expired) for ${download}`],
    ["every address refused", { network: "download", cause: "addresses" },
      `download failed: fetch failed (connect ECONNREFUSED 192.0.2.1:443; connect ECONNREFUSED 2001:db8::1:443) for ${download}`],
    ["a proxy's TLS error", { network: "download", cause: "proxy" },
      `download failed: fetch failed (UND_ERR_PRX_TLS: Secure Proxy Connection failed; caused by ERR_TLS_CERT_ALTNAME_INVALID: Hostname/IP does not match certificate's altnames) for ${download}`],
    ["a cause that refers to itself", { network: "download", cause: "loop" },
      `download failed: fetch failed (connect ECONNRESET) for ${download}`],
    ["a failure without a cause", { network: "download", cause: "none" },
      `download failed: fetch failed for ${download}`],
    ["a download cut off", { dropped: "download" },
      `download failed: terminated (UND_ERR_SOCKET: other side closed) for ${download}`],
  ]) {
    test(`install names the cause of ${name}`, () => {
      const r = runInstall({ sums: "listed", extract: "ok", ...scenario });
      assert.strictEqual(r.status, 1, r.stdout);
      assert.ok(r.stderr.split("\n").includes(want), `stderr:\n${r.stderr}\nwant the line:\n${want}`);
      assert.match(r.stderr, /go install github\.com\/kosako\/tachograph\/cmd\/tacho@latest/);
      assert.strictEqual(r.extracted, false);
      assert.strictEqual(r.installed, null);
      assert.deepStrictEqual(r.leftovers, []);
    });
  }
}

// runLauncher runs a copy of bin/tacho.js the way npm's bin shim does, next to
// a stand-in binary: a shell script running script, written with mode (null
// leaves the binary out). It reports what the launcher exited with (#444).
function runLauncher(script, { mode = 0o755, args = [] } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tacho-launcher-test-"));
  try {
    const bin = path.join(root, "bin");
    fs.mkdirSync(bin);
    fs.copyFileSync(path.join(__dirname, "bin", "tacho.js"), path.join(bin, "tacho.js"));
    if (script !== null) {
      const binPath = path.join(bin, assetFor(process.platform, process.arch).binary);
      fs.writeFileSync(binPath, "#!/bin/sh\n" + script + "\n");
      fs.chmodSync(binPath, mode);
    }
    return spawnSync(process.execPath, [path.join(bin, "tacho.js"), ...args], { encoding: "utf8" });
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// The stand-in binaries are shell scripts, which Windows can't run.
const noShell = process.platform === "win32" && "the stand-in binary is a shell script";

test("launcher forwards the arguments, the output, and a non-zero exit status", { skip: noShell }, () => {
  const r = runLauncher('echo "args: $*"; echo "to stderr" >&2; exit 3', { args: ["a", "b c"] });
  assert.strictEqual(r.status, 3, r.stderr);
  assert.strictEqual(r.stdout, "args: a b c\n");
  assert.strictEqual(r.stderr, "to stderr\n");
});

test("launcher exits 0 when the binary does", { skip: noShell }, () => {
  const r = runLauncher("exit 0");
  assert.strictEqual(r.status, 0, r.stderr);
});

test("launcher exits 1 with the reinstall hint when the binary is missing", () => {
  const r = runLauncher(null);
  assert.strictEqual(r.status, 1);
  assert.match(r.stderr, /tachograph: binary not found/);
  assert.match(r.stderr, /go install github\.com\/kosako\/tachograph\/cmd\/tacho@latest/);
});

test("launcher exits 1 naming the error when the binary can't be run", { skip: noShell }, () => {
  const r = runLauncher("exit 0", { mode: 0o644 });
  assert.strictEqual(r.status, 1);
  assert.match(r.stderr, /^tachograph: .*EACCES/m);
});

// A binary killed by a signal never reads as success. What the launcher then
// exits with is #442's to decide.
test("launcher doesn't exit 0 when the binary is killed by a signal", { skip: noShell }, () => {
  const r = runLauncher("kill -TERM $$");
  assert.notStrictEqual(r.status, 0);
});
