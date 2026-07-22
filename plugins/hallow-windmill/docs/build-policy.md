# Windmill build-policy — the pre-push ruleset

The single source of truth for the **reviewable rules a Windmill entity must satisfy before it is pushed** to the Hallow `dev` workspace. Distilled from the authoring/debug skills and the resolved `WINDMILL_LEARNINGS.md` entries so a rule fires reliably instead of only when the authoring skill happens to recall it inline.

**Audience:** the `windmill-build-reviewer` agent reads this and checks a locally-authored entity against it. Humans and the authoring skills cite it too. Each rule has a stable id (`GATE`, `GEN-n`, `SCRIPT-n`, `FLOW-n`, `TRIG-n`, `SCHED-n`, `RES-n`, `APP-n`) so a finding can point at the exact rule.

**Scope:** this doc lists only what is checkable by inspecting entity files *before push*. It is NOT the debug catalog (runtime symptom → fix lives in `skills/windmill-debug/`), and it is NOT a capability list (what the fork enables lives in the authoring skills' "Hallow gotchas" / availability sections).

---

## GATE — Review before push

**GATE.1 — No entity is pushed before a build-policy review passes.**
Any skill or agent that pushes a script, flow, trigger, schedule, resource, or app to the workspace SHALL first have the authored files reviewed against this doc (route to `windmill-build-reviewer`, or check inline against these rules). A clean review → push. A finding → fix, re-review, then push. This gate is the rule of record; authoring skills carry only a thin pointer to it, they do not restate it.

---

## GEN — Always applies (every entity)

**GEN.1 — Never `wmill sync push` / `wmill sync pull`.** Banned Hallow-wide — they delete server state absent from local files and clobber secret variables. Push via the `mcp__windmill__*` tools or the Windmill UI. (`wmill sync … --dry-run` is allowed for convergence checks only.)

**GEN.2 — No token / secret literal committed to any file.** No bearer token, API key, or password in an entity file, YAML, or inline script. Secrets come from resources / `f/platform_secrets/` variables, read at runtime.

**GEN.3 — `wmill` CLI commands that touch the remote pass `--workspace dev`.** The CLI resolves the target from the *active* workspace, not the directory. An un-`--workspace`'d command can hit the wrong workspace.

**GEN.4 — Multi-segment entity filenames join with `_`, never `.`.** An entity at `f/foo/bar_baz_v2` lives on disk as `bar_baz_v2.<kind>.yaml`. A dotted stem (`bar_baz.v2.<kind>.yaml`) derives a *different* server path — the live entity then shows as an unpaired delete and a push would destroy it. Applies to triggers, schedules, resources, scripts, flows.

**GEN.5 — One tool, one job.** If the authored thing can't be described in one sentence without "and", it's too big — it should have been narrowed before building (product-shaped asks are not Windmill entities). This is a scope check, not a syntax check; flag only obvious multi-purpose sprawl.

---

## SCRIPT — Scripts (any language)

**SCRIPT.1 — Explicit `timeout:` set in `*.script.yaml` (lower is better).** Never ship a timeout-less script. A hang on a `tag: fargate` script pins the single-slot Fargate worker and wedges the whole shared queue; the job's own timeout is the only bound (the stuck worker keeps heartbeating, so no reaper fires). Pick the smallest value covering expected runtime — fast API/Slack glue 60–120s, warehouse query 300–600s, heavy pipeline 900–1800s.

**SCRIPT.2 — Dispatch fn matches target kind: `runScriptAsync` for scripts, `runFlowAsync` for flows.** The dispatch fns are NOT polymorphic. `runScriptAsync(<flow-path>)` returns a job id + `queued: true` but **nothing runs** — silently dropped. Check `wmill flow list` vs `wmill script list` (or MCP equivalents) before choosing. `taskScript`/`taskFlow` are workflow-as-code only.

**SCRIPT.3 — Don't call `wmill.createToken`; use `process.env.WM_TOKEN`.** The top-level `windmill-client` namespace has no `createToken` (`TypeError: (void 0) is not a function`). Workers get a caller-scoped token injected as `WM_TOKEN` — read that for any `fetch` against `${BASE_URL}/api/...`.

**SCRIPT.4 — Wrap raw AWS SDK calls; rethrow a plain-string `Error`.** A raw AWS SDK error thrown out of a bun script crashes the result writer (`JSON.stringify cannot serialize cyclic structures`) and MASKS the real S3 cause (`NoSuchKey` / `AccessDenied`). Wrap: `catch (e:any) { throw new Error(\`${e.name}: ${e.message}\`) }`. Prefer the shared atoms `f/storage/s3_read_json` / `s3_write_json` over raw SDK use.

**SCRIPT.5 — Non-trivial DuckDB work is python3 + `import duckdb`, not the `duckdb` script kind.** User policy: *DuckDB should be Python*. The `duckdb` script kind's parameter modes both reject S3 paths — `$name` bind params are rejected in literal-only positions (`COPY … TO`, `ATTACH '…'`); `%%name%%` substitution is gated to `[A-Za-z_][A-Za-z0-9_]*`, so a bucket name with a dash is rejected. Reserve the `duckdb` kind for simple SQL-only analytics whose params fit its model.

**SCRIPT.6 — DuckLake catalog is Postgres/MySQL, never an S3 file.** `ATTACH 'ducklake:s3://…'` fails (`database does not exist`) — S3 is not random-write. Use a Postgres/MySQL catalog (durable, multi-worker-safe); a local DuckDB file only survives a single Fargate task.

**SCRIPT.7 — No filesystem I/O to local paths.** Windmill workers have no durable local filesystem; a `read/write` to a local path fails (`EACCES`/`ENOENT`) or is lost. Use S3 helpers (`f/storage/s3_*`) instead.

---

## FLOW — Flows

**FLOW.1 — Any step touching the sandbox S3 bucket sets `tag: fargate` on the MODULE.** A step that reads/writes `s3:///` (the sandbox bucket via `f/storage/sandbox_data`) MUST be pinned with module-level `tag: fargate`. The `default` (EC2) worker runs as the EC2 instance role with NO S3 IAM grant; only the Fargate task role has it. Symptom of a miss is a **masked** `JSON.stringify cannot serialize cyclic structures` (cyclic AWS AccessDenied), not the real error. `wmill script preview` can't be tagged and routes nondeterministically — validate S3 steps via a `tag: fargate` flow.

**FLOW.2 — DuckLake steps: `tag: fargate` + `python3` + `f.platform.ducklake.lib`.** A module reading/writing `dl.<schema>.<table>` needs module `tag: fargate` (catalog lives at the Fargate-netns sidecar `127.0.0.1:5435`), step language `python3` (DuckDB kind rejects S3 paths — see SCRIPT.5), and `from f.platform.ducklake.lib import connect`. Don't call `CHECKPOINT` / `CALL ducklake_*` inline (the daily maintain job owns that).

**FLOW.3 — `preprocessor_module` / `failure_module` are top-level `value` fields, not entries in `value.modules`.** Preprocessor id = `preprocessor`, failure id = `failure`; both support only `script`/`rawscript`. A regular module named `preprocessor`/`failure` inside `value.modules` is wrong. `preprocessor_module` runs first and cannot reference `results.*`; `failure_module` gets the `error` object.

**FLOW.4 — Every rawscript step has `input_transforms` for its params.** Missing `input_transforms` → the param never receives a value at runtime.

**FLOW.5 — Steps reference only PRIOR results; module ids are unique.** `results.<step>` resolves only for steps that execute before the current one (no forward refs); no downstream step references an inner-branch step id from outside the branch; no duplicate module ids.

**FLOW.6 — `input_transforms[].expr` uses NO wmill SDK.** The `expr` runs in a QuickJS isolate with no SDK — `await wmill.getResource/getVariable` throws `wmill is not defined`. Allowed in `expr`: `flow_input.*`, `results.*`, plain JS. Inject a resource/variable via a `static` transform (`value: $res:<path>` / `$var:<path>`) and unpack in the script body.

**FLOW.7 — No relative imports between inline scripts in the same flow.** Inline rawscript files in a `.flow/` dir are bundled independently — `import { x } from "./other.ts"` across inlines fails at runtime. Fold shared helpers into the one module that uses them, or promote to a real `PathScript`.

**FLOW.8 — Inline step `summary:` is 2–4 words, snake-friendly, no symbols/parens.** Windmill slugifies `summary:` into the `!inline` filename; a verbose summary yields an ugly, non-convergent filename that flips on every push/pull. Put long prose in `description:` (not slugified). Hand-renaming the `.inline_script.*` file is non-convergent — change the `id`/`summary` instead.

**FLOW.9 — Explicit `timeout` set; consider `concurrency_limit` for fan-out.** Same worker-wedge rationale as SCRIPT.1 at the flow level. A fan-out of N whole multi-step flows can hold-and-wait deadlock (parent job pins a worker while its steps need workers from the same pool) — set a flow-level `concurrency_limit`, or collapse thin glue steps into one script (one job → deadlock structurally impossible).

**FLOW.10 — Sync HTTP-trigger flow returning a raw string uses `early_return`.** A `request_type: sync` flow defaults to a JSON-wrapped result; to return a raw string (e.g. HTML), set flow-level `early_return: results.<module>.<field>`. (Real `text/html` content-type still needs trigger/app config.)

---

## TRIG — HTTP / event triggers

**TRIG.1 — Header/query/path access requires a `preprocessor` taking one `event` arg.** `main()` never receives request headers even with `raw_string`/`resource-headers`. HTTP triggers have no `args` field; the preprocessor is invoked with a SINGLE object `event: { kind, body, raw_string, route, path, method, params, query, headers }` — separate positional params come back `undefined`. The preprocessor's return becomes `main()`'s args (matched by name). Fetch resources inside the preprocessor via `wmill.getResource`.

**TRIG.2 — Trigger lives in a folder its intended callers can READ.** Folder ACL gates route lookup — a caller who can't read the trigger's folder gets `Trigger not found …` (404) even with valid auth; `authentication_method: windmill` does NOT bypass folder ACL. Put the trigger in a caller-readable folder (typically `f/shared/` with `g/all: true`); keep the elevated `script_path` impl in an admin-only folder.

**TRIG.3 — Don't rely on setting `permissioned_as` in local YAML.** The server stamps `permissioned_as` from the identity of whoever pushes (deployer-stamped) and ignores the YAML value. If a specific run-as principal is needed, that user pushes it, or an admin runs `wmill trigger set-permissioned-as`. (Hallow canonical elevation principal is `u/sandbox`.)

**TRIG.4 — To start a trigger disabled, create-then-update.** `POST /http_triggers/create` ignores `enabled`/`mode` and always creates `mode: enabled`; there is no `setenabled` for HTTP triggers. Create, then `POST /http_triggers/update/<path>` with `mode: disabled`. Create also requires `is_static_website` + `static_asset_config` present or it 422s.

**TRIG.5 — Only SQS/WebSocket/Postgres-CDC/MQTT + HTTP are available; Kafka/NATS/GCP/Azure/email are EE-blocked.** Don't author a trigger of an unavailable kind. (SQS is enabled via a Hallow fork deviation; email-routing triggers were never enabled — see the triggers skill availability matrix.)

---

## SCHED — Schedules

**SCHED.1 — Include the three churn-preventing fields.** A `*.schedule.yaml` must carry `cron_version: v2`, `ws_error_handler_muted: false`, and `email` — omitting them makes every sync diff a perpetual `~ modify`.

**SCHED.2 — `email` is deployer-stamped; don't set an arbitrary value.** The server forces `email` to the pushing user's identity. A different value diverges and churns. A different run-as principal means that user pushes, or an admin runs `wmill schedule set-permissioned-as`.

**SCHED.3 — Cron day-of-week RANGES use names, not numbers.** The parser rejects numeric DOW ranges (`0-4` → `Invalid range for Days of Week`). Use `SUN-THU`. Single numeric DOW (`0`) and lists (`0,3,5`) are fine — only ranges need names. (6-field cron.)

---

## RES — Resources

**RES.1 — `resource_type` is immutable after creation.** A push reporting success on a `resource_type` change silently keeps the old type (only `value`/`description` update). To change type: DELETE via raw HTTP API with a workspace token, re-create with the new type, verify with `wmill resource get`. (`wmill resource delete` doesn't exist; MCP `deleteResource` is admins-only.)

**RES.2 — Don't manually create a variable at a resource's own path.** A secret-typed resource field auto-creates a backing variable at the resource path (`is_linked: true`); a hand-created variable there collides. Cross-domain shared secrets go to `f/platform_secrets/<domain>__<name>` (double underscore) instead.

**RES.3 — On self-host prefer the `*_api_key` RT over an OAuth-Cloud-only one.** `apify` is OAuth-Cloud-only — use `apify_api_key`. (gcal/gmail/gdrive/gsheets accept a token field and also support the fork's Connect flow — either is fine.)

---

## APP — Raw apps

**APP.1 — Don't hand-write a `wmill.ts` shim.** `import { backend } from './wmill'` resolves to a build-time virtual module; `wmill app dev` generates the types + runtime exports. `wmill generate-metadata` is a no-op without a `wmill.yaml`.

---

## Maintenance

New reviewable pre-push gotchas drain here from `WINDMILL_LEARNINGS.md` via `/hallow-windmill:wmill-drain-learnings` — give each a stable id in the right section. Runtime-only symptoms (things you can't see until a job runs) belong in `skills/windmill-debug/`, not here. Capability announcements (what the fork enables) belong in the authoring skills, not here.
