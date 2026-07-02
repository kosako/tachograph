# tachograph

[日本語](README.md) | English

[![npm](https://img.shields.io/npm/v/tachograph)](https://www.npmjs.com/package/tachograph)
[![release](https://img.shields.io/github/v/release/kosako/tachograph)](https://github.com/kosako/tachograph/releases)
[![license](https://img.shields.io/github/license/kosako/tachograph)](LICENSE)

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

The easiest way is **npm** (no Go required; it downloads the prebuilt binary
for your platform and puts `tacho` on your PATH automatically):

```sh
npm install -g tachograph
```

It fetches the matching binary from the GitHub release in its postinstall step.
If no prebuilt binary fits your platform, or you prefer Go, use `go install`:

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

### Put it on your PATH (go install only)

> Not needed when installed via npm — `tacho` is already on your PATH.

`go install` drops the binary in `$(go env GOPATH)/bin` (`~/go/bin` by
default). If that directory isn't on your PATH you can't run `tacho` (and
Claude Code's statusLine can't launch it — it fails silently).

```sh
command -v tacho            # prints a path if it's on PATH; nothing means it isn't
go env GOPATH               # where it landed (install dir is /bin under this)
```

If it isn't on your PATH, add it in your shell config:

```sh
# zsh (macOS default)
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
# for bash, append the same line to ~/.bashrc / ~/.bash_profile
```

If you'd rather not touch your PATH, call `tacho` by absolute path (e.g.
`~/go/bin/tacho`) — including in the Claude Code settings below. `tacho
setup claude` prints a snippet with the correct absolute path baked in.

### Updating

If you installed via **npm**, reinstall to pull the latest version. It
overwrites the same location, so your statusLine / SwiftBar config keeps
working unchanged:

```sh
npm install -g tachograph@latest
tacho version    # check the current version (also shown at the top of tacho doctor)
```

If you installed via `go install`, re-run the same command (it overwrites the
same path under `$GOPATH/bin`):

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

To pin a specific version, use `tachograph@0.2.2` with npm or a tag like
`@v0.2.2` with go. `tacho version` reports the version embedded at install time
(the tag) — an untagged local build shows a commit-based pseudo-version.

## Usage

```sh
tacho                  # one-shot compact status, one line per agent
tacho watch -n 5       # refresh continuously
tacho status --json    # unified schema JSON (see docs/schema.md)
tacho statusline       # Claude Code statusLine adapter (reads stdin JSON)
tacho cmux push|clear  # manage cmux sidebar pills manually
tacho setup claude     # print/install the Claude Code statusLine config (--write)
tacho doctor           # diagnose install path, PATH, and config
```

```
claude Fable 5              ctx 32%  5h ███░░░░░ 37% ↻10:30  wk █████░░░ 68% ↻17:00
codex  gpt-5.5        ⚠6h   ctx 13%  5h █░░░░░░░  7% ↻06/13  wk ░░░░░░░░  2% ↻06/17
```

`⚠6h` marks stale data with its age, and the whole line is dimmed — usage can
only go down while an agent is idle, so a stale value reads as an upper bound.
The threshold is per tool: Claude is 60 minutes (about an hour); Codex is 5
hours, since it has no live feed and its limit windows stay valid for hours.
Agents without rate-limit windows (e.g. Claude Code on Bedrock) fall back to
session tokens and estimated cost.

### Claude Code status line

The easiest way is to let tacho do it:

```sh
tacho setup claude --write   # merge into ~/.claude/settings.json (keeps existing keys, writes a .bak)
tacho setup claude           # just print the snippet to paste (no file edits)
```

It uses a bare `tacho statusline` when tacho is on your PATH, or bakes in the
resolved absolute path when it isn't. To edit by hand, add to
`~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "tacho statusline",
    "padding": 0
  }
}
```

If something isn't working, `tacho doctor` reports the real binary path, PATH
status, config files, and whether the statusLine is configured.

Claude Code pipes its session JSON (model, context, rate limits) to
`tacho statusline`, which prints one line combining it with Codex usage.
As a side effect each invocation snapshots the Claude rate limits, so bare
`tacho` / `tacho watch` in other terminals can show them too (kept as a
last-known value for up to 30 days, shown as stale after 60 minutes).

### Customizing the status line

Put a one-line template in `~/.config/tachograph/statusline.tmpl` (or pass
`--template`).

#### Presets (start here)

Pick a **preset** that matches what you want, no placeholder assembly required:

```sh
tacho config statusline-preset --list   # list presets (name + template)
tacho config statusline-preset moon      # pick one; writes statusline.tmpl
```

| Preset | For | Example |
|---|---|---|
| `bar` | default — context + 5h gauge + weekly | `Fable 5 ctx 8% · 5h █░░░░░ 24% ↻06/12 · wk 41% · …` |
| `minimal` | model + 5h/weekly percentages only | `Fable 5 5h 24% · wk 41%` |
| `dial` | compact single-char dials (`○◔◑◕●`) | `Fable 5 ctx 8% · 5h ◔ 24% ↻06/12 · wk ◑ · codex ◔◑` |
| `moon` | moon-phase dials (`🌑🌒🌓🌔🌕`) | `Fable 5 5h 🌒 24% · wk 🌓 · codex 🌒🌓` |
| `cost` | model + context + 5h + this session's tokens/cost | `Fable 5 ctx 8% · 5h 24% · 989k $0.05` |
| `cwd` | working dir + model + context + 5h gauge | `myproj · Fable 5 ctx 8% · 5h █░░░░░ 24%` |

To hand-roll one, copy [`contrib/statusline.tmpl.example`](contrib/statusline.tmpl.example)
to `~/.config/tachograph/statusline.tmpl` and uncomment the line you want
(lines starting with `#` and blank lines are ignored; the first usable line
becomes the template).

#### Placeholders

Placeholders are `{tool.field}` with `tool` = `claude` | `codex`:

| field | renders |
|---|---|
| `model` | model display name (`Fable 5`, `gpt-5.5`) |
| `effort` | reasoning effort, `⚡xhi ` (`low`/`med`/`high`/`xhi`/`max`, marker + trailing space; Claude only, empty when the model doesn't support it) |
| `ctx` | context window usage, `8%` |
| `5h.pct` / `wk.pct` | rate-limit usage for the 5-hour / weekly window |
| `5h.bar:8` / `wk.bar:8` | usage gauge of the given width, `██░░░░░░` |
| `5h.dial` / `wk.dial` | single-character dial, `○◔◑◕●` (`◌` when no data) |
| `5h.moon` / `wk.moon` | larger moon-phase dial, `🌑🌒🌓🌔🌕` (emoji — not affected by colors; `◌` when no data) |
| `5h.resets` / `wk.resets` | reset time, `↻02:00` (today) or `↻06/15` |
| `tokens` / `tokens.session` | **current session** tokens, `989k` |
| `tokens.session.today` | **current session, today only** tokens (Claude only), `68k` |
| `tokens.all` | **today's all-session total** tokens, `12.7M/d` (`/d`=daily total) |
| `cost` / `cost.session` | **current session** estimated cost, `$0.05` (Claude's own figure in the status line) |
| `cost.session.today` | **current session, today only** estimated cost (Claude only), `$1.84` |
| `cost.all` | **today's all-session** estimated cost (price-table based, approximate), `$1.20/d` |
| `plan` | plan name (`prolite`, …) |
| `cwd` | session working directory (basename) |
| `stale` | `⚠1h ` (marker + data age) when older than 60 minutes, else empty (Codex has no live feed and its limit windows stay valid for hours, so it goes stale after 5 hours) |
| `age` | age of the data, `42s` / `5m` / `1h` / `3d` |

`*.session.today` is Claude only — Codex's token counts are cumulative and
can't be sliced to a single day, so it renders `--`.

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
bar shows a tachometer per tool — the logo ringed by an iOS-app-download-style
progress ring that fills clockwise with 5-hour usage; clicking reveals
per-tool details. The ring is colored by usage (green/yellow/red, gray when
stale). The logo and track are white by default (for Dark mode or a
wallpaper-darkened menu bar); set `TACHO_APPEARANCE=light` if your menu bar
is light. Set `TACHO_SWIFTBAR_TEXT=1` to fall back to the moon-dial text
(`C🌒 X🌑`).

```sh
brew install swiftbar   # if you don't have it
cp contrib/tacho.30s.sh <your SwiftBar plugin folder>/
chmod +x <plugin folder>/tacho.30s.sh
```

The `30s` in the filename is the refresh interval (rename to change). The
script just execs `tacho swiftbar`, so display changes belong in the tacho
renderer.

#### Configuring what's shown

The dropdown lists every metric per tool (5h / weekly / context / cost /
tokens); the menu bar shows the one you select.

The **Settings** submenu at the bottom picks values from a list (the current
choice is check-marked):

- **Display**: meter (gauge) or number
- **Metric**: 5h limit / weekly limit / cost / tokens (radio; context is excluded — it churns per session and isn't a useful at-a-glance menu-bar figure)
- **Tools**: Claude / Codex (checkboxes)

Or via the CLI (config lives in `~/.config/tachograph/config.json`):

```sh
tacho config show
tacho config set menubar.style number   # meter → number
tacho config set menubar.metric cost    # show spent cost instead of limits
tacho config set tools codex            # show only Codex
```

#### Cost price table (approximate, overridable)

`cost` and `tokens` are **today's totals across all sessions**. For Claude Code,
this includes regular sessions plus subagents / workflows transcripts under
those sessions. Cost is estimated from a per-model price table (tokens × rate).
Prices are rough, not exact, so override or extend them in
`~/.config/tachograph/pricing.json` (USD per million tokens):

```json
{
  "claude-fable": { "input": 10, "output": 50, "cache_read": 1, "cache_write": 12.5 },
  "gpt-5":        { "input": 1.25, "output": 10, "cache_read": 0.125, "cache_write": 1.25 }
}
```

Only the fields you set override the built-in defaults (e.g. set just `input`
and the other rates stay at their defaults — partial overrides merge). A **new
model id** not in the table has no defaults, so any rate you leave out is `0`.
Keys match model ids by prefix (`claude-fable` matches `claude-fable-5`). When a
Claude transcript records 1-hour cache writes, they are priced at 2x the input
rate. Models not in the price table are excluded from the cost calculation and
don't count toward the total (if no priced model ran that day, cost shows as
unknown, `--`).

### Codex TUI

Codex's own status line is configured natively — run `/statusline` in the
TUI and pick e.g. `model + five-hour-limit + weekly-limit`. tachograph
does not (and cannot) draw inside the Codex TUI; it reads Codex session
logs non-invasively for display everywhere else.

## Which variants are covered?

tachograph reads local logs (`~/.claude/projects`, `~/.codex/sessions`).
Anything that writes there — terminal, desktop, or IDE — is counted.

| | Tokens / cost (today) | Rate limits / context |
|---|---|---|
| **Codex** (TUI / Desktop) | ✅ | ✅ |
| **Claude Code** (CLI / IDE) | ✅ | ✅ |
| **Claude Desktop** | ✅ | ⚠️ needs the terminal too |

- **Codex Desktop** writes to `~/.codex/sessions`, so everything (including
  rate limits) is reflected.
- **Claude Desktop** usage is recorded in the transcripts, so its tokens/cost
  are included in the daily totals. But the 5-hour/weekly limits and context %
  come **only from the terminal statusLine** (transcripts carry no limit data).
  Rate limits are account-wide, so as long as you use terminal Claude Code now
  and then, the displayed headroom reflects desktop usage too. With no terminal
  use at all, limits show `--` (consumed but invisible to tacho).

## License

[MIT](LICENSE)
