#!/usr/bin/env node
// Launcher: exec the platform binary that postinstall placed next to this file,
// forwarding args, stdio, and exit code.

const path = require("path");
const fs = require("fs");
const { spawnSync } = require("child_process");

const binary = process.platform === "win32" ? "tacho.exe" : "tacho";
const binPath = path.join(__dirname, binary);

if (!fs.existsSync(binPath)) {
  console.error(
    "tachograph: binary not found — the postinstall download may have failed.\n" +
      "Reinstall (npm i -g tachograph), or install from source:\n" +
      "  go install github.com/kosako/tachograph/cmd/tacho@latest",
  );
  process.exit(1);
}

const res = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error(`tachograph: ${res.error.message}`);
  process.exit(1);
}
process.exit(res.status === null ? 1 : res.status);
