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
  - `internal/config` owns `config.json`; `internal/cache` owns tacho's state
    in the cache dir: the short-lived status cache (`status.json`), the
    Claude snapshot, derived caches (daily history, session trees), the
    notification record, and the cross-process locks.
  - `internal/render`, `internal/swiftbar`, and `internal/menubar` (the menu
    bar gauge image) render views. The preset catalog in
    `internal/render/presets.go` is mirrored by hand in the preset tables of
    both READMEs (name, description, sample output) and in
    `contrib/statusline.tmpl.example` (full templates); update them together.
  - `internal/notify` raises macOS notifications; `internal/setup` generates
    and applies the Claude Code statusLine config and the SwiftBar plugin.
  - `cmd/tacho` owns CLI wiring, including `tacho doctor`.
  - `scripts/check-release-assets` checks a release's archives (checksums,
    the binary's place, platform, and version) for the release workflow,
    and a GoReleaser snapshot's (version aside) for CI on every PR.
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
- `release.yml` publishes to npm only after two gates pass on the published
  GitHub Release: an install through the npm wrapper on ubuntu / macOS /
  Windows (`npm-install-smoke.yml`) and a static check of all six archives
  (`scripts/check-release-assets`). GoReleaser is pinned (`v2.18.3`) in both
  workflows; raise it on purpose. CI builds a GoReleaser snapshot on every PR
  and runs the same archive check (`-skip-version`), and runs `go test ./...`
  on Windows too.
- When a release run fails: never move or reuse a published tag (the Go
  module proxy caches it); re-run the failed jobs for a transient network
  error; for a broken asset, delete the GitHub Release, keep the tag, fix,
  and release the next patch; if only npm publish failed, re-run that job.
  Once the gates pass, never delete or replace that version's assets — the
  published npm version downloads them on every install.
