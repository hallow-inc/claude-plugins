---
name: triggers
description: Use when configuring a Windmill **trigger** — HTTP route, webhook, or email entry point that invokes a script or flow on Hallow's OSS Windmill. Triggers on "webhook in windmill", "http route", "email to a script", `.trigger.yaml`. Covers trigger types, payload mapping, auth, retry. NOT for: time-based runs (use schedules skill), Kafka/NATS/MQTT/SQS/GCP/Azure/Postgres-CDC/WebSocket triggers (EE-only, not available on Hallow).
---

# Windmill Triggers (OSS subset)

Triggers let external events invoke scripts and flows. Hallow runs Windmill OSS — only **HTTP** and **email** triggers are available. Webhook = HTTP trigger with `authentication_method: none`.

## File naming

`{path}.{trigger_type}_trigger.yaml`

- `u/user/webhook.http_trigger.yaml`
- `f/inbound/orders.email_trigger.yaml`

## Type → reference

| Trigger | File suffix | Reference |
|---|---|---|
| HTTP / webhook | `*.http_trigger.yaml` | `references/http.md` |
| Email | `*.email_trigger.yaml` | `references/email.md` |

Read the matching reference at `${CLAUDE_PLUGIN_ROOT}/skills/triggers/references/<name>.md` before writing the YAML. Every trigger type also includes shared `retry` + `error_handler_path` — see `references/common-retry.md`.

## Hallow ban

`wmill sync push` and `wmill sync pull` are banned in this workspace. They delete server state not in local files and clobber secret variables. Mirror trigger changes via the `windmill` MCP tools or the Windmill UI — never `wmill sync`.

## Not available

These trigger types are Enterprise-only and **not available** on Hallow's Windmill:

- WebSocket (`*.websocket_trigger.yaml`)
- Kafka (`*.kafka_trigger.yaml`)
- NATS (`*.nats_trigger.yaml`)
- Postgres CDC (`*.postgres_trigger.yaml`)
- MQTT (`*.mqtt_trigger.yaml`)
- SQS (`*.sqs_trigger.yaml`)
- GCP Pub/Sub (`*.gcp_trigger.yaml`)
- Azure Event Grid (`*.azure_trigger.yaml`)

If a user asks for one of these, tell them it requires EE and suggest an HTTP-trigger workaround (have the upstream system POST to a Windmill HTTP route) or a polling schedule against the source.
