# `wmill` CLI: preview vs run (no push)

Decision tree any `write-script-*` skill should follow after generating or editing a script.

## Hallow ban (read first)

`wmill sync push` and `wmill sync pull` are **banned** in the Hallow `dev` workspace. They delete server state not present locally and clobber linked secret variables. **Never run them, never suggest them.**

All entity mutations — create, update, delete — go through:

1. **MCP `windmill` tools** — `createScript`, `updateScript`, `createFlow`, `updateFlow`, `createResource`, `updateResource`, `createSchedule`, `updateSchedule`, `createVariable`, `updateVariable`, etc.
2. **The Windmill UI** — for one-off edits a user wants to make themselves.

Local YAML files in `infra/windmill/dev/` are the editable source of truth; Claude mirrors them to the server one entity at a time via MCP. There is no batch push.

## Commands you DO use

| User intent | Command | Notes |
|---|---|---|
| Test local script edits before mirroring | `wmill script preview <path>` | Runs the local file. Does not deploy. **Default after writing.** |
| Run an already-deployed script | `wmill script run <path>` | Only when there are no local edits. |
| Run an already-deployed flow | `wmill flow run <path>` | Same caveat. |
| Generate `.script.yaml` + `.lock` after editing | `wmill --workspace dev script generate-metadata <path>` | Bootstrap first if file is hand-written (see `cli-commands` SKILL Hallow gotcha). |

## Preview vs run — choose by intent

If the user says "run it" / "try it" / "test it" while there are **local edits**, use `wmill script preview` — the deployed version on the server does NOT reflect those edits yet, and the only way to get them there is the MCP API path (which happens after the user accepts the change).

Only use `wmill script run` when:
- The user explicitly says "run the deployed version" / "run what's on the server".
- There is no local edit in flight.

## After writing — offer to preview, then mirror via MCP

1. Write/edit the local YAML + code file.
2. Offer to `wmill script preview` to verify behavior.
3. Once verified, mirror to the server via the matching MCP tool (`createScript` / `updateScript` for scripts; `createFlow` / `updateFlow` for flows; etc.).
4. Confirm the server state with `getScriptByPath` / `getFlowByPath`.

Never wait passively after writing. Offer the preview as a one-sentence next step (e.g. "Want me to run `wmill script preview` with sample args?").

If the user already asked to test/run/try in their original request, skip the offer and just execute `wmill script preview <path> --workspace dev -d '<args>'`. Pick plausible args from the script's declared parameters. The shape varies by language: `main(...)` for code languages, the SQL dialect's own placeholder syntax (`$1` for PostgreSQL, `?` for MySQL/Snowflake, `@P1` for MSSQL, `@name` for BigQuery, etc.), positional `$1`, `$2`, … for Bash, `param(...)` for PowerShell.

`wmill script preview` does not deploy, but it still executes script code and may cause side effects; run it yourself when the user asked to test/preview (or after confirming that execution is intended).

For a **visual** open-the-script-in-the-dev-page preview (rather than `script preview`'s run-and-print-result), use the `preview` skill.

Use `wmill resource-type list --schema` to discover available resource types.

## If you see `wmill sync` in another doc

Ignore it for this workspace. Older platform-repo docs and generic upstream Windmill examples still suggest `wmill sync push` for CI. Hallow's `dev` workspace is mutated **only** via MCP/UI/API. The local-yaml-first workflow stays the same; just substitute the mirror step.
