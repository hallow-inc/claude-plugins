---
name: cli-commands
description: Use when running `wmill` CLI commands — `script preview/run`, `flow run`, `job get/list/logs`, `sync push/pull`, `workspace`, `version`. Triggers on "run the script", "check the job", "preview vs run", "push to windmill", inspecting failures via `wmill job`, "wmill generate-metadata no scripts found", "wmill bootstrap", "wmill --workspace", "wrong workspace push", "active workspace footgun", "script preview wrong worker tag". Covers preview-vs-run-vs-push decision, workspace flags (always pass `--workspace dev` — sync targets active workspace not cwd), JSON output handling, bootstrap-before-generate-metadata for hand-written scripts, `wmill script preview` ignoring sidecar tag (nondeterministic worker routing).
---

# Windmill CLI Commands

The Windmill CLI (`wmill`) provides commands for managing scripts, flows, apps, and other resources.

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
| Push local changes | `wmill sync push` | **Banned in Hallow `dev` workspace.** Use MCP `windmill` tools or UI. See `references/preview-vs-run.md`. |
| Live-reload preview UI | `wmill dev` | For app/raw-app iteration. |
| Validate trigger YAML | `wmill lint [directory]` | |

## Reference

- `references/preview-vs-run.md` — preview vs run vs sync push decision + Hallow ban
- `references/commands.md` — full `wmill` subcommand reference (all flags, all subcommands). Read on demand when you need exact CLI syntax not covered above.

## Hallow gotchas (CLI)

### ALWAYS pass `--workspace dev` explicitly — sync uses the active workspace, not the directory

`wmill sync push`/`pull` and most `wmill` subcommands resolve the target workspace from `wmill workspace` (active state), NOT from the directory you're in. Running `wmill sync push` from `infra/windmill/dev/` while the active workspace is `admins` will push dev-intended changes INTO admins — a real footgun that has tripped the CE 3-group cap in practice.

**Rules:**
1. Always pass `--workspace <ws>` explicitly on every `wmill` command. Do not rely on the active workspace.
2. If a dry-run shows unexpected `+ group` / `~ folder` operations against entities you've never seen, STOP — you're on the wrong workspace.
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
