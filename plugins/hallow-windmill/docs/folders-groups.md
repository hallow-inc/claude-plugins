# Windmill Groups + Folders: Multi-Tenant Isolation

Sources: [groups_and_folders](https://www.windmill.dev/docs/core_concepts/groups_and_folders) · [roles_and_permissions](https://www.windmill.dev/docs/core_concepts/roles_and_permissions) · [variables_and_secrets](https://www.windmill.dev/docs/core_concepts/variables_and_secrets) · [resources_and_types](https://www.windmill.dev/docs/core_concepts/resources_and_types) · [operator role](https://www.windmill.dev/docs/core_concepts/roles_and_permissions#operator)

---

## 0. When to create a group vs just use a folder ACL

Default at Hallow: **reach for a folder ACL first; create a new group only when the same set of people needs the same access across multiple folders.**

The historical reason to avoid groups — the Community-Edition **3-group cap** — is **GONE.** Hallow's customized-OSS fork neutered it (`deviation: neuter CE group cap`), so group creation no longer hard-fails at 3. But "you can" ≠ "you should": a group is a reusable identity to maintain, and most access needs are satisfied by attaching individual users (or one existing group) directly to a folder.

Decision:

| Situation | Do this |
|---|---|
| One app/domain, a handful of people, access doesn't recur elsewhere | **Folder ACL only.** Attach `u/person@hallow.app` (or an existing `g/team-*`) at Viewer/Writer on `f/<app>`. No new group. |
| The *same team* needs the same access on **several** folders | **A group is warranted.** One `g/team-foo`, attach it to each folder — beats re-listing the same users per folder. Ask an admin to create it. |
| You only need a *subset* of an existing group on one folder | Folder ACL — attach the individual users; don't fork a near-duplicate group. |
| Gating a privileged/shared tool | Reuse the standing groups (`g/admin`, `g/all`) + folder placement — see §5. Don't invent a group for one tool. |

Who can create groups: **workspace admins only.** Group creation runs under the caller's DB identity (`user_db.begin(authed)` → `set_session_context(is_admin, …)`), and the `group_` table's row-level security only permits the INSERT when the session is admin (the `windmill_admin` role). A non-admin's create call is rejected by RLS. So a non-admin engineer asks an admin, stating which folders the group will attach to and why an existing group / folder-ACL won't do. Standing system/convention groups not to duplicate: `g/all`, `g/admin`, `g/slack`, `g/error_handler`.

Folder-ACL mechanics (levels, inheritance, the subfolder gotcha) are in §1 below.

---

## 1. Folder ACL semantics

Three levels. Viewer = read-only. Writer = read + write. Admin = read + write + manage permissions + add admins.

Groups and individual users attach the same way — give `g/team-foo` Writer or Viewer on folder `f/app-foo`. All group members inherit that level homogeneously. Can also attach individual `u/person@hallow.app` at a different level than their group.

Default ACL on creation: creator gets Admin. Nobody else sees it until explicitly granted.

Path split:
- `u/<email>/item` — owned by one user. Private by default. Accessible only to that user unless extra perms added.
- `f/<folder>/item` — folder-scoped. Accessible to anyone with any ACL on `f/<folder>`.

**Critical gotcha**: only the **top-level** folder enforces permission inheritance. `f/app-foo/subfolder` has no independent permission boundary — access is governed entirely by the `f/app-foo` ACL. Subfolders are organizational only, not security boundaries. [groups_and_folders]

---

## 2. Folder discoverability for non-ACL users

Docs say folder items are "available to users having access to the folder" — implied: no ACL = no access. Users without explicit ACL should not see or list the folder. Not 100% explicit in docs but the permission model is path-based and additive — no ACL entry means no grant. Safe to treat as fully invisible. [roles_and_permissions]

---

## 3. Variables + resources: paths and secret masking

Variables and resources both follow the same path/ownership model.

- `u/alice/secret` — Alice only (unless extra-permed)
- `f/app-foo/db_creds` — anyone with read on `f/app-foo`

Secret values cannot be viewed outside of scripts — UI shows masked value. In job logs, first 3 chars shown + `*****` rest (only for values ≥8 chars, in-memory before DB write). Accessing a secret generates a `variables.decrypt_secret` audit event. [variables_and_secrets]

Non-ACL users: cannot access value, likely cannot see the secret name either since path ACL gates listing. But treat masking as defense-in-depth — primary control is path ACL.

Resources follow identical semantics. `u/user/my_db` vs `f/app-foo/my_db`. Share a resource with a team by putting it in their folder or explicitly extra-perming `g/team-foo`. [resources_and_types]

---

## 4. Schedules, triggers, webhooks

Schedules and triggers execute **as the user who last edited them** (`edited_by`). They live at a path — put the schedule at `f/app-foo/my_schedule` and it falls under `f/app-foo` ACL. Windmill does not explicitly document per-item inheritance for schedules, but path ownership applies: creator = owner, folder = shared namespace.

Gotcha: if an app-foo schedule is edited by a user who leaves or loses access, the run-as identity may break. Pin schedules to a service/bot user inside the group. [roles_and_permissions]

---

## 5. Top-level folders = the only *nesting* boundary — but not the only ACL mechanism

Permission enforcement via **path/folder structure** stops at the top-level folder. Subfolders are cosmetic, not a nested security boundary — this part still holds:

- `f/app-foo/` — one folder-level security boundary. `f/app-foo/subdir-a` and `f/app-foo/subdir-b` are NOT independently addressable ACL scopes; Windmill resolves the folder unit from the path's second segment only.
- Cannot grant `g/team-foo` access to `f/app-foo/subdir-a` but not `f/app-foo/subdir-b` **by folder structure alone**. A sibling top-level folder (`f/app-foo-admin/` vs `f/app-foo-ops/`) is one valid way to express a durable, structural access tier.

**But this is not the only way to narrow access below the folder default.** Windmill's documented "Extra permissions" feature (UI: `⋮` → Share) grants item-level access on a *single* script/flow/resource/variable/schedule/trigger/app/etc., independent of its folder, OR'd permissively with the folder's own grant. This is a first-class, upstream OSS mechanism — not a workaround. See the `windmill-acl` skill for the procedure (verb-based grant/revoke, mandatory before/after diff, and the `extra_perms` 3-state read/write semantics that make hand-authoring this JSON dangerous — the exact trap that caused the Libra DuckLake guard-fork incident).

**When to use which:**
- Durable, department/team-level boundary that should hold for everything inside it → a top-level folder (as below).
- One-off or narrower-than-folder access on a specific existing item → `windmill-acl` item-level grant, not a new sibling folder. Reach for a new folder only when the narrowing is itself durable and spans many items, not to express a single exception.

---

## 6. Run-as / run-on-behalf and cross-folder isolation

Scripts and flows can be configured to run "on behalf of" a specific user — they execute with that user's permissions. Apps always run as the app publisher's permissions.

Cross-folder resource access: if a script in `f/app-foo` references resource `f/shared/postgres`, the **caller's permissions** determine access — the caller must have read on `f/shared`. [roles_and_permissions]

Isolation implication: a script in `f/app-foo` can reach `f/shared` if the executing user (or run-as user) has ACL on `f/shared`. To fully isolate app-foo from app-bar, ensure no user/group has ACL on both folders, and do not put run-as users that cross both. The `f/shared/` folder is intentionally open — all relevant groups get Viewer there. [resources_and_types]

---

## 7. Operator vs Developer — which per group

| Role | Creates/edits scripts | Executes | Sees resources/vars in UI | Cost |
|---|---|---|---|---|
| Developer | Yes | Yes | Yes | 1 seat |
| Operator | No | Yes (in-scope only) | Configurable (admin can hide) | 0.5 seat |

Use Developer for: team members building/maintaining app-foo workflows.
Use Operator for: app consumers who only trigger runs (support staff, other apps, bots).

Workspace admin can toggle which sections Operators see: runs, schedules, resources, variables, triggers, audit logs, groups, folders, workers. Lock down all for pure execution-only consumers. [roles_and_permissions#operator]

---

## 8. What goes in `u/admin@hallow.app/` vs `f/shared/`

`u/admin@hallow.app/` — personal scratch space only. Nothing here is team-visible. Good for one-off test scripts, personal tokens. Do not put platform resources here — they die with the account and are invisible to others.

`f/shared/` — cross-app resources: Supabase connection (read-only service key), Slack webhook, shared utility scripts/flows usable by all apps. Give all team groups Viewer. Admins only get Writer/Admin.

`f/app-<name>/` — one folder per app. Group `g/app-<name>` gets Writer. App operators get Viewer or are assigned Operator workspace role scoped to this folder.

---

## Recommended topology for Hallow

```
Groups:
  g/platform-admins     → workspace Admin role
  g/app-<name>          → workspace Developer role, Writer on f/app-<name>
  g/app-<name>-ops      → workspace Operator role, Viewer on f/app-<name>

Folders:
  f/shared/             → Viewer: all groups; Writer: g/platform-admins
    resources/          (Supabase, internal APIs)
    scripts/            (shared utilities)
  f/app-<name>/         → Writer: g/app-<name>; Viewer: g/app-<name>-ops
    resources/          (app-specific DBs, creds)
    flows/
    scripts/
    schedules/          (pin schedule edited_by to a bot user in g/app-<name>)

u/ namespace:
  personal use only, nothing production
```

Operator visibility: disable resources, variables, audit logs, groups, folders for `g/app-<name>-ops`. Leave runs + schedules visible so they can check job status.

---

## 9. Item-level ACL — the second mechanism (beyond folders)

Everything above describes **folder** ACL — the default, and the right tool for durable boundaries. Windmill has a second, independent grant surface: item-level `extra_perms`, set via the UI's `⋮` → **Share** on any single entity, or the `/api/w/{workspace}/acls/get|add|remove/{kind}/{path}` API. It applies to 21 entity kinds (script, flow, app, raw_app, resource, variable, schedule, folder, group_, volume, and all 11 trigger types) and is OR'd permissively with the item's folder grant — an item is visible if *either* grants it.

This is not an internal/undocumented mechanism — Windmill documents it as a first-class feature. It is, however, easy to misuse: the `extra_perms` JSON is `{principal: bool}`, but read-visibility is gated by *key presence* while write is gated by the *boolean value* — so `{"g/all": false}` grants read, not denial. This ambiguity already caused a real incident at Hallow (Libra DuckLake guard-fork mutation). **Never hand-author this JSON.** Use the `windmill-acl` skill, which only accepts named verbs (`grant read`, `grant write`, `revoke`) and runs a mandatory before/after diff on every mutation.

Granting an item-level ACL entry still requires folder-owner rights on the item's containing folder (or, for `volume`, creator-only rights — the one kind with no folder-owner path at all) — this is not a way around folder-level gatekeeping, just a way to express something narrower than the folder's own default from inside it.

**Practical effect on the topology below**: before reaching for a new sibling folder (`f/app-foo-admin/` pattern) solely to express one narrower exception, consider whether an item-level grant on the specific resource/script/trigger in question does the job with less structure to maintain. Reserve new top-level folders for boundaries that are themselves durable and span multiple items.

**Gotchas summary**:
1. Sub-folders are not permission boundaries — design top-level folders accordingly.
2. Schedules run as `edited_by` user — use a dedicated service user per app group.
3. Secret masking only covers ≥8 char values; short tokens leak in logs.
4. Operator visibility is workspace-wide toggle, not per-folder — all ops users in workspace get same visibility config.
5. Workspace-level isolation (separate Windmill workspaces) is the only hard wall; folder isolation is strong but within one workspace's auth context.
6. Apps always run as publisher — if an app in `f/app-foo` needs `f/shared` resources, the app publisher account must have Viewer on `f/shared`.
