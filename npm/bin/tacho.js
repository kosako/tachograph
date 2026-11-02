#!/usr/bin/env node
// Launcher: exec the platform binary that postinstall placed next to this file,
// forwarding args, stdio, and exit code (or the signal that killed it).

const path = require("path");
const fs = require("fs");
const os = require("os");
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
if (res.signal) {
  exitLikeSignal(res.signal);
} else {
  process.exit(res.status === null ? 1 : res.status);
}

// exitLikeSignal ends the launcher the way the binary ended, killed by
// signal, so a caller sees what running the binary directly shows rather than
// the launcher's own exit 1 (#442). Node ignores some signals (SIGPIPE,
// SIGXFSZ) and SIGUSR1 would start its inspector instead: those, Windows,
// and a signal that doesn't end the launcher exit with the shell's
// 128 + signal number.
function exitLikeSignal(signal) {
  const number = os.constants.signals[signal];
  const code = number ? 128 + number : 1;
  if (process.platform !== "win32" && signal !== "SIGUSR1") {
    try {
      process.kill(process.pid, signal);
    } catch {
      process.exit(code);
    }
    setTimeout(() => process.exit(code), 100);
    return;
  }
  process.exit(code);
}
