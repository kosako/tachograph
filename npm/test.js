// Pure unit tests for the platform→asset mapping. Run: node test.js
const assert = require("node:assert");
const { test } = require("node:test");
const { assetFor, downloadURL, checksumsURL, checksumFor } = require("./asset");

test("assetFor maps known platforms", () => {
  assert.deepStrictEqual(assetFor("darwin", "arm64"), {
    asset: "tachograph_darwin_arm64.tar.gz",
    binary: "tacho",
  });
  assert.deepStrictEqual(assetFor("linux", "x64"), {
    asset: "tachograph_linux_amd64.tar.gz",
    binary: "tacho",
  });
  assert.deepStrictEqual(assetFor("win32", "x64"), {
    asset: "tachograph_windows_amd64.zip",
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
