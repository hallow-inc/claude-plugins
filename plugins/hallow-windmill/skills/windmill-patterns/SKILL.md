---
name: windmill-patterns
description: Reference skill for Hallow Windmill usage and development patterns. Loads when an engineer is creating or modifying a Windmill script, flow, app, trigger, schedule, or resource in the Hallow `dev` workspace, asking about shared atoms (slack_post, error_to_slack, assert_principal), the `f/platform_secrets/` pattern, `wmill.yaml` sync rules, the local-yaml-first workflow, `permissioned_as` elevation, or any "how do I do X in our Windmill setup" question that isn't first-time onboarding. ALSO answers how-does-it-work questions about windmill / wmill — "how do windmill schedules handle timezones", "how does permissioned_as work", "why does my windmill script behave like this", "explain how our windmill setup works" — routing to the right pattern doc. Surfaces the authoritative procedure at `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` plus companion docs.
---

# Windmill patterns (reference skill)

Loads when an engineer is *using* Windmill at Hallow — writing flows, scripts, apps, etc. — rather than setting up the dev loop for the first time (that's `/wmill-setup`).

Authoritative content: `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md`. Treat as source of truth.

## What to do when this skill loads

1. **Acknowledge the engineer's request** (one sentence). What entity? Which folder?
2. **Read `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` before answering.** Don't paraphrase from memory — Hallow-specific rules drift from generic Windmill docs.
3. **Route to the right companion doc:**
   - "Where does my file live? Who can read it?" → `${CLAUDE_PLUGIN_ROOT}/docs/folders-groups.md`
   - "What does `wmill sync` do? Can I push?" → short answer: no, never `wmill sync push` (banned at Hallow). All entity changes go through MCP `windmill` tools or the Windmill UI. Admin CI pipelines that do `sync push` to staging/prod live in the platform repo — out of scope here.
   - "Is there already a tool for X?" → `${CLAUDE_PLUGIN_ROOT}/docs/toolbox.md`
   - "I'm setting up Windmill for the first time" → wrong skill; tell them to run `/wmill-setup` instead.
4. **Never inline-walk a creation procedure yourself.** The corresponding Windmill skill (`write-flow`, `write-script-bun`, `raw-app`, `triggers`, `schedules`, `resources`, etc.) is the driver. This skill is reference.

## Hard rules (from patterns.md §7)

- **Never `wmill sync push` or `wmill sync pull`.** Banned in this workspace — it deletes server state not in local files and clobbers secret variables. Use MCP `windmill` tools (`createScript`, `updateFlow`, `getResource`, etc.) or the Windmill UI.
- **Local YAML first, then mirror to server.** Edit `infra/windmill/dev/f/<...>` first, then API. Never the other way around.
- **Pass `--workspace dev`** on any `wmill` CLI command. Active-workspace trap is real.
- **Secrets live in `f/platform_secrets/<domain>__<name>`** (admin-only). Domain folders reference them via `$var:f/platform_secrets/<domain>__<name>`. Never plaintext in YAML.
- **Scripts have no `permissioned_as`.** Only triggers, schedules, and flows can elevate. Wrap a script in an HTTP trigger with `permissioned_as: u/sandbox` if elevation is needed. `u/sandbox` is Hallow's canonical admin push identity — the push of the wrapping trigger MUST come from the `u/sandbox` token (server stamps `permissioned_as` from pusher).
- **`runFlowAsync`, not `runScriptAsync`, for flow paths.** Latter silently no-ops.
- **Use `_redact` before returning error strings to an LLM.** `import { redact } from "/f/slack_tools/_redact.ts"`.
- **Reuse before reinvent.** Check `toolbox.md` and `f/shared/` (`slack_post`, `error_to_slack`, `assert_principal`) before writing anything new.

## Companion entry points

| Entry point | When to use |
|---|---|
| `/hallow-windmill:wmill-setup` | First-time dev-loop setup. Wrong skill for usage questions. |
| `/hallow-windmill:wmill-doctor` | Verify dev loop is still working. Read-only. |
| `windmill-build` | Front door for "I want to automate X" / "build me a tool". Use when the user has not yet decided what entity to create. |
| `windmill-discover` | Find existing tools before building. Reuse-before-reinvent enforcement. |
| `windmill-debug` | A built tool failed at runtime. Fetches job logs + diagnoses. |
| Windmill authoring skills (`write-flow`, `write-script-*`, `raw-app`, `triggers`, `schedules`, `resources`, `write-workflow-as-code`, `preview`, `cli-commands`) | Drive actual creation/modification/inspection. Ship with this plugin, auto-load on relevant phrases. This skill only points at conventions. |

## When the engineer is mid-task

If they're actively writing a flow/script/app, stay in reference mode: answer the specific question, cite the relevant `patterns.md` section, and let the authoring skill (or them) do the editing. Do not seize the implementation.

## Out of scope

- Admin/infra ops (worker groups, instance settings, backups). Direct them to the platform repo `__docs/` if asked.
- Seeding secrets in `f/platform_secrets/` (admin-only via UI).
- Creating new top-level folders for non-admin users (admin must add to `wmill.yaml` excludes — flag this; don't do it).
