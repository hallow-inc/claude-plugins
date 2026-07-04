---
name: write-flow
description: >-
  Use when creating a Windmill **flow** — multi-step orchestration of scripts via `flow.yaml`, with branches, loops, suspends, approvals, error handlers. Triggers on "build a flow", "chain scripts", "windmill workflow", `flow.yaml`, "tag fargate", "S3 step in flow", "AccessDenied cyclic structures", "early_return HTML", "inline script import", "rename inline script", "flow sync up to date but diff persists". Covers flow scaffolding (`wmill flow new`), preprocessor/failure modules, loops, branches, approvals, sync HTTP-trigger response shaping via `early_return`, sandbox S3 steps MUST set `tag: fargate` (Fargate task role has the IAM grant; EC2 default worker does not — symptom is a masked cyclic-structure error), no cross-inline relative imports, wmill-owned inline-script filenames (hand-rename is non-convergent). NOT for: single-script work (use write-script-* skill), code-defined workflows (use write-workflow-as-code).
---

# Windmill Flow CLI Guide

## Creating a Flow

**You — the AI agent — scaffold the flow yourself by running `wmill flow new <path>` with the right flags. Do NOT hand-create the folder + `flow.yaml`, and do NOT tell the user to "run `wmill flow new` and follow the prompts".**

`wmill flow new` creates the folder with the correct suffix (`__flow` or `.flow` depending on the workspace's `nonDottedPaths` setting), writes a minimal `flow.yaml` shell, and prints Claude-specific next-step hints. Scaffolding by hand skips all of that and often picks the wrong suffix.

### Step 1 — Gather path + summary by asking the user

You need two things:

1. **path** — the windmill path, e.g. `f/folder/my_flow` or `u/username/my_flow`.
2. **summary** — a short description of the flow.

If the user's request didn't supply both, ask for both in a single round-trip. Use whichever interactive question facility your runtime provides — a structured multi-choice tool if available, otherwise plain chat — and provide one or two example values for each (with an "Other" / free-form fallback). Do not guess paths or summaries.

### Step 2 — Run the command yourself

```bash
wmill flow new f/folder/my_flow --summary "Short description"
```

Add `--description "..."` when the user provided a longer explanation worth preserving separately from the summary.

### Step 3 — Fill in `flow.yaml`

Open the generated `flow.yaml` (under the folder the command just created) and replace the empty `value.modules` + `schema` with the real flow definition.

For rawscript modules, use `!inline path/to/script.ts` for the content key. Inline script files should NOT include `.inline_script.` in their names (e.g. use `a.ts`, not `a.inline_script.ts`).

Once the flow has real content, **offer** to open the visual preview as a one-sentence next step (e.g. "Want me to open the visual preview?"). Don't auto-open — opening the dev page has side effects (browser window, possibly a `launch.json` entry) and the user should consent.

### Anti-patterns to avoid

- ❌ Hand-creating the `__flow` folder + `flow.yaml` instead of running `wmill flow new`. You'll miss the suffix-setting resolution, the default shape, and the Claude hints.
- ❌ Telling the user to "run `wmill flow new <path>`" — you can and should run it yourself.
- ❌ Inventing a path/summary instead of asking the user.

## CLI lifecycle

Flow equivalents: `wmill flow preview <path>` (local, default), `wmill flow run <path>` (deployed). Add `--remote` to preview against deployed scripts instead of local files for PathScript steps. Full preview/run/push decision tree (and Hallow `wmill sync` ban): see `${CLAUDE_PLUGIN_ROOT}/skills/cli-commands/references/preview-vs-run.md`.

For visual graph + live reload of the flow, use the `preview` skill — offer first, do not open automatically.


# Windmill Flow Building Guide

## OpenFlow Schema

The OpenFlow schema (openflow.openapi.yaml) is the source of truth for flow structure. Refer to OPENFLOW_SCHEMA for the complete type definitions.

## Reserved Module IDs

- `failure` - Reserved for failure handler module
- `preprocessor` - Reserved for preprocessor module
- `Input` - Reserved for flow input reference

## Hard Structural Rules

These are strict Windmill schema rules. Follow them exactly.

- `value.modules` is only for normal sequential steps
- `value.preprocessor_module` and `value.failure_module` are special top-level fields inside `value`, not entries in `value.modules`
- If a flow needs a preprocessor, create `value.preprocessor_module` with `id: preprocessor`
- If a flow needs a failure handler, create `value.failure_module` with `id: failure`
- Do NOT create regular modules inside `value.modules` named `preprocessor` or `failure`
- `preprocessor_module` and `failure_module` only support `script` or `rawscript`
- `preprocessor_module` runs before normal modules and cannot reference `results.*`
- `failure_module` can use the `error` object with `error.message`, `error.step_id`, `error.name`, and `error.stack`

Correct shape:

```yaml
value:
  preprocessor_module:
    id: preprocessor
    value:
      type: rawscript
      ...
  failure_module:
    id: failure
    value:
      type: rawscript
      ...
  modules:
    - id: process_event
      value:
        type: rawscript
        ...
```

Incorrect shape:

```yaml
value:
  modules:
    - id: preprocessor
      ...
    - id: process_event
      ...
    - id: failure
      ...
```

## Module ID Rules

- Must be unique across the entire flow
- Use underscores, not spaces (e.g., `fetch_data` not `fetch data`)
- Use descriptive names that reflect the step's purpose

## Common Mistakes to Avoid

- Missing `input_transforms` - Rawscript parameters won't receive values without them
- Referencing future steps - `results.step_id` only works for steps that execute before the current one
- Duplicate module IDs - Each module ID must be unique in the flow

## Data Flow Between Steps

- `flow_input.property` - Access flow input parameters
- `results.step_id` - Access output from a previous step only when that step result is in scope
- `results.step_id.property` - Access specific property from a previous step output only when that step result is in scope
- `flow_input.iter.value` - Current iteration value when inside a loop (`forloopflow` or `whileloopflow`)
- `flow_input.iter.index` - Current loop index when inside a loop (`forloopflow` or `whileloopflow`)

## Loop Structure Rules

- For `whileloopflow`, use module-level `stop_after_if` on the loop module itself when the loop should stop after an iteration result
- Do NOT put `stop_after_if` inside `value` of a `whileloopflow`
- `stop_after_all_iters_if` is for checks after the whole loop finishes, not the normal per-iteration break condition
- When a `whileloopflow` carries state forward between iterations, use `flow_input.iter.value` as the current loop value and provide an explicit first-iteration fallback when needed
- Use `flow_input.iter.index` only when the loop logic is truly based on the iteration index, not as a replacement for the current loop value
- If the user asks for a final scalar/object after a loop, add a normal step after the loop that extracts the final value from the loop result instead of returning the whole loop result array

Correct `whileloopflow` shape:

```yaml
- id: loop_until_done
  stop_after_if:
    expr: result.done === true
    skip_if_stopped: false
  value:
    type: whileloopflow
    skip_failures: false
    modules:
      - id: advance_state
        value:
          type: rawscript
          input_transforms:
            state:
              type: javascript
              expr: flow_input.iter && flow_input.iter.value !== undefined ? flow_input.iter.value : flow_input.initial_state
- id: return_final_state
  value:
    type: rawscript
    input_transforms:
      final_state:
        type: javascript
        expr: results.loop_until_done[results.loop_until_done.length - 1]
```

Incorrect `whileloopflow` patterns:

```yaml
- id: loop_until_done
  value:
    type: whileloopflow
    stop_after_if:
      expr: result.done === true
```

```yaml
input_transforms:
  state:
    type: javascript
    expr: flow_input.iter.index
```

```yaml
input_transforms:
  final_state:
    type: javascript
    expr: results.loop_until_done
```

## Approval / Suspend Structure

- `suspend` belongs on the flow module object itself, as a sibling of `id` and `value`
- Never put `suspend` inside `value`

Correct shape:

```yaml
- id: request_approval
  suspend:
    required_events: 1
    resume_form:
      schema:
        type: object
        properties:
          comment:
            type: string
        required: [comment]
  value:
    type: identity
```

Incorrect shape:

```yaml
- id: request_approval
  value:
    type: rawscript
    suspend:
      required_events: 1
```

## Branch Result Scope Rules

- Inside a branch, you may reference earlier outer steps and earlier steps in the same branch
- Outside a `branchone`, do NOT reference ids of steps that only exist inside its branches or default branch. Use `results.<branchone_module_id>` instead
- Outside a `branchall`, do NOT reference ids of steps inside its branches. Use `results.<branchall_module_id>` instead
- If downstream steps need a stable shape after a branch, make each branch return the same fields
- When needed, add a normalization step immediately after the branch and consume `results.<branch_module_id>` there

Correct after `branchone`:

```yaml
- id: route_order
  value:
    type: branchone
    ...
- id: send_confirmation
  value:
    input_transforms:
      routed:
        type: javascript
        expr: results.route_order
```

Incorrect after `branchone`:

```yaml
expr: results.create_shipment
expr: results.create_backorder
```

Correct after `branchall`:

```yaml
- id: enrich_parallel
  value:
    type: branchall
    parallel: true
    ...
- id: combine_data
  value:
    input_transforms:
      enrichments:
        type: javascript
        expr: results.enrich_parallel
```

## Input Transforms

Every rawscript module needs `input_transforms` to map function parameters to values:

Static transform (fixed value):
{"param_name": {"type": "static", "value": "fixed_string"}}

JavaScript transform (dynamic expression):
{"param_name": {"type": "javascript", "expr": "results.previous_step.data"}}

## Resource References

- For flow inputs: Use type `"object"` with format `"resource-{type}"` (e.g., `"resource-postgresql"`)
- For step inputs: Use static value `"$res:path/to/resource"`

## Final Structural Self-Check

Before finalizing a flow, verify:

- any preprocessor is in `value.preprocessor_module`
- any failure handler is in `value.failure_module`
- any approval step has module-level `suspend`
- no downstream step references inner branch step ids from outside the branch

## S3 Object Operations

Windmill provides built-in support for S3-compatible storage operations.

To accept an S3 object as flow input:

```json
{
  "type": "object",
  "properties": {
    "file": {
      "type": "object",
      "format": "resource-s3_object",
      "description": "File to process"
    }
  }
}
```

## Using Resources in Flows

On Windmill, credentials and configuration are stored in resources. Resource types define the format of the resource.

### As Flow Input

In the flow schema, set the property type to `"object"` with format `"resource-{type}"`:

```json
{
  "type": "object",
  "properties": {
    "database": {
      "type": "object",
      "format": "resource-postgresql",
      "description": "Database connection"
    }
  }
}
```

### As Step Input (Static Reference)

Reference a specific resource using `$res:` prefix:

```json
{
  "database": {
    "type": "static",
    "value": "$res:f/folder/my_database"
  }
}
```



## OpenFlow Schema (full reference)

Full OpenAPI JSON schema for OpenFlow at `${CLAUDE_PLUGIN_ROOT}/skills/write-flow/references/openflow-schema.md`. Read on demand when you need exact field shapes for: FlowValue, FlowModule, RawScript, PathScript, ForloopFlow, WhileloopFlow, BranchOne, BranchAll, AiAgent, AgentTool, InputTransform, Retry, suspend, etc.

## Hallow gotchas (flows)

### No relative imports between inline scripts in the same flow

Inline rawscript files inside a `.flow/` dir are bundled independently per module. You CANNOT `import { x } from "./other.ts"` from one inline module into another — the sibling file is not packaged. The flow will fail at runtime with a module-resolution error.

Fold shared helpers and constants into the single module that uses them, or promote the helper to a real script (`f/<folder>/<helper>.ts`) and reference it via `PathScript` instead of an inline.

### Inline-script filenames are wmill-derived — hand-renaming is non-convergent

The `*.inline_script.ts` filenames inside a `.flow/` dir are NAMED BY WMILL from each step's content/summary (often long descriptive strings). Server-side, the flow value EMBEDS the inline code directly — there is NO filename field stored server-side. The local files + `!inline <name>` pointers are a CLI-local serialization detail.

**Consequence:** hand-renaming `*.inline_script.ts` files (long → short, etc.) changes only local files. `sync push` reports "flow up to date" (true — the CODE is unchanged) but the file-level differ keeps listing the rename forever. `sync pull` REVERSES the rename and restores wmill's canonical names.

**To actually shorten an inline-script filename**, change the step's `id` or `summary` so wmill re-derives the name. Don't rename the file by hand.

### Return HTML/text from a sync HTTP-trigger flow via `early_return`

A `request_type: sync` HTTP-trigger flow defaults to a JSON-wrapped module result for the response body. To respond with a raw string (e.g. an HTML page), set flow-level `early_return` to the expression whose value should be the response body:

```yaml
value:
  modules:
    - id: register
      value:
        type: rawscript
        # returns { status, html } from inline script
  early_return: results.register.html
```

The `early_return` expression value becomes the response body verbatim. **Content-Type caveat:** setting actual `text/html` requires additional trigger/app config; `early_return` only controls the body. Confirm via curl against the trigger URL before declaring done.

### Any step touching the sandbox S3 bucket MUST set `tag: fargate`

Steps that read/write `s3:///` (the Hallow sandbox bucket via `f/storage/sandbox_data`) MUST be pinned to the `fargate` worker group via `tag: fargate` on the module. The `default` (EC2/Coolify-hosted) worker runs as the EC2 instance role, which has NO IAM grant to the sandbox bucket — only the Fargate worker task role does (per `infra/pulumi/windmill/component.go`).

**Symptom of a missing `tag: fargate`:** the failing step returns a **masked** error — `"TypeError: JSON.stringify cannot serialize cyclic structures"` from the Windmill bun wrapper (the AWS AccessDenied error object is cyclic). The real error is `"User: arn:aws:sts::...:assumed-role/hallow-platform-ec2/... is not authorized to perform: s3:ListBucket"`.

**To see the real cause when debugging**, wrap the S3 call: `catch (e) { return \`${e.name}: ${e.message}\`; }`.

**`wmill script preview` ignores tag routing** (no `--tag` flag) and routes nondeterministically — validate S3 steps via a flow with `tag: fargate`, not bare script preview.

Module shape:

```yaml
- id: write_to_s3
  tag: fargate
  value:
    type: rawscript
    language: bun
    ...
```

Full architectural context (Pulumi grant source, worker-group table, audit guidance for already-deployed-without-tag flows): see `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §8b.

### DuckLake steps — `tag: fargate` + Python + lib (NOT DuckDB kind)

Any flow module reading or writing the shared DuckLake (`dl.<schema>.<table>`) follows the same `tag: fargate` rule PLUS extra constraints:

| Rule | Why |
|---|---|
| Module `tag: fargate` | Catalog resource targets `127.0.0.1:5435` (tsforwarder sidecar in Fargate task netns) — default worker can't reach it |
| Step language: `python3` | DuckDB script kind's parameter binding rejects S3 paths — see patterns.md §9b rule 1 |
| Inline code uses `from f.platform.ducklake.lib import connect` | Pre-built ATTACH/secret/extension setup |
| `db:` param resource | `f/shared/ducklake_catalog_ro` (read), `f/<dept>/ducklake_catalog` (dept write), `f/platform/ducklake/catalog_pg` (admin) |
| Don't call `CHECKPOINT` or `CALL ducklake_*` inline | `f/platform/ducklake/maintain` runs CHECKPOINT daily; inline competes with concurrent writers |

Full ruleset (10 items, schema/lake rules, discovery snippets): `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §9b.

### Inline step `summary:` is slugified into the filename — keep it 2-4 words

Windmill names each inline-script file (`!inline <name>.inline_script.{ts,lock}`) by **slugifying the step's `summary:`**. The server applies the slug on push; a later `wmill sync pull` rewrites local `flow.yaml` `!inline` refs to whatever the current server slug is.

A long/verbose summary produces an ugly filename full of `+`, parens, and spaces — and it never converges: `summary: "Verify HMAC + timestamp window (replay protection)"` becomes `verify_hmac_+_timestamp_window_(replay_protection).inline_script.ts`, and every push/pull round-trip flips the filename and the `!inline` ref back.

**Rule:** inline step `summary:` must be 2-4 words, snake-friendly, no symbols, no parens (e.g. `summary: verify_signature`). Put the long prose in `description:` — that field is NOT slugified. This is the *cause* of the wmill-derived-filename non-convergence documented above; controlling the summary is how you make filenames both short and stable.

**Convergence check:** after authoring, both `wmill sync push --dry-run` and `wmill sync pull --dry-run` must read `0 changes`. (Hallow bans actual `wmill sync` — dry-run only, for convergence verification.)

### `input_transforms[].expr` runs in a QuickJS isolate with NO wmill SDK

A step's `input_transforms.<arg>.expr` is evaluated in a sandboxed QuickJS isolate that does **not** have the wmill SDK. Any `await wmill.getResource("...")` / `await wmill.getVariable("...")` inside an `expr` throws `Error: wmill is not defined` at flow runtime.

Allowed in `expr`: `flow_input.X`, `results.<step>.X`, plain JS, JSON, string interpolation, ternaries, arithmetic. Nothing that needs the SDK.

To inject a resource or variable into a step, use a `static` transform with the `$res:` / `$var:` prefix — the whole object is passed in, and the script body (which DOES have the SDK, but won't need it) unpacks the fields:

```yaml
# FAILS at runtime — wmill is not defined in the isolate
input_transforms:
  supabase_anon_key:
    type: javascript
    expr: (await wmill.getResource("f/area/supabase")).anon_key

# CORRECT — inject the resource statically, unpack in the script body
input_transforms:
  supabase:
    type: static
    value: $res:f/area/supabase
  my_var:
    type: static
    value: $var:f/platform_secrets/some__secret
```

If a flow genuinely needs a *derived* value from a resource at flow level, compute it inside an earlier script step and read it via `results.<step>.<field>` in the later `expr`.

### HTTP-trigger preprocessor takes ONE `event` arg — not separate body/headers/query

A flow's (or script's) HTTP-trigger `preprocessor` is invoked with a **single object** `event`, not positional `(body, raw_string, query, headers)` params. Declaring separate params makes every one of them `undefined` — symptom: `TypeError: undefined is not an object (evaluating 'headers["x-webhook-secret"]')` at the preprocessor step.

Correct signature (verified against `f/webhooks/slack/events`):

```ts
export async function preprocessor(event: {
  kind: "http";
  body: unknown;
  raw_string: string | null;
  route: string;
  path: string;
  method: string;
  params: Record<string, string>;
  query: Record<string, string>;
  headers: Record<string, string>;
}): Promise<{ /* reshaped args for main(), matched by name */ }> {
  const secret = event.headers["x-webhook-secret"];
  // ...
}
```

The preprocessor's return value becomes `main()`'s arg set (matched by name). See `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §8 for *why* a preprocessor is required (HTTP triggers have no `args` field).

### `wmill flow push` does NOT update `preprocessor_module` — patch via API

Pushing a flow (or `wmill script push <flow>/preprocessor.ts`) does NOT update the flow's `preprocessor_module` on the server — the push succeeds (exit 0) silently while the old preprocessor content stays live. To actually update it: GET the flow, patch `value.preprocessor_module.value.content`, then POST the full payload back:

```bash
# GET current flow value
curl -s "$BASE_URL/api/w/$WS/flows/get/p/f/<folder>/<flow>" -H "Authorization: Bearer $WM_TOKEN" > flow.json
# patch value.preprocessor_module.value.content in flow.json (jq/editor), then:
curl -s -X POST "$BASE_URL/api/w/$WS/flows/update/f/<folder>/<flow>" \
  -H "Authorization: Bearer $WM_TOKEN" -H 'Content-Type: application/json' \
  -d @flow.json   # `path` field REQUIRED in body or 422
```

The `path` field must be present in the update body or the call 422s. (At Hallow, prefer the MCP `windmill` flow tools over raw curl when they cover the operation.)

### Always set an explicit `timeout` (lower is better)

Every flow MUST set an explicit `timeout`. A timeout-less hang on a `tag: fargate` step pins the Fargate worker (concurrency 1, thin autoscale) and blocks the entire shared queue — and nothing else catches it (the worker keeps heartbeating). Pick the smallest value that comfortably covers the expected runtime (fast glue 60–120s, query 300–600s, heavy 900–1800s). Full rationale + the instance-default-vs-per-entity relationship: `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §7 ("Always set an explicit timeout").

### Many concurrent multi-step flows can deadlock

Same worker-pinning model as §timeout above, one level up. A worker runs **one job at a time**. A multi-step flow pins a worker for its **parent-flow job** for the whole run, while its **step jobs** also need workers. If the parent and steps draw from the same pool, then at enough concurrent runs every worker is holding a parent that is waiting for a step worker that can never free — a hold-and-wait deadlock.

- **Symptom:** launch N of the same multi-step flow at once → they all start, none finish. One at a time is fine; a nightly single-flow-over-many-items is fine. Only a *fan-out of N whole flows* wedges.
- **Two fixes you can apply as the author:**
  1. Set a flow-level **`concurrency_limit`** so no more parents run at once than the pool can also serve steps for.
  2. **Collapse thin steps into one script.** One job → one worker → deadlock is structurally impossible. This is the durable fix; prefer it when the steps are just glue. (A step that writes S3 keeps `tag: fargate`.)

(How the platform mitigates this at the infra level — a dedicated flow-orchestration worker pool — is an admin concern, not something a flow author configures.)
