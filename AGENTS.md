# Repository Instructions

## Language

- Use Japanese for day-to-day discussion and project documentation unless a
  user explicitly asks otherwise.

## Project

- `tachograph` provides the `tacho` CLI, a compact instrument panel for Claude
  Code and Codex usage, rate-limit, context, and cost signals.
- Keep the CLI dependency-light and fast. Prefer the Go standard library unless
  a dependency clearly pays for itself.

## Source Of Truth

- Tracked docs in this repo cover public usage and schema details:
  - `README.md` (Japanese) / `README.en.md` (English) for user-facing usage.
    Update both languages in the same change.
  - `npm/README.md` and the `description` in `npm/package.json` for the npm
    package page (a short English summary linking to the full README); keep
    them in step with the README intro.
  - `docs/schema.md` for the emitted JSON schema.
- Planning dashboards, progress logs, and private reference URLs live outside
  the repository. Their local pointers belong in `.agent-context.local.md`.
- `.agent-context.local.md` is untracked, user-owned, and read-only for agents.
  Do not edit it or copy its private references into tracked files.

## Development

- Keep changes small and issue-scoped. Avoid unrelated refactors.
- Preserve existing CLI output, JSON schema fields, config keys, and documented
  behavior unless the task explicitly changes that contract.
- Use existing package boundaries:
  - `internal/collector/*` reads agent data sources; `internal/agentpath`
    resolves the agents' data directories (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`).
  - `internal/daily` computes daily aggregates; `internal/pricing` holds the
    approximate price table and its `pricing.json` override.
  - `internal/core` assembles schema output; `internal/schema` defines its Go
    types (keep `docs/schema.md`, the authoritative spec, in sync).
  - `internal/config` owns `config.json`; `internal/cache` is the short-lived
    file cache.
  - `internal/render`, `internal/swiftbar`, `internal/menubar` (the menu bar
    gauge image), and `internal/cmuxbar` render views. The preset catalog in
    `internal/render/presets.go` is mirrored by hand in the preset tables of
    both READMEs (name, description, sample output) and in
    `contrib/statusline.tmpl.example` (full templates); update them together.
  - `internal/notify` raises macOS notifications; `internal/setup` generates
    and applies the Claude Code statusLine config.
  - `cmd/tacho` owns CLI wiring, including `tacho doctor`.
- Do not add tracked local paths, secrets, private URLs, or user-specific
  machine data.

## Verification

- For Go changes, run:

```sh
GOCACHE="$(mktemp -d)" go vet ./...
GOCACHE="$(mktemp -d)" go test ./...
```

- For npm wrapper changes, run from `npm/`:

```sh
node test.js
npm_config_cache="$(mktemp -d)" npm pack --dry-run
```

## Release Notes

- The latest published release may lag behind `main`. Summarize unreleased
  changes from `git log vX.Y.Z..HEAD` after confirming the latest GitHub
  Release and npm version.
- Release prep is a `chore: prepare vX.Y.Z release` PR that bumps
  `npm/package.json` and the version-pin examples in `README.md` /
  `README.en.md`. The `vX.Y.Z` tag push then runs GoReleaser and npm publish;
  the release workflow fails if the tag and `npm/package.json` disagree.
- GoReleaser builds the GitHub Release changelog from commit subjects and
  drops those starting with exactly `docs:` / `test:` / `chore:`; scoped
  prefixes such as `chore(pricing):` are kept.
