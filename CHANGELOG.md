# Changelog

All notable changes to plugins in this marketplace.

This repo uses **commit-SHA versioning** — every commit on `main` is a new
version. Entries below group changes by date for human readability; the
authoritative ordering is `git log`. If you need to pin to a specific commit
SHA from this list, use `/plugin install <plugin>@<marketplace>@<sha>`.

## Format

Each entry is dated, scoped to a plugin, and tagged:

- **added** — new feature, skill, doc, command
- **changed** — behavior change, doc rewrite, refactor
- **fixed** — bug, broken ref, stale content
- **removed** — file or feature deleted
- **security** — anything that prevents token/secret leak or hardens defaults

## Unreleased

### added

- Plugin renamed from `windmill-onboarding` to `hallow-windmill`. Install
  command is now `/plugin install hallow-windmill@hallow-claude-plugins`.
- `plugins/hallow-windmill/docs/installing.md` — user-facing install runbook
  covering GitHub Desktop, terminal `git clone`, and manual ZIP install
  paths. Includes per-symptom troubleshooting table.
- `windmill-capture-learning` skill — guided capture of Windmill gotchas,
  CLI quirks, and doc contradictions. Writes a `memory_store` entry tagged
  `windmill-plugin-update` plus a scratchpad bullet in
  `plugins/hallow-windmill/WINDMILL_LEARNINGS.md`.
- `plugins/hallow-windmill/WINDMILL_LEARNINGS.md` — scratchpad file the
  capture skill appends to. Drained into proper docs periodically.

### changed

- Distribution model is now `git clone` of the private repo
  `hallow-inc/hallow-claude-plugins` directly into
  `~/.claude/plugins/marketplaces/`. Updates are `git pull` + `/reload-plugins`.
  Every commit on `main` is a new version (no `version` field in `plugin.json`).
- Repo name: `hallow-inc/claude-plugins` → `hallow-inc/hallow-claude-plugins`.

### removed

- Tailnet tarball distribution path. `.github/workflows/publish.yml`,
  `install/install.sh`, `install/install.ps1`, and
  `windmill-server/f/platform/serve_claude_plugins.*` are no longer used —
  see `__to_delete/` for the deprecated artifacts.

## 2026-05-19 — hallow-windmill initial release

First public push of `hallow-windmill`. Audience: any Hallow team member,
no engineering background required.

### added

- `windmill-build` skill — front door for "I want to automate X" / "build me a
  tool that does Y". Asks 3 plain-language questions, routes to the matching
  authoring skill.
- `windmill-discover` skill — "what tools already exist". Reads curated
  `toolbox.md` and supplements with live `mcp__windmill__listScripts`,
  recommends a verdict (use this / adapt this / build new).
- `windmill-debug` skill — "my tool failed at runtime". Fetches the failing
  job's logs + result, classifies against a 13-row error→fix table, suggests
  the fix without auto-applying.
- `hallow-windmill` reference skill, `/wmill-setup` driver,
  `/wmill-doctor` smoke test, `windmill-onboarder` subagent — first-time
  Windmill dev-loop setup across macOS / Linux / WSL / native Windows.
- `windmill-patterns` reference skill — Hallow-specific conventions (entity
  creation, secrets, ACLs, local-yaml-first, `permissioned_as`).
- 28 authoring skills bundled (`write-flow`, `write-script-*` for Bun /
  TypeScript / Python / Deno / Go / Rust / Snowflake / BigQuery / Postgres /
  MySQL / MSSQL / DuckDB / GraphQL / Bash / PowerShell / C# / Java / PHP /
  R / native-TS / Bun-native, `raw-app`, `triggers`, `schedules`, `resources`,
  `write-workflow-as-code`, `preview`, `cli-commands`). Auto-load from any
  directory once the plugin is installed.
- `docs/getting-started.md` — plain-English overview, no jargon. Authoritative
  content the `windmill-build` skill reads.
- `docs/onboarding.md` — authoritative setup procedure walked by
  `/wmill-setup`. Token mint → wmill CLI install → workdir → MCP wiring →
  smoke test, all platforms.
- `docs/patterns.md`, `docs/folders-groups.md`, `docs/toolbox.md`,
  `docs/shared-tool-template.md` — engineer-oriented references.
- `docs/mcp.json.example.jsonc` — reference shape for the project-scoped
  `.mcp.json` written by `/wmill-setup`.

### security

- `.gitignore` ensures `.mcp.json` is excluded before `claude mcp add`
  writes a token. Token suppression patterns documented for bash, zsh,
  and PowerShell history.
- `wmill sync push` and `wmill sync pull` banned in every skill and doc
  that touches deployment — including the bundled authoring skills.
  Replaced with MCP `windmill` API tool guidance.

### changed

- N/A — initial release.

### removed

- N/A — initial release.

### fixed

- N/A — initial release.
