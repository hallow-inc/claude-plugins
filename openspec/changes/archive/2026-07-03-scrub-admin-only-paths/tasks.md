# Tasks — scrub-admin-only-paths

All paths relative to `plugins/hallow-windmill/`. Content-only change; no code, no entity behavior.

## 1. Delete the admin-only shared-tool recipe

- [x] 1.1 Move `docs/shared-tool-template.md` to `__to_delete/` (per file-deletion policy — never `rm`). Whole file is a `f/shared/` publish recipe a non-admin cannot execute.
- [x] 1.2 Verify no skill or doc still *routes a user into* the file. (Dangling text references are cleaned in tasks 5–7; this checks nothing treats it as a live step.)

## 2. windmill-build — remove the two leaks

- [x] 2.1 `skills/windmill-build/SKILL.md` Step 1c (line ~75): the "Anyone at Hallow → `f/shared/` (requires admin to grant write)" option. Rewrite so the non-admin ceiling is a **team folder**: e.g. "Anyone on my team → `f/<team>/`". Drop `f/shared/` as a user-selectable option entirely. No admin recipe, no "ask an admin to grant f/shared."
- [x] 2.2 `skills/windmill-build/SKILL.md` Step 4 (line ~114): the `f/shared/` bullet under "Tell the user how to share it". Remove the `f/shared/` line; leave `u/<user>/` and `f/<team>/` as the two real outcomes.
- [x] 2.3 `skills/windmill-build/SKILL.md` routing table (line ~128): **delete** the row `"I want to publish a shared atom" | Read ... shared-tool-template.md ...`. No replacement row — the concept leaves the non-admin view.

## 3. patterns.md — collapse admin recipes to non-admin reality

- [x] 3.1 "Adding a new shared atom" (lines ~118–124): delete the `Cross-workspace: f/shared/<name>, owners g/admin` bullet. Keep the `Domain-scoped: f/<domain>/<name>` bullet (a non-admin can write a domain folder they own) plus the schema/return-type bullets. Delete line 124 "Full recipe: see ... shared-tool-template.md".
- [x] 3.2 §5 "Script vs trigger permissions" (line ~156): collapse the "wrap it in an HTTP trigger with `permissioned_as: u/sandbox` … the admin push must come from the u/sandbox token" recipe to a single non-admin line: elevation is **admin-only** — a non-admin asks an admin to wrap + push the trigger and provides the script; scripts always run as the caller. Keep the factual two bullets (lines 153–154: scripts can't elevate, triggers/schedules/flows can) as reference.
- [x] 3.3 §7 trigger-ACL note (line ~262): the "put the trigger in `f/shared/` … impl in an admin-only folder … `permissioned_as: u/sandbox`" recipe. This is the shared+elevated publish pattern (admin-only). Reduce to the reference fact a non-admin needs — *folder ACL gates trigger route lookup; a caller who can't read the trigger's folder gets "Trigger not found"* — and drop the "here's how to publish the elevated shared trigger" recipe.
- [x] 3.4 `docs/patterns.md` line 15 (companion-docs list): remove the `shared-tool-template.md` list entry.

## 4. cli-commands reference — add the missing caveat

- [x] 4.1 `skills/cli-commands/references/commands.md` `group create` (line ~209): append inline caveat "(admin-only on Hallow; non-admins ask a workspace admin — `wm_deployers` does NOT grant group creation)". Matches the existing gate style used for `f/platform_secrets/` writes and `connect-slack` in the same file.

## 5. README — drop dangling template references

- [x] 5.1 `README.md` line ~70: remove the `docs/shared-tool-template.md | Reference | Recipe for adding a new reusable tool.` table row.
- [x] 5.2 `README.md` line ~155: remove the `shared-tool-template.md ← recipe for new shared tools` line from the layout tree.
- [x] 5.3 `README.md` line ~128 (Hard rules / routing, if present) and the "publish a shared atom" mention in the build front-door description — confirm none survive pointing at the deleted file.

## 6. folders-groups — drop dangling template reference

- [x] 6.1 `docs/folders-groups.md` §0 line ~20: the "Gating a privileged/shared tool" row references `shared-tool-template.md`. Remove that reference. The row's group-vs-folder advice stays; only the pointer to the deleted admin recipe goes.

## 7. Verify no orphans remain

- [x] 7.1 `grep -rn 'shared-tool-template' plugins/hallow-windmill --include='*.md'` (excluding `__to_delete/`) returns **zero** hits.
- [x] 7.2 Re-scan for residual leaks: no skill/doc *instructs a non-admin* to write to `f/shared/`, create a group, seed `f/platform_secrets/`, push an elevated `permissioned_as: u/sandbox` trigger, or run `set-permissioned-as` / `connect-slack` as a step. (Reference explanations of *why* those fail, and read/call-side usage of `f/shared` atoms, correctly remain.)
- [x] 7.3 `claude plugin validate .` passes (frontmatter/manifest intact after edits).
- [x] 7.4 Spot-check the read/call side is untouched: `windmill-discover` still recommends `f/shared/` atoms; `getResource("f/shared/...")` examples intact; `f/platform_secrets/` *by-name references* in scripts intact.

## 8. Correct stale `wm_deployers` references

The group was deleted; its deploy rights are the ordinary folder-ACL model. Fix the *capability gate* wording; keep the *role noun* "deployer" (= whoever pushed) wherever it describes stamping behavior.

- [x] 8.1 Collapse the `set-permissioned-as` gate from "admin or wm_deployers" to **"admin"** in all five CLI-reference lines: `skills/cli-commands/references/commands.md` lines ~33 (app), ~119 (flow), ~382 (schedule), ~414 (script), ~523 (trigger) — each `(requires admin or wm_deployers group)` → `(requires admin)`.
- [x] 8.2 `skills/cli-commands/SKILL.md` line ~82: "Requires admin or `wm_deployers` group." → "Requires admin."
- [x] 8.3 `skills/triggers/SKILL.md` line ~70: "Requires admin / `wm_deployers`." → "Requires admin."
- [x] 8.4 `skills/schedules/SKILL.md` line ~211: "Requires admin / `wm_deployers`." → "Requires admin."
- [x] 8.5 `docs/folders-groups.md` §0 line ~22: remove the sentence "`wm_deployers` is a deploy-rights group, NOT admin — it does **not** let you create groups." (the group is gone). Remove `g/wm_deployers` from the standing-groups list at the end of that line. Keep the rest of the group-creation-is-admin-only explanation.
- [x] 8.6 **Do NOT touch** the role-noun uses of "deployer" — `skills/triggers/SKILL.md` :3,:68 · `skills/schedules/SKILL.md` :3,:205,:207 · `skills/windmill-debug/references/symptom-index.md`:44 · `docs/patterns.md` :252,:254. These describe *deployer-stamping* (server stamps `email`/`permissioned_as` from the pusher), an unchanged behavior. Verify each still reads correctly after 8.1–8.5.
- [x] 8.7 `grep -rn 'wm_deployers' plugins/hallow-windmill --include='*.md'` (excluding `__to_delete/`) returns **zero** hits.
