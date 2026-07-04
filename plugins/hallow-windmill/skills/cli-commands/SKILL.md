---
name: cli-commands
description: Use when running `wmill` CLI commands — `script preview/run`, `flow run`, `job get/list/logs`, `workspace`, `version`. Triggers on "run the script", "check the job", "preview vs run", inspecting failures via `wmill job`, "wmill generate-metadata no scripts found", "wmill bootstrap", "wmill --workspace", "active workspace footgun", "script preview wrong worker tag". Covers preview-vs-run decision (no push — sync banned at Hallow), workspace flags, JSON output handling, bootstrap-before-generate-metadata for hand-written scripts, `wmill script preview` ignoring sidecar tag (nondeterministic worker routing).
---

# Windmill CLI Commands

The Windmill CLI (`wmill`) provides commands for inspecting and previewing scripts, flows, apps, and resources.

> **Hallow rule:** `wmill sync push` and `wmill sync pull` are **banned**. All entity mutations go through the **MCP `windmill` tools** (`createScript`, `updateFlow`, `createResource`, …) or the **Windmill UI** — never the CLI. `wmill` here is for **read/preview only**: job inspection, local preview, listing, validation. Anything that mutates server state is off-limits.

## Global Options

- `--workspace <workspace:string>` - Specify the target workspace. This overrides the default workspace.
- `--debug --verbose` - Show debug/verbose logs
- `--show-diffs` - Show diff informations when syncing (may show sensitive informations)
- `--token <token:string>` - Specify an API token. This will override any stored token.
- `--base-url <baseUrl:string>` - Specify the base URL of the API. If used, --token and --workspace are required and no local remote/workspace already set will be used.
- `--config-dir <configDir:string>` - Specify a custom config directory. Overrides WMILL_CONFIG_DIR environment variable and default ~/.config location.

## When to use which command

| User intent | Command | Notes |
|---|---|---|
| Test local script edits | `wmill script preview <path>` | Default. Does not deploy. See `references/preview-vs-run.md`. |
| Run a deployed script/flow | `wmill script run <path>` / `wmill flow run <path>` | Use only when no local edits. |
| Inspect a failed job | `wmill job get <id>`, `wmill job logs <id>` | Drill into step IDs for flow failures. |
| Find recent runs | `wmill job list --script-path <path>` | JSON output via `--json | jq`. |
| List scripts/flows/apps | `wmill script list`, `wmill flow list`, `wmill app list` | |
| Inspect a resource | `wmill resource get <path>` | Use `resource-type list --schema` for type discovery. |
| Inspect a schedule/trigger | `wmill schedule get <path>`, `wmill trigger get <path>` | |
| Push local changes | — | `wmill sync push` is **banned**. Use MCP `windmill` tools (`createScript`, `updateScript`, `updateFlow`, `createResource`, `createSchedule`, `updateSchedule`, etc.) or the Windmill UI. |
| Pull server state | — | `wmill sync pull` is **banned** (clobbers unrelated local files). Read individual entities via MCP `getScriptByPath`, `getFlowByPath`, `getResource`. |
| Live-reload preview UI | `wmill dev` | For app/raw-app iteration. |
| Validate trigger YAML | `wmill lint [directory]` | |

## Reference

- `references/preview-vs-run.md` — preview vs run decision + Hallow ban on sync
- `references/commands.md` — full `wmill` subcommand reference (all flags, all subcommands). Read on demand when you need exact CLI syntax not covered above.

## Hallow gotchas (CLI)

### ALWAYS pass `--workspace dev` explicitly — CLI uses the active workspace, not the directory

Most `wmill` subcommands resolve the target workspace from `wmill workspace` (active state), NOT from the directory you're in. Running a `wmill` command from `infra/windmill/dev/` while the active workspace is `admins` hits admins. Even read commands return wrong results.

**Rules:**
1. Always pass `--workspace dev` explicitly on every `wmill` command.
2. If a `wmill` listing shows entities you've never seen, STOP — wrong workspace.
3. `wmill.yaml`'s `workspaces:` block helps but the CLI may still default to active unless `--workspace` is passed.

### `wmill generate-metadata` doesn't discover hand-written script files

If you wrote a `*.ts` / `*.py` / `*.sql` script by hand and run `wmill --workspace dev script generate-metadata <path>`, it silently produces "no scripts found" rather than generating the `*.script.yaml` + `*.script.lock`.

**Bootstrap first:**

```bash
wmill --workspace dev script bootstrap f/<folder>/<name> <lang>   # creates the metadata shell
# overwrite the bootstrapped file with your real content
wmill --workspace dev script generate-metadata f/<folder>/<name>  # NOW it works
```

### `wmill script preview` ignores tags — can't validate S3 routing

`wmill script preview` has NO `--tag` flag. Even if you put `tag: fargate` in the script's sidecar `.script.yaml`, preview routes nondeterministically to whatever worker is free (usually `default`). To validate an S3 script's behavior against the Fargate worker (which has the IAM grant), wrap it in a flow with `tag: fargate` on the module and run the flow — bare script preview lies.

### Workspace dedup — `wmill workspace add` rejects (URL, workspace) duplicates

`wmill workspace add` enforces a unique `(remote URL, workspace_id)` constraint regardless of the `name` arg. Error: `"Backend constraint violation: (URL, workspace) already exists as 'X'. Use --force to overwrite."` `--force` overwrites the existing entry (losing the prior token) — useless for keeping two aliases (e.g. `u/brandon` and `u/sandbox` against the same workspace).

**Workaround:** edit `$HOME/Library/Preferences/windmill/remotes.ndjson` directly. ndjson, NOT toml. Path is `Library/Preferences/windmill/`, NOT `.config/wmill/`. Append a duplicate row with a new name + new token:

```ndjson
{"name":"dev","remote":"https://windmill.platform.hallow.app/","workspaceId":"dev","token":"<token-1>"}
{"name":"dev-sandbox","remote":"https://windmill.platform.hallow.app/","workspaceId":"dev","token":"<token-2>"}
```

### Server-side principal swap — `set-permissioned-as` (no re-push)

`wmill trigger set-permissioned-as <path> <email> --kind <kind>` and `wmill schedule set-permissioned-as <path> <email>` swap the run-as principal **server-side** without re-pushing YAML or any sync operation. Email = login email; CLI resolves + reports username. Requires admin.

**Caveat:** the new principal MUST have read on the impl script's folder (e.g. `f/platform_secrets/`) or runtime decrypt fails. Grant via `g/admin` membership or folder extra_perms first.

Useful when migrating an entity from one canonical admin identity to another (e.g. `u/brandon` → `u/sandbox`) without disturbing the YAML on disk.

### `folders/create` + `folders/update` return BARE STRINGS, not JSON

The `POST /folders/create` and `POST /folders/update/<path>` endpoints return a bare string body (e.g. `Created folder f/foo`), NOT JSON. A blind `JSON.parse(body)` on the response throws `Unexpected identifier "Created"`. Only parse the body if it starts with `{` or `[`; otherwise treat it as a status string.

### `folders/update` has PUT semantics — always send `owners`

`POST /folders/update/<path>` replaces the whole folder record (PUT, not PATCH). Omitting `owners` from the payload EMPTIES the owners list and the call 400s with `"invalid state: owner would not have permission"`. Always include the current `owners` array in every update, even when you're only changing another field.

Related footgun: the folder **creator is auto-added to `owners`**. A manual test run as `u/sandbox` pollutes the owners of any folder it creates — clean those up post-test (re-`update` with the intended owners) so test identities don't linger as folder owners.
