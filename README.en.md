# tachograph

[日本語](README.md) | English

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

## Install

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

## Usage

```sh
tacho                  # one-shot compact status, one line per agent
tacho watch -n 5       # refresh continuously
tacho status --json    # unified schema JSON (see docs/schema.md)
tacho statusline       # Claude Code statusLine adapter (reads stdin JSON)
tacho cmux push|clear  # manage cmux sidebar pills manually
```

```
claude Fable 5              ctx 32%  5h ███░░░░░ 37% ↻10:30  wk █████░░░ 68% ↻17:00
codex  gpt-5.5        ⚠6h   ctx 13%  5h █░░░░░░░  7% ↻06/13  wk ░░░░░░░░  2% ↻06/17
```

`⚠1h` marks data older than 15 minutes with its age, and the whole line is
dimmed — usage can only go down while an agent is idle, so a stale value
reads as an upper bound. Agents without rate-limit windows (e.g. Claude
Code on Bedrock) fall back to session tokens and estimated cost.

### Claude Code status line

Add to `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "tacho statusline",
    "padding": 0
  }
}
```

Claude Code pipes its session JSON (model, context, rate limits) to
`tacho statusline`, which prints one line combining it with Codex usage.
As a side effect each invocation snapshots the Claude rate limits, so bare
`tacho` / `tacho watch` in other terminals can show them too (for up to
10 minutes).

### Customizing the status line

Put a template in `~/.config/tachograph/statusline.tmpl` (or pass
`--template`). Default:

```
{claude.model} {claude.stale}ctx {claude.ctx} · 5h {claude.5h.bar:6} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.pct} · codex {codex.stale}5h {codex.5h.pct} wk {codex.wk.pct}
```

Or a dial-style variant:

```
{claude.model} ctx {claude.ctx} · 5h {claude.5h.dial} {claude.5h.pct} {claude.5h.resets} · wk {claude.wk.dial} · codex {codex.5h.dial}{codex.wk.dial}
```

Placeholders are `{tool.field}` with `tool` = `claude` | `codex`:

| field | renders |
|---|---|
| `model` | model display name (`Fable 5`, `gpt-5.5`) |
| `ctx` | context window usage, `8%` |
| `5h.pct` / `wk.pct` | rate-limit usage for the 5-hour / weekly window |
| `5h.bar:8` / `wk.bar:8` | usage gauge of the given width, `██░░░░░░` |
| `5h.dial` / `wk.dial` | single-character dial, `○◔◑◕●` (`◌` when no data) |
| `5h.moon` / `wk.moon` | larger moon-phase dial, `🌑🌒🌓🌔🌕` (emoji — not affected by colors) |
| `5h.resets` / `wk.resets` | reset time, `↻02:00` (today) or `↻06/15` |
| `tokens` | session tokens, `989k` / `12.5M` |
| `cost` | estimated session cost, `$0.05` |
| `plan` | plan name (`prolite`, …) |
| `cwd` | session working directory (basename) |
| `stale` | `⚠1h ` (marker + data age) when older than 15 minutes, else empty |
| `age` | age of the data, `42s` / `5m` / `1h` / `3d` |

Missing values render as `--`. Percentages and bars are colored by usage
(<50% green, ≥50% yellow, ≥80% red); disable with `--no-color` or `NO_COLOR`.

### cmux sidebar

Inside a [cmux](https://cmux.com) terminal, `tacho statusline` automatically
mirrors the status to the workspace sidebar as colored pills —
`claude ctx24% 5h24% wk41%` / `codex 5h4% wk11%`, colored green/yellow/red
by usage and gray when stale — with no extra setup beyond the status line.
It detects cmux via `CMUX_WORKSPACE_ID` and talks through the bundled cmux
CLI, fire-and-forget, so the status line latency is unaffected.

Manual control:

```sh
tacho cmux push    # push pills once (e.g. from cron or other hooks)
tacho cmux clear   # remove tacho's pills
```

### macOS menu bar (SwiftBar)

For an always-visible gauge regardless of which agent is running, a
[SwiftBar](https://github.com/swiftbar/SwiftBar) plugin is bundled. The menu
bar shows `C🌒 X🌑` (tool initial + 5-hour moon dial); clicking reveals
per-tool details, colored by usage and gray (with age) when stale.

```sh
brew install swiftbar   # if you don't have it
cp contrib/tacho.30s.sh <your SwiftBar plugin folder>/
chmod +x <plugin folder>/tacho.30s.sh
```

The `30s` in the filename is the refresh interval (rename to change). The
script just execs `tacho swiftbar`, so display changes belong in the tacho
renderer.

### Codex TUI

Codex's own status line is configured natively — run `/statusline` in the
TUI and pick e.g. `model + five-hour-limit + weekly-limit`. tachograph
does not (and cannot) draw inside the Codex TUI; it reads Codex session
logs non-invasively for display everywhere else.

## License

[MIT](LICENSE)
