# Scrub admin-only paths from the hallow-windmill plugin

## Why

The `hallow-windmill` plugin's audience is **exclusively non-admin** Hallow engineers (README line 12: "Admin/infra-ops content lives in the platform repo"). But several Windmill actions are hard-gated to workspace admins by row-level security — a non-admin literally *cannot* perform them:

- Write/publish to `f/shared/` (needs `g/admin` folder ownership + admin push)
- Create a group (RLS: `windmill_admin` only; `wm_deployers` does **not** count)
- Seed variables under `f/platform_secrets/` (admin / `u/sandbox` only)
- Push a trigger/schedule with elevated `permissioned_as: u/sandbox` (server stamps from pusher identity — only an admin push achieves it)
- `set-permissioned-as`, `workspace connect-slack` (admin / `wm_deployers`)

Today the plugin **leaks admin-only how-to recipes into non-admin flows.** A non-admin gets routed into an action they can't complete, hits an RLS rejection, and is stuck with no "here's what you *can* do instead." An audit found six such leaks (one whole doc, two skill lines, two doc sections, one CLI reference line).

Compounding this, the plugin references a group **`wm_deployers` that no longer exists** — it was deleted and its deploy rights folded into the ordinary folder-ACL model. Twelve references across six files are now stale and, worse, *wrong about the capability boundary*. They imply a privileged "deployer" tier gates normal deploys and `set-permissioned-as`. The correct model:

- **Deploying your own work is ordinary** — being a Writer on the target folder is all it takes. There is no deploy group.
- **`set-permissioned-as` and elevated `permissioned_as` pushes are admin-only** now that `wm_deployers` is gone. "admin or `wm_deployers`" collapses to "admin."

Fixing this is the same theme — correcting the admin/non-admin capability boundary — so it folds into this change.

## What Changes

The rule: **the plugin is non-admin-audience, so admin how-to is removed, not dual-pathed.** Where a non-admin hits an admin-gated wall, the plugin redirects to what they *can* do (a team folder) and never shows the admin recipe. There is no "if you're an admin, do X" branch anywhere — admins fend for themselves via the platform repo.

Scope is documentation/skill content only. No code, no capability, no behavior of any Windmill entity changes.

**Removed entirely (no replacement pointer):**
- `docs/shared-tool-template.md` — 100% admin recipe (publishing to `f/shared/` + `u/sandbox` push). Deleted.
- The `windmill-build` routing row "I want to publish a shared atom" → template. Deleted; the concept of workspace-wide publishing disappears from the non-admin view.

**Rewritten to the non-admin move:**
- `windmill-build` Step 1c "Anyone at Hallow → `f/shared/`" → tops out at `f/<team>/` (the real ceiling of non-admin capability).
- `patterns.md` "Adding a new shared atom" — drop the `f/shared/` create option; keep only the `f/<domain>/` option a non-admin can write.
- `patterns.md` §5 elevation recipe — collapse the "wrap in `permissioned_as: u/sandbox` and push" recipe to a one-line "elevation is admin-only; ask an admin, you provide the script."

**Caveat added:**
- `cli-commands/references/commands.md` `group create` — inline "admin-only on Hallow" note.

**Stale `wm_deployers` references corrected (6 files, ~9 "admin or wm_deployers" gates):**
- Every "admin / `wm_deployers`" or "admin or wm_deployers group" gate on `set-permissioned-as` → **"admin"** (`triggers/SKILL.md`, `schedules/SKILL.md`, `cli-commands/SKILL.md`, `cli-commands/references/commands.md` ×5).
- `folders-groups.md` §0 — remove the `wm_deployers` explainer sentence and drop `g/wm_deployers` from the standing-groups list.
- The word "deployer" as a *role noun* ("deployer-stamped", "the deployer's user", "non-deployer principal") is **kept** — it means "whoever ran the push," an unchanged behavior, not the deleted group.

**Cleanup:**
- README + `folders-groups.md` "Related" list — remove dangling references to the deleted template.

## Boundaries — what does NOT change

The read/call side is untouched, because a non-admin **can** read and call `f/shared/` atoms:

- `windmill-discover` recommending `f/shared/` atoms — stays (using ≠ publishing).
- Calling `f/shared/slack_post` etc. from a script/flow (`getResource`) — stays.
- Referencing `f/platform_secrets/` variables *by name* in scripts — stays (script references; admin seeds).
- `windmill-debug` troubleshooting content explaining *why* a 403 / `permissioned_as` mismatch happened — stays (reference explanation, not a step a user is told to take).
- Correct existing "ask an admin" pointers (`f/platform_secrets/` writes, `connect-slack`, `set-permissioned-as`) — stay as-is.

## Impact

- Non-admin users no longer get routed into RLS-rejected dead ends.
- The plugin surface shrinks: one doc deleted, no admin recipes to maintain or drift.
- A user who wants workspace-wide sharing tops out cleanly at a team folder — the true limit of non-admin capability — rather than being sent to fail at `f/shared/`.
- Files touched: `docs/shared-tool-template.md` (delete), `skills/windmill-build/SKILL.md`, `docs/patterns.md`, `skills/cli-commands/references/commands.md`, `README.md`, `docs/folders-groups.md`.
