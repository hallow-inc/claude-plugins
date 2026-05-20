# Windmill Patterns — Hallow `dev` Workspace

> **Scope:** engineer-oriented reference. Covers TypeScript shapes, YAML schemas, ACL semantics, secrets routing, and operational rules. Assumes familiarity with scripts/flows/apps/triggers/schedules as Windmill concepts.
>
> **Non-technical entry point:** read `${CLAUDE_PLUGIN_ROOT}/docs/getting-started.md` instead — outcome-framed, plain English, no jargon. Or just say "I want to automate X" and let the `windmill-build` skill drive.
>
> **Need to know what already exists before building?** Use the `windmill-discover` skill or read `${CLAUDE_PLUGIN_ROOT}/docs/toolbox.md`.
>
> **Tool broken?** Use the `windmill-debug` skill.

Audience: Hallow team members (engineers or anyone comfortable reading code) creating or modifying Windmill scripts, flows, apps, triggers, schedules, and resources. Rule-shaped, terse. Hallow-specific constraints — overrides generic Windmill docs where they conflict.

Companion docs in this plugin (`${CLAUDE_PLUGIN_ROOT}/docs/`):
- `folders-groups.md` — ACL semantics, where your files end up
- `shared-tool-template.md` — recipe for adding a new reusable tool
- `toolbox.md` — catalog of existing shared tools in the `dev` workspace
- `onboarding.md` — first-time setup (covered end-to-end by `/wmill-setup`)

This doc covers what is **established in the workspace** and **non-obvious from source files alone**. Admin/infra ops are out of scope.

---

## 1. Entity creation — always via skills, never by hand

| Entity | Skill | Scaffold command (run yourself) |
|---|---|---|
| Script | `write-script-<lang>` (default `bun`) | n/a — skill writes files directly |
| Flow | `write-flow` | `wmill flow new <path> --summary '<s>'` |
| Raw app | `raw-app` | `wmill app new --summary --path --framework` |
| HTTP/WS/Kafka/etc trigger | `triggers` | n/a |
| Cron schedule | `schedules` | n/a |
| Resource / resource type | `resources` | n/a |
| Workflow-as-code script | `write-workflow-as-code` | n/a |

Rules:
- NEVER hand-scaffold a flow folder + `flow.yaml`. Run `wmill flow new` yourself.
- NEVER tell the user to run `wmill flow new` / `wmill app new`. You run it.
- ASK the user for `path`, `summary`, and (apps) `framework` via a single `AskUserQuestion` call if missing. Never invent.
- Path lives under `f/<folder>/...` (team folder) or `u/<user>/...` (user-private). Ask which.

---

## 2. On-disk file shapes

Every entity round-trips through YAML on disk. Skills produce these — do not write by hand unless editing an existing one.

```
f/<folder>/
  folder.meta.yaml                # ACL + display name
  <script>.ts                     # code
  <script>.script.yaml            # metadata (summary, schema, lock ref, kind)
  <script>.script.lock            # frozen deps (regenerated on push)
  <script>.http_trigger.yaml      # optional: HTTP trigger pointing at script
  <flow>.flow/
    flow.yaml                     # modules, schema, failure_module
    <inline>.inline_script.ts     # inline raw scripts referenced from flow.yaml
    <inline>.inline_script.lock
  <app>.raw_app/
    raw_app.yaml                  # summary + framework
    App.tsx, index.tsx, ...       # framework code
    backend/                      # server scripts the app calls
  <resource>.resource.yaml        # value + resource_type
  <var>.variable.yaml             # value + is_secret + labels
  <schedule>.schedule.yaml        # cron + script_path + args
```

Canonical examples in this repo:
- Script: `dev/f/shared/slack_post.{ts,script.yaml}`
- Flow: `dev/f/slack_bot/agent_reply.flow/flow.yaml`
- Raw app: `dev/f/platform/query_runner.raw_app/`
- HTTP trigger: `dev/f/slack_bot/handle_mention.http_trigger.yaml`
- Resource: `dev/f/webhooks/db.resource.yaml`
- Variable: `dev/f/shared/sandbox_s3_bucket.variable.yaml`
- Schedule: `dev/f/webhooks/cleanup.schedule.yaml`
- Folder meta: `dev/f/shared/folder.meta.yaml`

---

## 3. The local-yaml-first workflow

The single most-violated rule. Memorize.

> **Path note:** `infra/windmill/dev/f/<...>` refers to the platform repo's authoritative YAML tree (where committed entity definitions live). Your local Windmill dev-loop workdir — where `.mcp.json` and `wmill.yaml` live — is `~/dev/wmill/` (or Windows equivalent), set up by `/wmill-setup`. Edits flow: local edits → MCP push to server → user commits to platform repo.

1. Edit YAML in `infra/windmill/dev/f/<...>` locally first.
2. Mirror to the server via the **MCP `windmill` API tools** (`createScript`, `updateFlow`, `createResource`, etc.) or via the Windmill **UI** — never `wmill sync`.
3. User handles `git commit` themselves. You never commit.

Why this order:
- The repo is the source of truth.
- `wmill sync push` is **banned** in this workspace (see §7). It deletes server state not in local files and clobbers secret variables.
- Reverse order (UI first → pull) drifts the repo silently.

If you need to *read* current server state, use MCP tools (`getScriptByPath`, `getFlowByPath`, `getResource`, `listScripts`). Don't `wmill sync pull` either — same destructive risk in reverse on unrelated files.

---

## 4. Shared atoms — reuse before reinvent

Established cross-cutting building blocks. Check here before writing anything new.

| Path | Use it when |
|---|---|
| `f/shared/slack_post` | Posting to Slack from any script/flow. Webhook URL **or** bot `channel`. Returns `ts` in bot mode. |
| `f/shared/error_to_slack` | Workspace-level error handler. Auto-routes via folder ancestry → `error_webhook` resource → falls back to `f/shared/slack_ops_webhook`. |
| `f/shared/assert_principal` | Gate a flow/script to allowed groups/users. Use as the first module of any privileged flow. |
| `f/slack_tools/_redact` | Library (no `main()`). `import { redact } from "/f/slack_tools/_redact.ts"` before returning error strings to an LLM. Defense in depth. |
| `f/slack_bot/bot_token` | Slack bot token resource. Default token source for `slack_post` bot mode. |

Adding a new shared atom:
- Cross-workspace: `f/shared/<name>`, owners `g/admin`, `g/all: false` (opt-in callers).
- Domain-scoped: `f/<domain>/<name>` (e.g. `f/slack_tools/`, `f/storage/`).
- Schema: typed args with `description` (renders as tooltip), `default: null` for optional, `required:` for mandatory.
- Return type: typed object, never raw error strings (use `_redact` first).

Full recipe: see `${CLAUDE_PLUGIN_ROOT}/docs/shared-tool-template.md`.

---

## 5. Secrets + permissions

### The `f/platform_secrets/` pattern

All workspace-wide secret values live in `f/platform_secrets/` (admin-only, `owners: [g/admin]`, no `g/all`). Resources in domain folders **reference** secrets via `$var:f/platform_secrets/<domain>__<name>`.

Naming: `<domain>__<name>` (double underscore). Example:
- Secret variable: `f/platform_secrets/webhooks__db_password`
- Consumer resource: `f/webhooks/db.resource.yaml` → `password: $var:f/platform_secrets/webhooks__db_password`

Why: domain folders are open to the relevant team for resource/script edits; the secret value itself stays gated to admins. Folder-level read = decrypt, so domain teams see only the reference path, never the plaintext.

Secrets are **never** committed in plaintext to YAML. Seed via Windmill UI in `f/platform_secrets/`. `wmill.yaml` has `skipSecrets: true` so push won't carry them either way.

### Script vs trigger permissions (critical gotcha)

- **Scripts** have no `permissioned_as` field. They always run as the caller.
- **Triggers, schedules, and flows** can elevate via `permissioned_as`.

If a script needs elevated privileges (e.g. mint a workspace token, access a resource the caller lacks), wrap it in an HTTP trigger with `permissioned_as: u/<admin>`. Example: `f/slack_tools/flow_status.http_trigger.yaml` runs as `u/brandon` so `wmill.createToken` works regardless of caller.

### Resource ACL

A resource inherits the folder ACL. Place the resource in the folder whose group should be able to *use* it (read = decrypt). The folder ACL alone determines access — subfolders are organizational, not security boundaries.

---

## 6. Flows — established conventions

From `f/slack_bot/agent_reply.flow/`, `f/platform/flows/provision_supabase_area.flow/`:

1. **First module = guard.** Privileged flows start with `f/shared/assert_principal` to gate by group/user.
2. **`failure_module` is a peer.** Use it for terminal error notification (typically Slack). Inline raw script lives as `failure.inline_script.ts` next to `flow.yaml`.
3. **AI agent tools = scripts, not flows.** `agent_reply.flow` shows the pattern: each tool is a `type: script` with `tool_type: flowmodule` and an explicit `input_transforms` dict.
4. **System prompts in the flow yaml**, not in a separate file. Include explicit refusal rules and anti-injection language for any LLM-touching agent (see `agent_reply.flow/flow.yaml` for template).
5. **Approval gates** use Windmill suspend; pair with Slack post for the approval prompt. See `provision_supabase_area`.
6. **HTTP trigger → flow dispatch**: use `runFlowAsync`, **not** `runScriptAsync`. The latter silently no-ops on flow paths (returns `queued: true` but never dispatches).

---

## 7. Operational rules

### Never use `wmill sync`

`wmill sync push` and `wmill sync pull` are banned in this workspace. Reasons:
- Push deletes server state not present in local files.
- Push clobbers secret variables back to placeholder values.
- Sync uses the *active workspace* (`wmill workspace`), not the directory name — easy to push `dev` content to `admins` or `prod` by accident.

If you ever see a `wmill sync` command suggested in another doc, ignore it for this workspace. Use MCP tools or the UI.

### `wmill.yaml` excludes — respect them

```yaml
excludes:
  - u/**                # user-private — never touch
  - f/b2b/**            # owned by non-admin user(s)
  - f/**/*.raw_app/AGENTS.md
  - f/**/*.raw_app/sql_to_apply/**
```

When a non-admin user creates a new top-level `f/<name>/` folder, add it to `excludes` (or sync push will delete their work).

### Resource type is immutable

Changing a resource's `resource_type` requires API delete + recreate. `wmill` will silently keep the old type. Use the `resources` skill which handles this.

### Debugging job failures

When a script or flow fails, **fetch the job before speculating**. CLI:

```bash
wmill job list --script-path <path> --limit 10     # recent runs of one entity
wmill job list --failed --limit 20                  # recent failures, workspace-wide
wmill job get <id>                                  # status + step tree (for flows)
wmill job logs <id>                                 # stdout/stderr
wmill job result <id>                               # JSON result
```

For flows, `wmill job get` returns sub-job IDs per step — drill into the failing step's logs specifically.

---

## 8. HTTP trigger specifics

Two gotchas from `f/slack_bot/handle_mention.http_trigger.yaml`:

1. **Headers require a `preprocessor`.** `main()` never receives request headers. Define `export async function preprocessor(event)` to read headers and fetch resources before main runs. The HTTP trigger has no `args:` field — preprocessor is the only way.

2. **Custom signature verification** (Slack v0, Stripe, etc) needs `authentication_method: none` + `raw_string: true`. Windmill's built-in schemes don't cover HMAC headers. Verify yourself in the script, using the raw body to recompute the HMAC.

Sync mode (`request_type: sync`) returns the script result as the response body — required for Slack url_verification (3-second ack window). Async mode (`request_type: async`) returns a job ID immediately.

---

## 9. Visual preview after writing

After creating or modifying any flow / script / app, offer visual verification via the `preview` skill. It opens the Windmill dev page for that entity. URL form: `https://windmill.platform.hallow.app/<workspace>/<kind>/<path>`.

---

## 10. Checklist before declaring "done"

- [ ] Used the correct `write-*` skill (not freehand YAML).
- [ ] File on disk first, then mirrored to server via MCP API (not `wmill sync`).
- [ ] Folder ACL appropriate for who should read/run.
- [ ] Secrets in `f/platform_secrets/`, referenced by `$var:` path — never plaintext.
- [ ] If LLM-touching: anti-injection rules in system prompt, `_redact` on any error strings returned.
- [ ] If flow + HTTP trigger: trigger uses `runFlowAsync`, sets `permissioned_as` if elevation needed.
- [ ] If new top-level `f/<folder>/` owned by non-admin: added to `wmill.yaml` `excludes`.
- [ ] `wmill job logs <id>` shows success for the path you touched (don't trust UI alone).
- [ ] Visual preview offered to the user.
