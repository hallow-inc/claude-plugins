---
name: triggers
description: Use when configuring a Windmill **trigger** — HTTP route, webhook, WebSocket, Postgres CDC, MQTT, or SQS entry point that invokes a script or flow on Hallow's customized-OSS Windmill. Triggers on "webhook in windmill", "http route", "sqs trigger", "websocket trigger", "postgres cdc", `.trigger.yaml`, "trigger 404 / not found", "trigger ACL", "permissioned_as", "headers in preprocessor", "create disabled trigger", "is_static_website required". Covers trigger types + availability matrix (SQS enabled via Hallow fork deviation; Kafka/NATS/GCP/Azure/email still EE-blocked), payload mapping, auth, retry, folder-ACL gating on route lookup, workspaced URL form (`/api/r/<ws>/<route>`), deployer-stamped `permissioned_as`, create-then-disable for staged rollout, multi-segment underscore filenames. NOT for: time-based runs (use schedules skill), Kafka/NATS/GCP/Azure/email triggers (EE-only, still not available on Hallow).
---

# Windmill Triggers (Hallow customized-OSS build)

Triggers let external events invoke scripts and flows. Hallow runs a **customized OSS fork** of Windmill — **HTTP/webhook** is primary; **WebSocket, Postgres CDC, MQTT** are OSS-native; **SQS** is enabled by a Hallow deviation. See "Trigger availability" below for the full matrix and what's still blocked. Webhook = HTTP trigger with `authentication_method: none`.

## File naming

`{path}.{trigger_type}_trigger.yaml`

- `u/user/webhook.http_trigger.yaml`
- `f/inbound/queue_events.sqs_trigger.yaml`

## Type → reference

| Trigger | File suffix | Status | Reference |
|---|---|---|---|
| HTTP / webhook | `*.http_trigger.yaml` | available (primary) | `references/http.md` |
| WebSocket | `*.websocket_trigger.yaml` | available (OSS-native) | Windmill UI / docs |
| Postgres CDC | `*.postgres_trigger.yaml` | available (OSS-native) | Windmill UI / docs |
| MQTT | `*.mqtt_trigger.yaml` | available (OSS-native) | Windmill UI / docs |
| SQS | `*.sqs_trigger.yaml` | available (Hallow deviation) | Windmill UI |
| Email-routing | `*.email_trigger.yaml` | **NOT available** (EE-gated; see warning) | `references/email.md` |

Read the matching reference at `${CLAUDE_PLUGIN_ROOT}/skills/triggers/references/<name>.md` before writing the YAML. Every trigger type also includes shared `retry` + `error_handler_path` — see `references/common-retry.md`.

## Hallow ban

`wmill sync push` and `wmill sync pull` are banned in this workspace. They delete server state not in local files and clobber secret variables. Mirror trigger changes via the `windmill` MCP tools or the Windmill UI — never `wmill sync`.

## Trigger availability on Hallow's customized OSS build

Hallow runs a **customized OSS fork** of Windmill (`hallow-inc/windmill`) that unlocks some Enterprise features. The matrix below reflects that build — it is NOT stock Windmill OSS, and it is NOT the same as the upstream EE/OSS split.

**Available:**

- HTTP routes / webhooks (`*.http_trigger.yaml`) — primary, best-supported. See `references/http.md`.
- WebSocket (`*.websocket_trigger.yaml`) — OSS-native (never EE-gated upstream).
- Postgres CDC (`*.postgres_trigger.yaml`) — OSS-native.
- MQTT (`*.mqtt_trigger.yaml`) — OSS-native.
- **SQS (`*.sqs_trigger.yaml`) — enabled by a Hallow fork deviation** (`deviation: implement OSS SQS triggers`). Two auth modes: static AWS credentials, or OIDC (Windmill mints an OIDC token → STS `AssumeRoleWithWebIdentity`). SQS is EE-only in stock OSS; on Hallow it works.

> WebSocket / Postgres CDC / MQTT are compiled into the build, but Hallow may not run a dedicated listener worker for each — **verify the listener actually fires before relying on one** (create the trigger, send a test event, check `wmill job list --script-path <path>`).

**Still Enterprise-only — NOT available** (backend `handler_oss` returns `"... not available in open source version"`):

- Kafka (`*.kafka_trigger.yaml`)
- NATS (`*.nats_trigger.yaml`)
- GCP Pub/Sub (`*.gcp_trigger.yaml`)
- Azure Event Grid (`*.azure_trigger.yaml`)
- **Email-routing trigger (`*.email_trigger.yaml`)** — the `local_part`-based inbound-mail feature documented in `references/email.md`. The EE `windmill-trigger-email` crate is gated and the frontend only fetches email triggers under an enterprise license. **`references/email.md` describes a feature NOT currently enabled on Hallow** — confirm it's actually live before authoring one (it predates the OSS-fork audit and was never deviation-enabled).

If a user asks for a still-blocked kind, tell them it requires EE and suggest an HTTP-trigger workaround (have the upstream system POST to a Windmill HTTP route) or a polling schedule against the source. For SQS, use the SQS trigger directly — get the config shape from the Windmill UI's SQS trigger form.

## Hallow gotchas (HTTP triggers)

These bit production. Verify each before considering a trigger "done".

### Multi-segment filenames use underscore, not dot

Trigger entity at `f/foo/bar_baz_v2` must live on disk as `bar_baz_v2.http_trigger.yaml` (underscore). A dotted name like `bar_baz.v2.http_trigger.yaml` derives a DIFFERENT entity path that does NOT match the server — every `wmill sync` diff will show the live trigger as a `-` delete (no paired `+`), and any push would destroy the real route. Always join multi-segment stems with `_`.

### `permissioned_as` is server-set, not request-writeable

The field appears in API responses but Windmill ignores it on YAML push. The server stamps it from the **identity of whoever pushed** — typically the deployer's user (matches `wmill workspace`'s active token owner). Setting an arbitrary value in local YAML is a no-op. If a trigger needs a different run-as principal, that user must push it (or set it via the UI post-create).

**Swap principal without re-pushing:** `wmill trigger set-permissioned-as <path> <email> --kind <kind>` rewrites the server-side principal without disturbing local YAML. Requires admin / `wm_deployers`. New principal must have read on the impl script's folder or runtime decrypt fails. See `cli-commands` SKILL.md → "Server-side principal swap".

### Headers require a `preprocessor`

`main()` never receives request headers — even with `raw_string: true` or `format: resource-headers`. HTTP triggers have NO `args` field; headers/query/path-params are exposed only via the `event` object inside `export async function preprocessor(event)`. The preprocessor's return value becomes main()'s arg set (matched by parameter **name**, order irrelevant). Resources must be fetched inside the preprocessor via `await wmill.getResource(...)`. Applies to all event triggers (http, webhook, email).

### Folder ACL gates route lookup (404 if caller can't READ the folder)

Even with valid auth, a call returns `"Trigger not found at name /w/<ws>/<route>"` if the caller can't read the trigger's folder. `authentication_method: windmill` does NOT bypass folder ACL. Place HTTP triggers in a folder readable by intended callers (typically `g/all: true`) and keep the elevated `script_path` impl in an admin-only folder. The "shared tool, elevated impl" pattern: `f/shared/<tool>.http_trigger.yaml` (readable) → `f/<admin>/<impl>.ts` (admin).

### Workspaced HTTP route URL = `/api/r/<ws>/<route>` (no extra `w/`)

Internal worker-to-trigger fetches use `${BASE_URL}/api/r/<workspace>/<route>`. The server's not-found error includes `/w/<ws>/...` because the internal lookup key prefixes with that — the URL path itself does NOT. Misreading the error and adding `w/` to the URL produces persistent 404s. The Windmill UI curl example on the trigger page is the canonical form.

### Create-then-update to start a trigger as `disabled`

`POST /http_triggers/create` ignores `enabled`/`mode` fields and ALWAYS creates the trigger in `mode: "enabled"` — even when local YAML says `disabled`. There is no `setenabled` endpoint for HTTP triggers (returns 404, unlike schedules). The workflow: create the trigger, then immediately `POST /http_triggers/update/<path>` with `mode: "disabled"`. Also: create requires `is_static_website` + `static_asset_config` keys present or it 422s with `"missing field is_static_website"`. Contrast: `schedule create` honors `enabled: false` at create time.
