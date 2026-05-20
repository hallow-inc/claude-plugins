# Windmill Groups + Folders: Multi-Tenant Isolation

Sources: [groups_and_folders](https://www.windmill.dev/docs/core_concepts/groups_and_folders) · [roles_and_permissions](https://www.windmill.dev/docs/core_concepts/roles_and_permissions) · [variables_and_secrets](https://www.windmill.dev/docs/core_concepts/variables_and_secrets) · [resources_and_types](https://www.windmill.dev/docs/core_concepts/resources_and_types) · [operator role](https://www.windmill.dev/docs/core_concepts/roles_and_permissions#operator)

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

## 5. Top-level folders = only enforcement boundary

Permission enforcement stops at the top-level folder. Practical implication for app-per-folder model:

- `f/app-foo/` — one security boundary. Everything inside is in-or-out for whoever has ACL.
- Cannot grant `g/team-foo` access to `f/app-foo/subdir-a` but not `f/app-foo/subdir-b`. Whole folder, all or nothing.
- Fine-grained sub-isolation requires separate top-level folders. Use `f/app-foo-admin/` vs `f/app-foo-ops/` if you need different access tiers within one app.

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

**Gotchas summary**:
1. Sub-folders are not permission boundaries — design top-level folders accordingly.
2. Schedules run as `edited_by` user — use a dedicated service user per app group.
3. Secret masking only covers ≥8 char values; short tokens leak in logs.
4. Operator visibility is workspace-wide toggle, not per-folder — all ops users in workspace get same visibility config.
5. Workspace-level isolation (separate Windmill workspaces) is the only hard wall; folder isolation is strong but within one workspace's auth context.
6. Apps always run as publisher — if an app in `f/app-foo` needs `f/shared` resources, the app publisher account must have Viewer on `f/shared`.
