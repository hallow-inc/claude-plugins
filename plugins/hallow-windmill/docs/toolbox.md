# Windmill Toolbox

Re-usable scripts and flows in the `dev` Windmill workspace that any Hallow employee can drop into their own flows or scripts. Each is a typed wrapper around Hallow infrastructure — Supabase, S3 sandbox, Snowflake, Slack — so callers don't re-implement auth, sandbox prefixing, schema scoping, or validation.

URL: `https://windmill.platform.hallow.app` (tailnet-only).

## At a glance

| Path | What it does |
|------|--------------|
| `f/shared/slack_post` | Post to Slack via webhook URL/resource or `chat.postMessage` |
| `f/shared/assert_principal` | Gate a script/flow to specific users or groups |
| `f/storage/s3_write_json` | Write JSON to the sandbox-data S3 bucket under your principal prefix |
| `f/storage/s3_read_json` | Read JSON from the sandbox-data S3 bucket |
| `f/storage/sandbox_data_prefix` | Build a safe S3 prefix from `WM_PERMISSIONED_AS + dataset` |
| `f/storage/sandbox_data_roundtrip` | Put+Get a tiny object to verify IAM/KMS perms |
| `f/warehouse/snowflake_query` | Run a Snowflake query, max-row-guarded |
| `f/warehouse/supabase_query` | Run a Postgres query against Hallow Supabase (read-only by default) |
| `f/platform/supabase_provision_area` | DDL: create a team or user role + schema (atom — use the flow) |
| `f/platform/flows/provision_supabase_area` | Self-service: provision a Supabase area + DM credentials |

## Atoms

### `f/shared/slack_post`

`{ text, webhook?, channel?, bot_token?, blocks?, thread_ts? } → { mode, ok, ts?, channel? }`

One Slack-posting helper. Two modes:

- **Webhook mode** — pass `webhook` as either a full incoming-webhook URL or a resource path (`f/shared/slack_ops_webhook`). Posts `{text, blocks}`.
- **Bot mode** — pass `channel` (and optionally a `bot_token` resource). Uses `chat.postMessage` with the bot token at `f/slack_bot/bot_token` by default.

Either `webhook` or `channel` is required.

```ts
await wmill.runScript("f/shared/slack_post", null, {
  channel: "#hallow-data-ops",
  text: ":checkered_flag: Daily pipeline done",
});
```

### `f/shared/assert_principal`

`{ allowed_groups?, allowed_users? } → { principal, matched_via }`

Reads `WM_PERMISSIONED_AS` (server-injected, unspoofable) and throws unless the principal matches `allowed_users` or `allowed_groups`. Group match also consults `WM_GROUPS`. With no allowlist, just validates the principal shape and returns it. Use as the first step of a flow that must be restricted.

```ts
await wmill.runScript("f/shared/assert_principal", null, {
  allowed_groups: ["g/admin"],
});
```

### `f/storage/s3_write_json` / `f/storage/s3_read_json`

`{ s3, dataset, key, body } → { uri, bucket, key, bytes }` / `{ s3, dataset, key } → { uri, bucket, key, body }`

Write/read JSON under `s3://<bucket>/<u/{user}|g/{group}>/<dataset>/<key>`. Principal taken from `WM_PERMISSIONED_AS`. Dataset and key are regex-validated. Pair with the `f/storage/sandbox_data` resource.

```ts
await wmill.runScript("f/storage/s3_write_json", null, {
  s3: await wmill.getResource("f/storage/sandbox_data"),
  dataset: "my_pipeline",
  key: "snapshot.json",
  body: { rows: [...] },
});
```

### `f/storage/sandbox_data_prefix` / `f/storage/sandbox_data_roundtrip`

- `sandbox_data_prefix` returns `{ bucket, region, prefix, principal }` for callers that want to drive their own S3 client.
- `sandbox_data_roundtrip` is a Put+Get verifier — call with no args for a smoke test, or pass `{ dataset, key, body }` to roundtrip your own data. Use as a pre-flight before a large multi-part write.

### `f/warehouse/snowflake_query`

`{ snowflake, sql, params?, max_rows? } → { rows, row_count, ms }`

JWT/key-pair auth via the resource's `private_key`. Throws if rows > `max_rows` (default 100000) — tighten WHERE or raise. Works with `f/warehouse/snowflake` (windmill_role) and `f/finance/snowflake_reader` (finance read-only).

```ts
await wmill.runScript("f/warehouse/snowflake_query", null, {
  snowflake: await wmill.getResource("f/warehouse/snowflake"),
  sql: "SELECT count(*) FROM data.events WHERE event_date = $1",
  params: ["2026-05-01"],
});
```

### `f/warehouse/supabase_query`

`{ db, sql, params?, allow_mutation? } → { rows, row_count, ms }`

By default rejects SQL whose first keyword mutates state (INSERT/UPDATE/DELETE/TRUNCATE/DROP/ALTER/CREATE/GRANT/REVOKE). Pass `allow_mutation: true` to permit. Pair with `f/warehouse/supabase_user_db` for the read-only analyst role, or with your own team-area Postgres resource.

```ts
await wmill.runScript("f/warehouse/supabase_query", null, {
  db: await wmill.getResource("f/warehouse/supabase_user_db"),
  sql: "SELECT id, name FROM platform.accounts WHERE created_at > now() - interval '7 days'",
});
```

## Flow: `f/platform/flows/provision_supabase_area`

Self-service provisioning of a Postgres role + schema on the Hallow Supabase.

Inputs: `{ kind: "team"|"user", name, requester_slack_id, slack_channel? }`.

Restricted to `g/admin` (via `assert_principal`). Steps:

1. Gate — `assert_principal({ allowed_groups: ["g/admin"] })`.
2. Generate a 32-byte password.
3. DDL — create role (if not exists) + set password + `statement_timeout=30s` + `CONNECTION LIMIT 10` + create schema + grants on `platform` schema.
4. Public announce to `slack_channel` (default `#platform-alerts`) — no secrets.
5. DM the requester with role, schema, password (one-time), and a usage snippet.

Re-running the flow with the same `kind+name` **rotates the password** — that's the intended rotation path. Other steps are idempotent (DDL is `IF NOT EXISTS` + set-add grants).

### Why schema-per-team, not DB-per-team

The Hallow Supabase is a single Coolify-hosted stack: one Postgres, one PostgREST, one GoTrue, one Studio. Supabase's UX is bound to one database — multiple DBs break Studio/Auth/Storage. Schema-per-team mirrors the schema-per-app convention already in `architecture.md` and the v2 new-app provisioning flow.

Connection caveat: the shared Postgres has `max_connections >= 200` (architecture.md §146). Each role is capped at `CONNECTION LIMIT 10` and `statement_timeout = '30s'` to prevent a runaway script from starving the pool.

## Conventions

Every atom in this toolbox follows the same shape:

- Typed input via inline TypeScript `type` declarations.
- Validates inputs early; throws with clear messages.
- Returns plain JSON; no side-channel state.
- Default runtime `bun`.
- No comments unless the why is non-obvious.

For the source of truth, see `infra/windmill/dev/f/{shared,storage,warehouse,platform}/`.
