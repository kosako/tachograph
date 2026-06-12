# tachograph

> A compact instrument cluster for your coding agents.

`tacho` shows, at a glance, what your AI coding agents are doing and how much
headroom you have left:

- current model per session
- rate-limit usage (5-hour / weekly windows) and reset times
- context-window usage

Supported agents: **Claude Code** and **Codex CLI**.

## Why "tachograph"?

A tachograph is the legally mandated instrument in trucks that records driving
time, mandatory rest periods, and when the driver may resume. That is exactly
what this tool does for coding agents: it tracks how much of your rate-limit
window you have burned, when it resets, and what is currently running.

## Design principles

1. **An instrument, not an observability platform.** No log accumulation, no
   cost analytics, no dashboards. Optimized for the quick glance.
2. **Collectors and renderers are separate.** The core emits a single unified
   JSON schema (`tacho status --json`); display targets are pluggable.
3. **No resident daemon.** On-demand collection with a short-lived file cache.
4. **Thin by design.** Reads the data your agents already write to disk.

## Status

🚧 Under construction — MVP in progress. See the
[issues](https://github.com/kosako/tachograph/issues) for the roadmap.

## Install

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

## License

[MIT](LICENSE)
