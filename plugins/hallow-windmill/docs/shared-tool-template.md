# Windmill Shared Tool Template

Repeatable recipe for exposing scripts/flows to flow authors as composable building blocks.

Canonical example: `f/shared/slack_post`.

## 4-Part Recipe

### 1. Location

| Scope | Folder | ACL |
|---|---|---|
| Cross-cutting (any workspace user) | `f/shared/<tool>` | `g/admin` owner, opt-in callers |
| Domain-specific | `f/<domain>/<tool>` (e.g. `f/slack_tools`, `f/storage`) | Domain folder ACL |

Folder ACL governs who can call. `f/shared/folder.meta.yaml`:

```yaml
summary: Cross-cutting shared scripts (error handlers, etc)
display_name: shared
owners:
  - g/admin
extra_perms:
  g/admin: true
  g/all: false
```

### 2. Script File (`<tool>.ts`)

Four design rules:

1. **Resource-or-literal args** — accept resource path OR raw value. Caller skips plumbing.
2. **Canonical default** — `wmill.getResource("f/<canonical>")` when arg omitted.
3. **Typed discriminated union return** — downstream flows can branch.
4. **Throw on validation failure** — surfaces as Windmill job failure.

Example (`f/shared/slack_post.ts`):

```typescript
import * as wmill from "windmill-client";

type Slack = { token: string };

type Result = {
  mode: "webhook" | "bot";
  ok: true;
  ts?: string;
  channel?: string;
};

export async function main(
  text: string,
  webhook?: string,
  channel?: string,
  bot_token?: Slack,
  blocks?: Record<string, unknown>[],
  thread_ts?: string,
): Promise<Result> {
  if (!text || text.length === 0) {
    throw new Error("text is required");
  }
  if (!webhook && !channel) {
    throw new Error("provide either webhook (URL or resource path) or channel");
  }

  if (webhook) {
    const url = webhook.startsWith("$res:") || webhook.startsWith("f/") || webhook.startsWith("u/")
      ? (await wmill.getResource(webhook.replace(/^\$res:/, ""))) as { url: string }
      : { url: webhook };

    const body: Record<string, unknown> = { text };
    if (blocks) body.blocks = blocks;

    const res = await fetch(url.url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      throw new Error(`Slack webhook returned ${res.status}: ${await res.text()}`);
    }
    return { mode: "webhook", ok: true };
  }

  const token = bot_token?.token
    ?? ((await wmill.getResource("f/slack_bot/bot_token")) as Slack).token;
  if (!token) {
    throw new Error("bot token missing — pass bot_token or seed f/slack_bot/bot_token");
  }

  const body: Record<string, unknown> = { channel, text };
  if (blocks) body.blocks = blocks;
  if (thread_ts) body.thread_ts = thread_ts;

  const res = await fetch("https://slack.com/api/chat.postMessage", {
    method: "POST",
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(body),
  });
  const data = (await res.json()) as { ok: boolean; ts?: string; channel?: string; error?: string };
  if (!res.ok || !data.ok) {
    throw new Error(`Slack chat.postMessage failed: ${data.error ?? res.status}`);
  }
  return { mode: "bot", ok: true, ts: data.ts, channel: data.channel };
}
```

### 3. Schema YAML (`<tool>.script.yaml`)

- `summary` — one-liner shown in flow editor picker
- `description` — multi-line; document each mode + when to use
- Per-field `description` — tooltips in flow editor
- `format: resource-<type>` — for resource args, renders resource picker
- `required:` — strict, only truly mandatory fields

Example (`f/shared/slack_post.script.yaml`):

```yaml
summary: Post a message to Slack (webhook URL or bot chat.postMessage)
description: |
  One Slack-posting helper for any script or flow. Two modes:

  - webhook mode: pass `webhook` as either an incoming-webhook URL or a
    resource path (f/shared/slack_ops_webhook etc). Posts `{text, blocks}`.
  - bot mode: pass `channel` (and optionally `bot_token` resource). Posts via
    chat.postMessage using the bot token at f/slack_bot/bot_token by default.

  Either `webhook` or `channel` is required. Returns the message ts in bot mode.
lock: '!inline f/shared/slack_post.script.lock'
kind: script
schema:
  $schema: https://json-schema.org/draft/2020-12/schema
  type: object
  properties:
    text:
      type: string
      description: Message text (Slack mrkdwn).
      default: null
      originalType: string
    webhook:
      type: string
      description: Slack incoming-webhook URL OR resource path (e.g. f/shared/slack_ops_webhook).
      default: null
      originalType: string
    channel:
      type: string
      description: Channel ID or name (bot mode). Required if webhook not given.
      default: null
      originalType: string
    bot_token:
      type: object
      description: f/slack_bot/bot_token (defaulted if omitted)
      default: null
      format: resource-slack
      properties:
        token:
          type: string
          originalType: string
      required:
        - token
    blocks:
      type: array
      description: Optional Slack Block Kit blocks.
      default: null
      items:
        type: resource
        resourceType: record
      originalType: resource[]
    thread_ts:
      type: string
      description: Optional parent thread timestamp (bot mode).
      default: null
      originalType: string
  required:
    - text
timeout: 30
```

### 4. Surface to Users

```bash
wmill script generate-metadata f/shared/<tool>   # regen .lock + .script.yaml
```

Mirror to server via API (per project rules: no `wmill sync`, edit local YAML first then API push).

Users discover via flow editor → "Workspace scripts" → `f/shared/<tool>`. Summary appears in picker; field descriptions render as tooltips.

## Checklist

1. Pick folder. Confirm ACL gives intended callers `read` (folder owners decide).
2. Scaffold: `wmill script bootstrap f/shared/<name>` (or via skill).
3. Write `main()`:
   - Resource-or-literal args
   - Canonical default via `getResource`
   - Discriminated-union return
   - Throw on validation failure
4. Fill `.script.yaml`: `summary`, `description`, per-field `description`, `format: resource-<type>`.
5. `wmill script generate-metadata <path>`.
6. Mirror to server via API.
7. Test in throwaway flow: confirm picker shows summary + tooltips.

## Variants

| Variant | When | How |
|---|---|---|
| Flow tool | Multi-step pattern | `f/shared/<name>.flow/flow.yaml` via `wmill flow new` |
| Trigger-wrapped | Tool needs elevated identity (decrypt secrets folder) | Wrap with HTTP trigger `permissioned_as: u/<admin>` (scripts always run as caller) |
| HTTP-triggered | Tool needs request headers | Add `export async function preprocessor(event)` |

## Related

- `folders-groups.md` — folder ACL model
- `patterns.md` §3 — local-yaml-first workflow (edit local first, mirror via MCP)
- `f/shared/slack_post.ts` — canonical example in dev workspace
