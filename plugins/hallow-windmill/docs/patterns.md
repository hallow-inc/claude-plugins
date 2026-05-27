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

### Two flavors of secret storage

| Flavor | When to use | Path / naming |
|---|---|---|
| **Standalone secret var** | Cross-domain shared secret (one secret consumed by multiple resources) | `f/platform_secrets/<domain>__<name>`, double underscore |
| **Resource-backed secret** | Single resource owns the secret (e.g. a Slack bot token tied to one resource) | Auto-linked at the resource's own path. Do NOT pre-create the variable. |

Resource-backed flavor: when a resource type has a secret-typed field, Windmill auto-creates a backing variable at the **resource's own path** marked `is_linked: true`. Manually creating a variable at that path collides with the auto-linked one. Earlier double-underscore naming at the resource path predates this auto-link behavior and conflicts with it.

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
7. **No relative imports between inline scripts in the same flow.** Each inline rawscript is bundled independently — `import {...} from "./sibling.ts"` fails at runtime. Fold helpers into the single module that uses them, or promote to a real script + reference via PathScript.
8. **Inline-script filenames are wmill-owned.** `*.inline_script.ts` names are derived from each step's content/summary, and the flow CODE lives EMBEDDED in `flow.yaml` server-side. Hand-renaming the files is non-convergent: push reports "up to date" (code unchanged) but the differ keeps flagging the rename. To shorten a filename, change the step's `id` or `summary` so wmill re-derives the name.
9. **Sync `request_type: sync` flows return JSON by default.** To respond with a raw string (HTML, plain text), set flow-level `early_return: results.<module>.<field>` — the expression value becomes the response body verbatim. Content-Type still defaults to JSON; setting `text/html` needs trigger config.

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

### `permissioned_as` is deployer-stamped

Both HTTP triggers and schedules have `permissioned_as` (and schedules also `email`). The server STAMPS these fields to the identity of whoever pushed the entity — local YAML values are overwritten on push. If a trigger or schedule needs a non-deployer run-as principal, that user must perform the push. Don't try to set arbitrary values in YAML; they will revert and churn.

### Workspaced HTTP route URL

External callers POST to `${BASE_URL}/api/r/<workspace>/<route_path>` — NO extra `w/` prefix. The "not found" error includes `/w/<ws>/...` from the server's internal lookup key; copying that into the URL is a common 404 source. The trigger page in the Windmill UI shows the canonical curl example.

### HTTP triggers + folder ACL

Folder ACL gates trigger route lookup. Even with valid `windmill` auth, a caller who can't READ the trigger's folder gets `"Trigger not found"`. Standard pattern: put the trigger in `f/shared/` (g/all readable); set `script_path` to an impl in an admin-only folder; set `permissioned_as: u/<admin>` so the script can decrypt admin-only secrets regardless of caller.

### Create-then-disable for HTTP triggers

`POST /http_triggers/create` ignores `enabled`/`mode` fields — always creates `mode: "enabled"`. To stage as disabled: create, then `POST /http_triggers/update/<path>` with `mode: "disabled"`. Schedules differ — `create` honors `enabled: false` directly.

---

## 8b. Worker tags + Fargate IAM (S3 / sandbox bucket)

Hallow's Windmill runs two worker groups:

| Worker group | Host | IAM | Has sandbox-bucket access? |
|---|---|---|---|
| `default` | Coolify/EC2 container | EC2 instance role `hallow-platform-ec2` | **NO** — no grant to `hallow-platform-sandbox-data-*` |
| `fargate` | ECS Fargate task | Task role `windmill-worker-task-role` | YES — grants `s3:List*/Get*/Put*/Delete*` + KMS |

The Pulumi component (`infra/pulumi/windmill/component.go` "windmill-worker-sandbox-data-policy") grants the sandbox bucket policy ONLY to the Fargate task role. By design — broadening to the EC2 host role would put bucket access on every default-worker job.

### Rule: any step touching `s3:///` or `f/storage/sandbox_data` MUST have `tag: fargate`

In `flow.yaml`:

```yaml
- id: write_to_s3
  tag: fargate
  value:
    type: rawscript
    language: bun
    ...
```

### Masked-error symptom when the tag is missing

The AWS SDK's `AccessDenied` error object is cyclic. When the Windmill bun wrapper tries to serialize the unhandled error for the job result, it throws `"TypeError: JSON.stringify cannot serialize cyclic structures"` at `wrapper.mjs writeFile(result.json)` instead of surfacing AccessDenied. The cyclic-error symptom is the tell that the step ran on the wrong worker.

**To unmask while debugging**, wrap: `catch (e) { return \`${e.name}: ${e.message}\`; }`. Real message: `"User: arn:aws:sts::131654760153:assumed-role/hallow-platform-ec2/i-... is not authorized to perform: s3:ListBucket"`.

### `wmill script preview` is non-deterministic for tag routing

The CLI's `script preview` has no `--tag` flag and ignores the sidecar `.script.yaml` tag — it routes to whichever worker happens to be free. So you can't validate sandbox-bucket scripts via `wmill script preview` — wrap in a flow with `tag: fargate` and run the flow instead.

### Already-deployed flows missing the tag

Any S3-as-DataTable flow written before this rule was understood is broken — it has never actually run successfully against S3. Examples in `infra/windmill/dev/`: anything that imports `f/storage/sandbox_data` and doesn't have `tag: fargate` on the S3-touching module. Audit before enabling on a schedule.

---

## 8c. Multi-segment filename convention (schedules + triggers)

A schedule or HTTP trigger whose path is `f/foo/bar_baz_v2` MUST live on disk as `bar_baz_v2.schedule.yaml` (underscore). A dotted name like `bar_baz.v2.schedule.yaml` derives a DIFFERENT entity path that won't match the server — `wmill sync` then shows the live entity as a `-` delete with no paired `+`, and a push would destroy it. Always join multi-segment stems with `_`.

Same rule for `*.http_trigger.yaml`. Single-segment names (`cleanup.schedule.yaml`) are fine — only multi-segment stems with dots trip the path derivation.

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
- [ ] If schedule or HTTP trigger has a multi-segment path stem (e.g. `foo_bar_v2`), filename uses `_` underscores between segments — NOT `.` dots (dotted filenames derive a different entity path and a push will DESTROY the live route/schedule).
- [ ] If any step touches the sandbox S3 bucket (`s3:///` or `f/storage/sandbox_data`), the flow module has `tag: fargate` set (default EC2 worker has no IAM grant — masked cyclic-structure error otherwise; see §8b).
- [ ] `wmill job logs <id>` shows success for the path you touched (don't trust UI alone).
- [ ] Visual preview offered to the user.
