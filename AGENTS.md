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
  - `README.md` / `README.en.md` for user-facing usage.
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
  - `internal/collector/*` reads agent data sources.
  - `internal/daily` computes daily aggregates.
  - `internal/core` assembles schema output.
  - `internal/render`, `internal/swiftbar`, and `internal/cmuxbar` render views.
  - `cmd/tacho` owns CLI wiring.
- Do not add tracked local paths, secrets, private URLs, or user-specific
  machine data.

## Verification

- For Go changes, run:

```sh
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
