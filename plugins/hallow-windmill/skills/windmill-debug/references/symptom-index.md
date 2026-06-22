# Windmill symptom index

One-stop lookup for "I see error X, where do I fix it?". Built from the windmill-debug classify table plus scattered gotchas across the plugin.

**Lookup order:** find the symptom string below → jump to the fix-source. Quote the error verbatim when in doubt — paraphrased messages mis-match.

## Auth / permissions

| Symptom (verbatim or pattern) | Diagnosis | Fix-source |
|---|---|---|
| `401 Unauthorized` / `Token expired` | Resource token dead/wrong. | `windmill-debug` classify table |
| `403 Forbidden` / `permission denied` | Caller lacks resource ACL. | `docs/patterns.md` §5 (script vs trigger permissions) |
| `Resource not found: f/...` | `wmill.getResource()` path wrong or resource deleted. | `windmill-debug` classify table |
| `Variable not found: f/platform_secrets/...` | Secret reference broken. | `docs/patterns.md` §5 (Two flavors of secret storage) |
| `WM_PERMISSIONED_AS missing / wrong` | Script ran as wrong principal. Scripts can't elevate. | `docs/patterns.md` §5 (script vs trigger permissions) + `skills/triggers/SKILL.md` |
| `User: arn:aws:sts::...:assumed-role/hallow-platform-ec2/... is not authorized to perform: s3:ListBucket` | Step ran on `default` worker; needs `tag: fargate`. Real error usually MASKED — see cyclic-structures entry. | `docs/patterns.md` §8b + `skills/write-flow/SKILL.md` (Hallow gotchas) |

## Code-level errors

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| `TypeError`, `ReferenceError`, `SyntaxError` | Real code bug. | `windmill-debug` classify table |
| `wmill.createToken is not a function` / `TypeError: (void 0) is not a function` | `createToken` doesn't exist on top-level windmill-client. Use `process.env.WM_TOKEN`. | `skills/write-script-bun/SKILL.md` (Hallow gotchas) |
| `TypeError: JSON.stringify cannot serialize cyclic structures` at `wrapper.mjs writeFile(result.json)` (or `wrapper.mjs:60`) | **Masked AWS SDK error** — the SDK error object is cyclic and crashes the result writer. Two underlying causes: (a) `AccessDenied` because the step ran on the wrong worker (missing `tag: fargate`); (b) a genuine S3 error (`NoSuchKey`, bad key) thrown raw. Fix: catch + rethrow `new Error(\`${e.name}: ${e.message}\`)` to unmask, then address the real cause. | `docs/patterns.md` §8b + `skills/write-flow/SKILL.md` (Hallow gotchas) + `skills/write-script-bun/SKILL.md` (Hallow gotchas: rethrow pattern) |
| `TypeError: undefined is not an object (evaluating 'headers["..."]')` at preprocessor step | HTTP-trigger preprocessor declared positional params `(body, headers, query)`. It takes ONE `event` object — separate params are all `undefined`. | `skills/write-flow/SKILL.md` (Hallow gotchas: preprocessor signature) + `docs/patterns.md` §8 |
| `Error: wmill is not defined` at flow runtime (in an input_transform) | `input_transforms[].expr` runs in a QuickJS isolate with no wmill SDK. Inject via `static: $res:/$var:` instead. | `skills/write-flow/SKILL.md` (Hallow gotchas: input_transforms isolate) + `docs/patterns.md` §6 rule 11 |
| `EACCES` / `ENOENT` on local filesystem | Workers don't have that filesystem. Use S3. | `windmill-debug` classify table |

## Dispatch / routing

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| `runScriptAsync` returned `queued: true` but nothing ever ran | Called `runScriptAsync` against a FLOW path. Silent no-op. | `skills/write-script-bun/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §6 rule 6 |
| `HTTP trigger: 404` from caller / `Trigger not found at name /w/<ws>/<route>` | Two causes: (a) caller's URL has extra `w/` — should be `/api/r/<ws>/<route>`, no `w/`. (b) caller's identity can't READ trigger's folder (ACL gates lookup, even with `authentication_method: windmill`). | `skills/triggers/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §8 |
| Slack post failed: `not_in_channel` | Bot not invited. | `windmill-debug` classify table |
| Slack post failed: `channel_not_found` | Wrong channel ref. Use ID, not `#name`. | `windmill-debug` classify table |

## Sync / push / lineage

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| `Bad request: A script with hash <H> with same parent_hash has been found. However, the lineage must be linear` | Script moved/renamed, old path still active. Single-child lineage enforced. | `skills/windmill-debug/SKILL.md` classify table |
| `wmill sync` lists live schedule/trigger as `-` delete (no paired `+`) | Filename uses `.` between segments — derives a different entity path than server's `_`. Push would DESTROY the live entity. | `docs/patterns.md` §8c + `skills/schedules/SKILL.md` + `skills/triggers/SKILL.md` |
| Schedule/trigger shows perpetual `~ modify` on every sync diff | Missing one of `cron_version: v2`, `ws_error_handler_muted: false`, `email`. Or `email` was set to non-deployer value (server stamps to pusher every push). | `skills/schedules/SKILL.md` (Hallow gotchas: "Three fields" + "deployer-stamped email") |
| `flow.yaml` push reports "Done, N changes" but next dry-run shows IDENTICAL N changes | Hand-renamed inline-script files (`*.inline_script.ts`). Names are wmill-derived from step content/summary; server stores code embedded. Rename is non-convergent. | `skills/write-flow/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §6 rule 8 |
| Dry-run shows unexpected `+ group` / `~ folder` for a workspace you didn't intend | `wmill sync` uses ACTIVE workspace (`wmill workspace`), not cwd. Pass `--workspace <ws>` explicitly. | `skills/cli-commands/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §7 |

## Schema / API quirks

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| HTTP trigger created via API comes back `mode: "enabled"` despite local YAML `disabled` | `POST /http_triggers/create` ignores enabled/mode. No `setenabled` endpoint (404 unlike schedules). | `skills/triggers/SKILL.md` (Hallow gotchas: "Create-then-update") + `skills/triggers/references/http.md` |
| `POST /http_triggers/create` → `422 missing field is_static_website` | Create payload requires `is_static_website` + `static_asset_config` keys present. | `skills/triggers/references/http.md` |
| Resource `resource_type` change pushed cleanly but server still shows old type | `resource_type` is IMMUTABLE. Push silently keeps original type (only updates value/description). | `skills/resources/SKILL.md` (Hallow gotchas: delete-recreate procedure) + `docs/patterns.md` §7 |
| `wmill resource-type get apify` → "Available only on Windmill Cloud" | OAuth-only Cloud RT. Use `apify_api_key` sibling on self-host. | `skills/resources/SKILL.md` (Hallow gotchas) |
| Created variable at resource path conflicts with auto-linked one | Resource secret-typed fields auto-create a linked variable at the resource's own path. Don't pre-create. | `skills/resources/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §5 |
| HTTP trigger main() got `headers: undefined` | HTTP triggers have no `args` field. Headers exposed only via `event` in `export async function preprocessor(event)`. | `skills/triggers/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §8 |
| Flow inline script: `import {...} from "./sibling.ts"` fails at runtime | Inline modules bundled independently — no cross-inline relative imports. | `skills/write-flow/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §6 rule 7 |
| `Unexpected identifier "Created"` parsing a `folders/create` or `folders/update` response | Those endpoints return a BARE STRING, not JSON. Only `JSON.parse` if the body starts with `{`/`[`. | `skills/cli-commands/SKILL.md` (Hallow gotchas: folders return bare strings) |
| `folders/update` → `400 invalid state: owner would not have permission` | `folders/update` is PUT semantics — omitting `owners` empties them. Always send the current `owners` array. | `skills/cli-commands/SKILL.md` (Hallow gotchas: folders/update PUT) |
| `DELETE /apps/delete/p/<path>` returns `200 "app deleted"` but the app persists | Generic delete only removes low-code apps, NOT `raw_app: true` apps. `apps/exists` also unreliable. Authoritative check = `apps/list`; delete via UI. | `skills/raw-app/SKILL.md` (Hallow gotchas: deleting a raw app) |
| `wmill flow push` exits 0 but preprocessor changes don't take effect | `flow push` doesn't update `preprocessor_module`. Patch via API: GET → edit `value.preprocessor_module.value.content` → POST `/flows/update/<path>` (with `path` in body). | `skills/write-flow/SKILL.md` (Hallow gotchas: flow push doesn't update preprocessor) |

## Cron / scheduling

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| `Invalid range for Days of Week: 0-4` | Cron parser rejects numeric DOW ranges. Use names: `SUN-THU`. | `skills/schedules/SKILL.md` (Hallow gotchas: "Cron parser rejects numeric DOW ranges") |
| Scheduled job didn't fire | Confirm schedule exists + `enabled: true`. Check `job list --script-path <path>` for any runs. | `windmill-debug` classify table + `skills/schedules/SKILL.md` |

## DuckDB / DuckLake

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| DuckDB `COPY ... TO '$path'` or `ATTACH '$path'` rejects bind param | `$name` rejected in literal-only positions. Use `%%name%%` substitution (or python3 + `import duckdb`). | `skills/write-script-duckdb/SKILL.md` (Hallow gotchas) |
| DuckDB `%%name%%` rejects bucket name with dash | `%%name%%` hard-gated to `[A-Za-z_][A-Za-z0-9_]*`. Switch to python3 + `import duckdb`. | `skills/write-script-duckdb/SKILL.md` (Hallow gotchas) |
| `ATTACH 'ducklake:s3://.../catalog.ducklake' (CREATE_IF_NOT_EXISTS true)` → "database does not exist" | DuckLake catalog can't live on S3 (S3 not random-write). Use Postgres/MySQL catalog. | `skills/write-script-duckdb/SKILL.md` (Hallow gotchas) |

## Tooling / CLI

| Symptom | Diagnosis | Fix-source |
|---|---|---|
| `wmill generate-metadata` says "no scripts found" on a hand-written file | Must `wmill script bootstrap <path> <lang>` first, then overwrite, then generate-metadata. | `skills/cli-commands/SKILL.md` (Hallow gotchas) |
| `wmill script preview` of an S3 script intermittently succeeds/fails | Preview ignores tag routing — nondeterministic worker selection. Validate via flow with `tag: fargate`. | `skills/cli-commands/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §8b |
| `Job timed out` | Default per-job timeout hit (often 30s). Bump `timeout:` in `.script.yaml` or `flow.yaml` step. | `windmill-debug` classify table |
| Fargate queue stalled — many jobs `queued`, none progressing; logs of one job froze after `--- BUN CODE EXECUTION ---` with no output/error | A timeout-less job hung inside user code and pinned the single thin Fargate worker (concurrency 1). Worker keeps heartbeating so no reaper fires. Cancel the hung job; ALWAYS set an explicit `timeout`. | `docs/patterns.md` §7 ("Always set an explicit timeout") |
| `429 Too Many Requests` | Downstream service rate-limit. Backoff/cache. | `windmill-debug` classify table |

## How to grow this index

When a new gotcha lands in `WINDMILL_LEARNINGS.md` and gets promoted to a skill/doc, add one row here pointing at the new fix-source. Quote the error verbatim in column 1 — paraphrasing breaks searchability.
