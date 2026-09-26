# tachograph

A compact instrument cluster for your coding agents (**Claude Code** / **Codex**) —
model, rate-limit headroom (what's left of the 5-hour / weekly windows), and
context window, at a glance.

```sh
npm install -g tachograph
tacho                 # one-shot compact status
tacho setup claude    # print the Claude Code statusLine config (--write installs it)
```

`npm install` downloads the prebuilt `tacho` binary for your platform from the
matching [GitHub release](https://github.com/kosako/tachograph/releases)
(macOS / Linux / Windows, x64 or arm64) and verifies it against the release's
`checksums.txt`. The download runs in the package's postinstall script
(Node.js 18+), so installing with `--ignore-scripts` leaves no binary. If no
prebuilt binary fits your platform, install from source instead:

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

Full documentation:
[English](https://github.com/kosako/tachograph/blob/main/README.en.md) /
[日本語](https://github.com/kosako/tachograph#readme).

## License

[MIT](https://github.com/kosako/tachograph/blob/main/LICENSE)
