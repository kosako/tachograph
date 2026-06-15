# tachograph

A compact instrument cluster for your coding agents (**Claude Code** / **Codex**) —
model, rate-limit usage (5-hour / weekly), and context window, at a glance.

```sh
npm install -g tachograph
tacho                 # one-shot compact status
tacho setup claude    # wire up the Claude Code statusLine
```

`npm install` downloads the prebuilt `tacho` binary for your platform from the
matching [GitHub release](https://github.com/kosako/tachograph/releases). If no
prebuilt binary fits your platform, install from source instead:

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

Full documentation: <https://github.com/kosako/tachograph>.

## License

[MIT](https://github.com/kosako/tachograph/blob/main/LICENSE)
