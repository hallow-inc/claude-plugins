---
name: cli-commands
description: Use when running `wmill` CLI commands — `script preview/run`, `flow run`, `job get/list/logs`, `sync push/pull`, `workspace`, `version`. Triggers on "run the script", "check the job", "preview vs run", "push to windmill", inspecting failures via `wmill job`. Covers preview-vs-run-vs-push decision, workspace flags, JSON output handling.
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
