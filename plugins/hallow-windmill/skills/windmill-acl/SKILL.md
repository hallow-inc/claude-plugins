---
name: windmill-acl
description: >-
  Use when granting, revoking, or auditing **item-level access** on an existing Windmill entity — narrower than its containing folder's default ACL. Triggers on "who can access this resource", "give X write access to Y", "share this script with just", "narrow this to just the finance team", "revoke access from", "grant access to a single resource/script/flow/trigger", "extra permissions", "Share" (Windmill UI `⋮` menu). Covers the `extra_perms` mechanism across all 21 Windmill entity kinds (script, flow, app, raw_app, resource, variable, schedule, folder, group_, volume, and all 11 trigger types), its 3-state read/write semantics, the folder-owner precondition, and the `volume` creator-only exception. NOT for: creating a new entity (use `resources`/`triggers`/`schedules`/`write-flow`), durable department-level access boundaries (use a top-level folder — see `docs/folders-groups.md`), or workspace role assignment (admin/developer/operator — that's a workspace setting, not an item grant).
---

# Windmill item-level ACL

Windmill has two independent ways to grant access to something:

1. **Folder ACL** — the default. Everything under `f/<folder>/` shares one ACL; only the top-level folder segment is a real permission boundary (subfolders are cosmetic — see `docs/folders-groups.md` §5). Use this for durable, department-level boundaries. This is what `resources`/`triggers`/`schedules` create by default.
2. **Item-level ACL** (this skill) — a documented, first-class Windmill feature ("Extra permissions", UI: `⋮` → Share) that grants access on a *single* entity, independent of its folder. Use this when you need narrower or different access than the folder default for one specific script/flow/resource/variable/schedule/trigger/app — without carving out a whole new sibling folder just to express that one narrowing.

Both are real Windmill mechanisms, upstream OSS, not a Hallow-fork addition. Item-level ACL is OR'd permissively with the folder's own grant — an item is visible if *either* grants it.

## The 3-state trap — read this before doing anything else

`extra_perms` is a JSON object on every ACL'd entity: `{"u/alice": true, "g/finance": false}`. It looks like a simple boolean map. **It is not.** The same field is read by two different checks:

- **Read/visibility check**: JSON *key presence* — `extra_perms ? 'g/finance'`. The boolean value is irrelevant here.
- **Write check**: the boolean *value* — `(extra_perms ->> 'g/finance')::boolean`.

So:

| State | Meaning |
|---|---|
| key absent | invisible — no access at all |
| key present, value `false` | **read access** (the value only gates write; presence alone grants read) |
| key present, value `true` | read + write access |

`{"g/all": false}` does **not** mean "denied." It means "granted read." This exact confusion already caused a real incident at Hallow — a folder's `extra_perms` was edited assuming `false` meant deny, and it silently widened read access instead (see project memory: `windmill-folder-acl-allowlist-absent-key-stronger`). Treat every raw `extra_perms` object you see with this in mind, and never hand-author one.

**Because of this, this skill never accepts or echoes a raw `{principal: bool}` payload.** Only these verbs:

- `grant read <principal> on <kind>/<path>`
- `grant write <principal> on <kind>/<path>`
- `revoke <principal> from <kind>/<path>`
- `list <kind>/<path>` — show current grants, no mutation

If a request arrives shaped like a raw boolean payload ("set g/finance to false on this resource"), do not pass it through — ask the requester to restate as a verb (read/write/revoke), and if they meant "deny," clarify that denial is the *absence* of the key, not `false`.

## The 21 supported kinds

`script`, `flow`, `app`, `raw_app`, `resource`, `variable`, `schedule`, `folder`, `group_`, `volume`, and all 11 trigger types: `http_trigger`, `websocket_trigger`, `kafka_trigger`, `nats_trigger`, `postgres_trigger`, `mqtt_trigger`, `amqp_trigger`, `gcp_trigger`, `azure_trigger`, `sqs_trigger`, `email_trigger`.

The same procedure below applies uniformly to all of them — this is one mechanism, not 21 special cases. Note: `raw_app` grants are stored on the underlying `app` table (Windmill internal detail; irrelevant to how you call the API, just don't be surprised if you're inspecting the DB directly).

## Precondition: folder-owner rights

Granting or revoking an item-level ACL entry requires the caller to already be an **owner of the item's containing folder** (`f/<folder>/...` → owner of `f/<folder>`). This is enforced server-side (`require_owner_of_path`) — the same folder-owner gate that already governs everything else scoped to that folder. There is no narrower principal; folder-owner or workspace-admin is the wall.

**Exception: `volume`.** Volumes are gated by **creator-only** — no folder-owner path exists for this kind at all. If the caller isn't the volume's creator (or a workspace admin), a folder-owner grant elsewhere won't help.

Before attempting a grant/revoke, confirm (or ask the caller to confirm) they have the right standing. If a call comes back permission-denied, explain *why* in these terms rather than relaying Windmill's raw error text — "you need owner rights on the containing folder to grant access on items inside it" is the actual reason, not a mysterious 403.

## Procedure — mandatory before/after diff

This skill mutates access to **existing** entities, unlike `resources`/`triggers`/`schedules` which create new ones. A wrong call here can expose a secret, not just misconfigure something new. There is no server-side enforcement of the verb-only rule above — the procedure below is the actual safety mechanism, so follow it every time, no shortcuts:

1. **List first.** Call the read endpoint and show the item's current `extra_perms` state before touching anything.
2. **State intent in plain language.** "This will grant READ to `g/finance` — the key `g/finance` will be present with value `false`." Never state it as "set g/finance to false" without the READ/WRITE word attached.
3. **Execute** the grant or revoke.
4. **List again.** Call the read endpoint a second time and show the resulting state.
5. **Confirm the diff matches intent.** If it doesn't, stop and investigate before telling the caller it's done.

No silent one-shot writes. If step 1 or step 4 fails to reach the API, do not proceed past that step.

## The raw API call

There is no `wmill` CLI subcommand, MCP tool, or SDK wrapper for this endpoint in this environment — `windmill-mcp` is Windmill's own built-in server and has no local source to extend from this plugin. Drive it directly via authenticated HTTP:

```
GET    /api/w/{workspace}/acls/get/{kind}/{path}
POST   /api/w/{workspace}/acls/add/{kind}/{path}
POST   /api/w/{workspace}/acls/remove/{kind}/{path}
```

Auth: `Authorization: Bearer <token>` header. A `wmill token create --label <short-lived-label> --expiration <ISO8601>` token works from a plain shell `curl` call — no running-script/SDK context required. This was verified live against `dev`: `GET /api/w/dev/acls/get/resource/f/hops/ducklake_catalog` returned `200 {}` via a short-lived test token, which was deleted immediately after the test.

**Token hygiene:** `wmill token create` has no scope-limiting flag — it mints a token with the caller's full identity and permissions. Always label it clearly, give it a short expiration (minutes, not days), and delete it (`wmill token delete <prefix>`) immediately after use. Never leave a standing token around for this.

**Gotcha — `wmill token create` fails silently from the wrong directory.** Run it from a directory with a resolvable `wmill.yaml`/workspace config (e.g. this repo's `dev/`), and pass `--workspace dev` explicitly. From an unresolvable directory it still prints something that looks like a token, but nothing is actually created server-side — `wmill token list` won't show it, and any call using it 401s. Always confirm the token appears in `wmill token list` before trusting it.

Confirmed request/response shapes (internal — never surface these raw shapes to a caller as something they should author themselves; the skill constructs them from the verb). Verified live end-to-end against `dev` (grant → list → revoke → list, full round-trip, `f/hops/ducklake_catalog`, `u/claudetag` as principal — chosen because they already had folder-level read on `f/hops`, making the grant a safe no-op):

```
GET /api/w/dev/acls/get/resource/f/hops/ducklake_catalog
→ 200 {}                                          (no item-level grants yet)

POST /api/w/dev/acls/add/resource/f/hops/ducklake_catalog
Body: {"owner": "u/claudetag", "write": false}     (write:false = READ grant)
→ 200 "Successfully modified granular acl"

GET /api/w/dev/acls/get/resource/f/hops/ducklake_catalog
→ 200 {"u/claudetag":false}                        (diff confirmed: key present, value false = read)

POST /api/w/dev/acls/remove/resource/f/hops/ducklake_catalog
Body: {"owner": "u/claudetag"}                     (no "write" field needed to revoke)
→ 200 "Successfully removed granular acl"

GET /api/w/dev/acls/get/resource/f/hops/ducklake_catalog
→ 200 {}                                          (back to prior state, confirmed)
```

For a write grant, use `{"owner": "<principal>", "write": true}` on the same `add` endpoint.

## When to reach for this vs. a new folder

| Situation | Use |
|---|---|
| A whole department/team needs durable, broad access to many things | Top-level folder (`f/<dept>/`) — see `docs/folders-groups.md` |
| One resource/script/trigger needs narrower or different access than its folder's default, as a one-off or small exception | This skill — item-level `extra_perms` |
| You're about to create a sibling folder (`f/<dept>_lake`, `f/<dept>_ops`, etc.) *solely* to express one narrower grant on one or a few items | Consider this skill instead — it may avoid the new-folder-plus-new-group overhead entirely |

This does not replace folder-split patterns already in flight elsewhere (e.g. the `people`/`hops` DuckLake catalog split) — those remain valid, and this skill is not a mandate to retrofit them. It's the missing option for *new* narrow-access needs going forward.
