# tachograph

[日本語](README.md) | English

[![npm](https://img.shields.io/npm/v/tachograph)](https://www.npmjs.com/package/tachograph)
[![release](https://img.shields.io/github/v/release/kosako/tachograph)](https://github.com/kosako/tachograph/releases)
[![license](https://img.shields.io/github/license/kosako/tachograph)](LICENSE)

> A compact instrument cluster for your coding agents.

`tacho` shows, at a glance, what your AI coding agents are doing and how much
headroom you have left:

- current model per session
- rate-limit headroom (what's left of the 5-hour / weekly windows) and reset times
- context-window usage
- estimated cost / tokens (current session and today's total; `tacho daily`
  lists them per day)

Supported agents: **Claude Code** and **Codex CLI**.

## Why "tachograph"?

A tachograph is the legally mandated instrument in trucks that records driving
time, mandatory rest periods, and when the driver may resume. That is exactly
what this tool does for coding agents: it tracks how much of your rate-limit
window you have burned, when it resets, and what is currently running.

## Design principles

1. **An instrument, not an observability platform.** No log accumulation, no
   dashboards. Optimized for the quick glance (the exceptions are today's cost /
   token totals and two per-day listings recomputed from the existing logs:
   `tacho daily` and SwiftBar's last-7-days rows. tacho itself stores no
   history; the `daily-history.json` behind the SwiftBar rows is a derived
   cache that can be rebuilt from the logs).
2. **Collectors and renderers are separate.** The core emits a single unified
   JSON schema (`tacho status --json`); display targets are pluggable.
3. **No resident daemon.** On-demand collection with a file cache.
4. **Thin by design.** Reads the data your agents already write to disk.

## Install

The easiest way is **npm** (no Go required; it downloads the prebuilt binary
for your platform and puts `tacho` on your PATH automatically):

```sh
npm install -g tachograph
```

It fetches the matching binary (macOS / Linux / Windows on x64 or arm64) from
the GitHub release in its postinstall step.
If no prebuilt binary fits your platform, or you prefer Go, use `go install`:

```sh
go install github.com/kosako/tachograph/cmd/tacho@latest
```

If you use neither npm nor Go, download the archive for your platform
(`tachograph_<os>_<arch>.tar.gz`, `.zip` on Windows) from
[GitHub Releases](https://github.com/kosako/tachograph/releases) and put the
`tacho` binary somewhere on your PATH.

### Put it on your PATH (go install only)

> Not needed when installed via npm — `tacho` is already on your PATH.

`go install` drops the binary in `$(go env GOPATH)/bin` (`~/go/bin` by
default), or in `GOBIN` when that is set (version managers such as mise may
set it; if `go env GOBIN` prints a path, read `$(go env GOPATH)/bin` below as
that directory). If that directory isn't on your PATH you can't run `tacho`
(and Claude Code's statusLine can't launch it — it fails silently).

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

To pin a specific version, use `tachograph@0.6.2` with npm or a tag like
`@v0.6.2` with go. `tacho version` reports the version embedded at install time
(the tag) — an untagged local build shows a commit-based pseudo-version.

## Usage

```sh
tacho                  # one-shot compact status, one line per agent (cached for 30 s; --no-cache to bypass)
tacho watch -n 5       # refresh continuously
tacho status --json    # unified schema JSON (see docs/schema.md)
tacho daily -days 30   # per-day estimated cost / tokens (default 30 days, recomputed from the logs)
tacho statusline       # Claude Code statusLine adapter (reads stdin JSON)
tacho cmux push|clear  # manage cmux sidebar pills manually
tacho setup claude     # print/install the Claude Code statusLine config (--write)
tacho doctor           # diagnose install path, data sources, cache, integrations (and unknown config values)
tacho version          # print the installed version (also tacho --version)
tacho config show|set  # show / change settings (~/.config/tachograph/config.json; show also warns about unknown values; statusline-preset and more below)
```

```
claude Fable 5              ctx 32%  5h ███░░░░░ 37% ↻10:30  wk █████░░░ 68% ↻17:00
codex  gpt-5.5        ⚠6h   ctx 13%  5h █░░░░░░░  7% ↻06/13  wk ░░░░░░░░  2% ↻06/17
```

`⚠6h` marks stale data with its age, and the whole line is dimmed — a stale
value is the headroom as last observed; it does not reflect a reset since then
(which raises it) or consumption elsewhere (which lowers it).
The threshold is per tool: Claude is 60 minutes (about an hour); Codex is 5
hours, since it has no live feed and its limit windows stay valid for hours.
When no rate-limit windows are known, the line falls back to session tokens
and, when known, estimated cost — on a backend without them (e.g. Claude Code
on Bedrock), and also for Claude on a subscription until its limits arrive.
Claude's rate limits and context % only come through the statusLine, so while
the statusLine hasn't run in Claude Code in the last 30 days (e.g. before you
set it up), the Claude line shows just the session tokens counted from the
transcript, with `ctx --%`. Set it up with `tacho setup claude` (below).

### Claude Code status line

The easiest way is to let tacho do it:

```sh
tacho setup claude --write   # merge into ~/.claude/settings.json (keeps other keys, replaces statusLine; writes a .bak unless one exists)
tacho setup claude           # just print the snippet to paste (no file edits)
```

It uses a bare `tacho statusline` when the `tacho` on your PATH is this very
binary, or bakes in the resolved absolute path otherwise (not on PATH, or a
different install shadows it). To edit by hand, add to
`~/.claude/settings.json` (or `settings.json` under `CLAUDE_CONFIG_DIR` if you
set it — `--write` targets the same file):

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
status, config files, data-source freshness, cache state, cmux/SwiftBar
integration, each tool's current collection state (ok / stale / an error with
a hint), and whether the statusLine is configured.

Claude Code pipes its session JSON (model, context, rate limits) to
`tacho statusline`, which prints one line combining it with Codex usage.
As a side effect each invocation snapshots the Claude rate limits, so bare
`tacho` / `tacho watch` in other terminals can show them too (kept as a
last-known value for up to 30 days, shown as stale after 60 minutes; ctx and
the session's tokens / cost describe the most recently observed session, so
they turn into `--` once stale).

### Customizing the status line

Put a one-line template in `~/.config/tachograph/statusline.tmpl` (or pass the
template string itself, not a file path, with
`tacho statusline --template '…'`).

#### Presets (start here)

Pick a **preset** that matches what you want, no placeholder assembly required:

```sh
tacho config statusline-preset --list   # list presets (name, description, template)
tacho config statusline-preset moon      # pick one; writes statusline.tmpl (replacing any existing one)
```

| Preset | For | Example |
|---|---|---|
| `bar` | default — context + 5h gauge + weekly, plus Codex's 5h/weekly | `Fable 5 ctx 8% · 5h █░░░░░ 24% ↻06/12 · wk 41% · …` |
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
| `5h.pct` / `wk.pct` | rate-limit **headroom** (percent left) for the 5-hour / weekly window, `76%`; usage with `limits.display: used` |
| `5h.bar:8` / `wk.bar:8` | headroom gauge of the given width (8 when `:width` is omitted; drains as you use it; fills with `used`; with no data it still renders an empty gauge of the same width, `░░░░░░░░`, so check `pct` for `--`), `██████░░` |
| `5h.dial` / `wk.dial` | single-character headroom dial, `○◔◑◕●` (● = all left, or used up with `used`; `◌` when no data) |
| `5h.moon` / `wk.moon` | larger moon-phase headroom dial, `🌑🌒🌓🌔🌕` (🌕 = all left, or used up with `used`; emoji, so not colored; `◌` when no data) |
| `5h.resets` / `wk.resets` | reset time, `↻02:00` (within the next 24 hours) or `↻06/15` (otherwise) |
| `tokens` / `tokens.session` | **current session** tokens, `989k` |
| `tokens.session.today` | **current session, today only** tokens (Claude only), `68k` |
| `tokens.all` | **today's all-session total** tokens, `12.7M/d` (`/d`=daily total) |
| `cost` / `cost.session` | **current session** estimated cost, `$0.05` (Claude: the estimate Claude Code passes to the status line; Codex: tacho's price-table estimate, pricing the session's cumulative tokens at the current model's rate) |
| `cost.session.today` | **current session, today only** estimated cost (Claude only), `$1.84` |
| `cost.all` | **today's all-session** estimated cost (price-table based, approximate), `$1.20/d` |
| `plan` | plan name (`prolite`, …; from Codex's `rate_limits.plan_type`, so always `--` for Claude) |
| `credits` | credit balance (from Codex's `rate_limits.credits`; `--` when the tool/plan has none), `23.5` |
| `cwd` | session working directory (basename) |
| `stale` | `⚠1h ` (marker + data age) when older than 60 minutes, else empty (Codex has no live feed and its limit windows stay valid for hours, so it goes stale after 5 hours; in the status line Claude's data comes fresh from Claude Code on every call, so `{claude.stale}` is normally empty) |
| `age` | age of the data, `42s` / `5m` / `1h` / `3d` |

Every `tokens` scope counts **billable tokens** (input + cache writes + cache
reads + output), the same denominator as `cost` (since v0.5.0; before that,
`tokens.all` / `tokens.session.today` counted "new" tokens without cache reads).
`*.session.today` is Claude only — tacho doesn't compute the current
session's today-only share for Codex, so it renders `--`.

Missing values render as `--`. The 5h / weekly percentages and bars show
**headroom** (percent left) by default while their color still follows usage
(<50% used green, ≥50% yellow, ≥80% red); `tacho config set limits.display used`
switches them to **usage** (the gauges then fill as you use them; colors are
unchanged; `remaining` switches back to the default). `ctx` stays a usage
figure. Disable colors with `--no-color` or `NO_COLOR`.

### cmux sidebar

Inside a [cmux](https://cmux.com) terminal, `tacho statusline` automatically
mirrors the status to the workspace sidebar as colored pills —
`claude ctx24% 5h76% wk59%` / `codex 5h96% wk89%` (5h / wk are headroom by
default, following `limits.display`; ctx is usage; without rate-limit windows
the pill shows session tokens instead, e.g. `claude ctx24% 989ktok`, and a
stale pill shows its age right after the tool name, e.g. `claude ⚠1h …`),
colored green/yellow/red by the highest usage among the reported windows
(5h / wk) and gray when stale — with no extra setup beyond the status line.
It detects cmux via
`CMUX_WORKSPACE_ID` and talks through the cmux CLI (`TACHO_CMUX_BIN`, else
`cmux` on PATH, else the one bundled with cmux.app), fire-and-forget, so the
status line latency is unaffected.

Manual control:

```sh
tacho cmux push    # push pills once (run it inside a cmux terminal; by default it can't reach cmux from cron or other outside processes)
tacho cmux clear   # remove tacho's pills
```

### macOS menu bar (SwiftBar)

For an always-visible gauge regardless of which agent is running, a
[SwiftBar](https://github.com/swiftbar/SwiftBar) plugin is bundled. The menu
bar shows a tachometer per tool — the logo ringed by a fuel-gauge-style ring
showing the 5-hour headroom by default (the Metric setting below can switch it
to weekly), which drains clockwise as you use it (or fills up, with the usage
display); clicking reveals per-tool details. The ring is colored by usage
(green/yellow/red, gray when stale). Only the ring marks stale data — the
number and moon-text displays show old values as is — so check each tool's
dropdown header (`⚠` + age) for how old the data is. The logo and track are
white by default (for Dark mode or a wallpaper-darkened menu bar); set
`TACHO_APPEARANCE=light` if your menu bar is light. Set
`TACHO_SWIFTBAR_TEXT=1` to fall back to the moon-dial text (`C🌔 X🌑`; with the
default headroom display a full moon = all left, with the usage display a full
moon = used up).

```sh
brew install swiftbar   # if you don't have it
cp contrib/tacho.30s.sh <your SwiftBar plugin folder>/
chmod +x <plugin folder>/tacho.30s.sh
```

The `30s` in the filename is the refresh interval (rename to change). The
script just execs `tacho swiftbar`, so display changes belong in the tacho
renderer. Plugins launched by SwiftBar may not see your shell config (e.g.
`~/.zshrc`), so set `TACHO_APPEARANCE` / `TACHO_SWIFTBAR_TEXT` in this file,
before the `exec` line (e.g. `export TACHO_APPEARANCE=light`). The script also
adds only `/opt/homebrew/bin` and `~/go/bin` to PATH; if `tacho` lives
elsewhere (e.g. an npm global under nvm / mise), replace `tacho` on the `exec`
line with the absolute path `tacho doctor` prints as `running:`.

#### Configuring what's shown

The dropdown lists every metric per tool (5h / weekly / context / cost /
tokens); the menu bar shows the one you select. If a tool doesn't report the
selected 5h / weekly window (e.g. Codex temporarily without a 5h window), the
menu bar shows a window it does report instead (tagged like `X wk85%` in the
number style). The moon-dial text (`TACHO_SWIFTBAR_TEXT=1`) currently ignores
the selection and always draws the 5h window (or the first reported one;
#266).

Below them, the **last 7 days of cost/tokens** appear one row per day
(`09/17  C $150.76/179M  X $0.13/27k`). The figures are defined exactly like
`tacho daily`, and today's row equals the cost / tokens rows above (both read
`$0.00` / `0` on a day with no usage yet); they differ only when today's total
is unknown and the rows above fall back to the current session's value. Each
tool's three costliest days are shown in blue so the heavy days stand out
(days with a zero or unknown cost don't compete). Earlier days are computed
only when the cache lacks them — normally just yesterday, after midnight; the
6 days up to yesterday at once on the first run or when the cache is rebuilt —
and kept in `daily-history.json` in the cache directory (`tacho doctor`
prints where); anything older than 7 days is dropped. It is a derived cache
that can always be rebuilt from the logs, is rebuilt when the tacho binary
(version) or `pricing.json` changes, and can be deleted freely (the next
refresh recreates it).

The **Settings** submenu at the bottom picks values from a list (the current
choice is check-marked):

- **Display**: meter (gauge) or number (cost / tokens have no gauge, so they show as a number even with meter)
- **Metric**: 5h limit / weekly limit / cost / tokens (radio; context is excluded — it churns per session and isn't a useful at-a-glance menu-bar figure). cost / tokens show today's total (marked `/d`) and fall back to the current session's value (no `/d`) when the daily total is unknown
- **Limit display**: remaining / used (what the 5h / weekly percentages, gauges, and ring show; the same setting drives the status line and `tacho`; colors still follow usage)
- **Tools**: Claude / Codex (checkboxes). Besides the menu bar, dropdown, and notifications, this also filters `tacho`, `tacho watch`, `tacho daily`, and the cmux pills (listed in `tools` order). The status line is not filtered: it shows whichever tools its template names (the default template includes Codex), and `tacho status --json` always lists both tools

These submenu labels are shown in Japanese: Display = 表示形式 (meter / number =
メーター / 数字), Metric = 指標, Limit display = リミット表示 (remaining / used =
残量 / 使用率), Tools = 表示するツール. The last-7-days heading
(直近 7 日の cost/tokens) and the `/d` note (当日合計 = today's total across
all sessions) are in Japanese too.

Or via the CLI (config lives in `~/.config/tachograph/config.json` — or under
`$XDG_CONFIG_HOME/tachograph/` when `XDG_CONFIG_HOME` is set, along with
`statusline.tmpl` and `pricing.json`; `tacho config path` prints the actual
location):

```sh
tacho config show
tacho config set menubar.style number   # meter → number (meter | number)
tacho config set menubar.metric cost    # show spent cost instead of limits (limit_5h | limit_weekly | cost | tokens)
tacho config set limits.display used    # 5h / weekly as usage instead of headroom (remaining | used)
tacho config set tools codex            # show only Codex (comma-separated claude-code / codex, shown in the order you list them)
```

#### Headroom notifications (off by default)

When a 5h / weekly window's headroom drops to a percentage you set, tacho
raises a macOS notification. There is no daemon: the check rides on
each SwiftBar run of the plugin (every 30 seconds with the bundled
`tacho.30s.sh`), so notifications only happen **while the SwiftBar plugin is
running** (the status line and `tacho` never notify). Notifications are
posted by SwiftBar, so allow SwiftBar in the macOS Notifications settings.

```sh
tacho config set notify.thresholds 50,30,10   # notify at 50% / 30% / 10% left
tacho config set notify.thresholds ""         # off (the default)
```

- Thresholds are "% left" (whole numbers 1–99, several allowed). They stay
  headroom figures even when `limits.display` shows usage, and so does the
  notification text.
- One list applies to every shown tool (`tools`) × 5h / weekly. The text names
  both, e.g. `Claude weekly: 28% left · resets ↻09/20`.
- Each (tool, window, threshold) fires **once per reset cycle**; it re-arms
  when the headroom rises back above the threshold or the reset time changes.
  Dropping past several thresholds at once fires only the deepest one.
- Stale values never fire. A notification that fails to send is retried on
  the next refresh.
- What has been announced is kept in `notify-state.json` in the cache
  directory; deleting it only means one more notification.

### Cost price table (approximate, overridable)

In the menu bar and dropdown, `cost` and `tokens` (`cost.all` / `tokens.all` in
the status line) are **today's totals across all sessions** (`tokens` counts
billable tokens, cache reads included — the same denominator as `cost`). For
Claude Code, this includes regular sessions plus subagents / workflows
transcripts under those sessions. Cost is estimated from a per-model price
table (tokens × rate). Prices are rough, not exact, so override or extend them
in `~/.config/tachograph/pricing.json` (USD per million tokens):

```json
{
  "claude-fable": { "input": 10, "output": 50, "cache_read": 1, "cache_write": 12.5 },
  "gpt-5":        { "input": 1.25, "output": 10, "cache_read": 0.125, "cache_write": 1.25 }
}
```

Only the fields you set override the built-in defaults (e.g. set just `input`
and the other rates stay at their defaults — partial overrides merge). A **new
model id** not in the table has no defaults, so any rate you leave out is `0` —
even when the key extends a built-in one (`claude-fable-5-2` does not inherit
`claude-fable`'s rates). Keys match model ids by prefix (`claude-fable` matches
`claude-fable-5`), and the **longest matching key wins**: to override a tier
that has its own built-in entry (`claude-fable-5-1`, `claude-mythos-5-1`,
`claude-opus-5-5`, `claude-sonnet-5`, …), use that exact key — an override on
`claude-fable` does not reach `claude-fable-5-1`. A Bedrock-style id such as
`us.anthropic.claude-…` that matches no key as written is retried without its
`[region.]anthropic.` / `openai.` prefix, so a key that keeps the prefix, e.g.
`us.anthropic.claude-fable-5`, applies only to Bedrock ids starting with it —
it is a new key with no built-in defaults, so set all four rates. When a Claude
transcript records 1-hour cache writes, they are priced at 2x the input rate
(`cache_write` is the 5-minute rate and does not apply to them). Models not in
the price table still count toward the token total, but are excluded from the
cost calculation and the cost total (if no priced model ran that day, cost
shows as unknown, `--`). If `pricing.json` can't be parsed (bad JSON, or a
value of the wrong type), the whole file is ignored and the built-in prices
stand; `tacho doctor` flags both syntax errors and wrongly typed values.

### Per-day cost / tokens (`tacho daily`)

"How much did I use yesterday?" and "what did the last month look like?",
as a table in the terminal.

```sh
tacho daily            # last 30 days
tacho daily -days 7    # last 7 days
```

```
day         claude $  claude tokens  codex $  codex tokens  total $
2026-09-16   $138.28         126.9M    $0.14           21k  $138.42
2026-09-17   $150.76           179M    $0.13           27k  $150.89
2026-09-18    $46.08          23.9M    $0.00             0   $46.08
-------------------------------------------------------------------
total        $335.13         329.8M    $0.27           48k  $335.40

note: recomputed from the logs still on disk; days past Claude Code's transcript retention are under-reported.
```

- The figures are defined exactly like the daily totals (`cost.all` /
  `tokens.all`, the `/d` values in SwiftBar): today's row equals `daily` in
  `tacho status`, and yesterday's row is yesterday's "today" figure,
  recomputed from the logs. The tools shown follow the `tools` setting.
- **tacho stores no history.** Every call recomputes from the Claude Code
  transcripts and Codex session logs (a few seconds for 30 days), so the
  table only ever shows what is still on disk: a day whose transcripts
  Claude Code has deleted past its retention period reads smaller than it
  was (that is what the footnote warns about). For long-term retention or
  analysis, use a dedicated tool.
- `--` means that tool's logs could not be read (unknown); 0 means the logs
  hold no usage. Cost is `--` on a day with usage but no priced model, and
  `$0.00` on a day with no usage. In the `total` row, a tool reads `--` if any
  of its days is unknown, and its cost reads `--` if any day's cost does.

### Codex TUI

Codex's own status line is configured natively — run `/statusline` in the
TUI and pick e.g. `model + five-hour-limit + weekly-limit`. tachograph
does not (and cannot) draw inside the Codex TUI; it reads Codex session
logs non-invasively for display everywhere else.

## Which variants are covered?

tachograph reads local logs (`~/.claude/projects`, `~/.codex/sessions`, or
`projects` / `sessions` under `CLAUDE_CONFIG_DIR` / `CODEX_HOME` when set).
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
