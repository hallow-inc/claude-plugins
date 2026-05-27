---
description: Drain pending entries from WINDMILL_LEARNINGS.md into the right skill/doc files, then mark them resolved. Walks the user through each `[ ]` entry, proposes a target, applies edits, updates the scratchpad.
allowed-tools: Read, Edit, Write, Grep, Glob, AskUserQuestion
---

# /wmill-drain-learnings

Promote captured Windmill learnings from `WINDMILL_LEARNINGS.md` (capture-as-you-go scratchpad) into the authoritative plugin files (skills/, docs/), then update the scratchpad to mark each promoted entry as done.

The scratchpad is meant to be **drained periodically** — entries accumulate during dev work via `/windmill-capture-learning`, then get promoted into reusable docs so future Claude sessions discover them.

## Procedure

### Step 1 — List pending entries

Read `${CLAUDE_PLUGIN_ROOT}/WINDMILL_LEARNINGS.md`. Extract every entry under `## Pending entries` whose Action line starts with `[ ]` (unchecked). For each, capture: date, topic, observation, evidence, proposed action, tags.

If the Pending section is empty: report "Scratchpad already drained — nothing to do." and stop.

### Step 2 — Map entries to target files

For each pending entry, propose a target file based on the topic and tags. Routing table:

| Topic / tag | Likely target |
|---|---|
| `trigger-schema`, `http-trigger`, `acl-gating` | `skills/triggers/SKILL.md` (Hallow gotchas) + `skills/triggers/references/http.md` if schema-level |
| `schedules`, `cron`, `run-as` (schedule context) | `skills/schedules/SKILL.md` (Hallow gotchas) |
| `resources`, `resource-type`, `secret-resource` | `skills/resources/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §5 if cross-cutting |
| `flows`, `inline-script`, `flow-sync` | `skills/write-flow/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §6 |
| `windmill-client`, `dispatch`, `wmill-api` (script-level) | `skills/write-script-bun/SKILL.md` (Hallow gotchas) |
| `duckdb`, `ducklake` | `skills/write-script-duckdb/SKILL.md` (Hallow gotchas) |
| `cli`, `sync`, `workspace`, `generate-metadata` | `skills/cli-commands/SKILL.md` (Hallow gotchas) |
| Error string / debugging recipe | `skills/windmill-debug/SKILL.md` classify table + `skills/windmill-debug/references/symptom-index.md` |
| Cross-cutting architectural rule (IAM, worker tags, naming convention) | `docs/patterns.md` (new section or extend existing) |
| Shared-tool authoring pattern | `docs/shared-tool-template.md` |

Domain-specific recipes (e.g. n8n migration quirks, single-product API auth) DO NOT belong in plugin skills — keep them in the Resolved section of the scratchpad for future audit reference, but do not promote.

### Step 3 — Confirm routing with user

Show the user a numbered list of pending entries with proposed targets. Single `AskUserQuestion` to confirm or override:

- Confirm: apply all proposed routings.
- Override specific entries: user names entry numbers + alt targets.
- Skip an entry: user marks specific entry as "leave pending" with reason.

### Step 4 — Promote each entry

For each confirmed routing:

1. Read the current target file. Find the right section (usually "Hallow gotchas" — append new gotcha to end if present; create the section if not).
2. Compose a self-contained promoted version: rule statement → evidence/symptom → fix → cross-link to related docs. Drop the "Action" / "Tags" / "Evidence" labels from scratchpad format — those are capture metadata, not doc content.
3. Apply via `Edit` (surgical append/insert; do not refactor surrounding content).
4. Update the symptom-index reference (`skills/windmill-debug/references/symptom-index.md`) if the entry includes a quotable error string. One row: symptom → diagnosis → fix-source.

### Step 5 — Mark promoted entries resolved in the scratchpad

For each promoted entry:
1. Move the entry from `## Pending entries` to `## Resolved (last 60 days)`.
2. Change `[ ]` → `[x] done YYYY-MM-DD` (today's date, ISO).
3. Replace the action body with a brief list of promotion targets (e.g. "promoted to `skills/triggers/SKILL.md` (Hallow gotchas) + `docs/patterns.md` §8").

If Step 3 marked an entry "leave pending", leave it untouched in the Pending section.

### Step 6 — Report

Output a one-screen summary:
- Entries drained: N
- Entries skipped (per user): M
- Files touched (deduped list)
- Suggest: "Run `/hallow-windmill:wmill-doctor` to verify nothing structurally broke, or invoke `plugin-dev:plugin-validator` agent for a quick check."

## Hard rules

- **Surgical edits only.** Append to existing "Hallow gotchas" sections; do not refactor the surrounding skill content.
- **Cross-link, don't duplicate.** When the same rule applies to multiple files, pick the canonical target and add a one-line cross-ref in the others (`See ${CLAUDE_PLUGIN_ROOT}/skills/<x>/SKILL.md "Hallow gotchas → <heading>"`).
- **Domain-specific learnings stay in the scratchpad's Resolved section.** Only promote rules that apply broadly to Windmill development on Hallow.
- **Never delete a pending entry** without promoting OR confirming with the user.
- **Today's date is ISO** — never paraphrase ("yesterday", "last week").

## Routing

| User says | Route to |
|---|---|
| "I learned X today, save it" | `/hallow-windmill:windmill-capture-learning` (captures TO scratchpad — opposite direction) |
| "Drain the scratchpad" / "Promote learnings" | This command |
| "The scratchpad has too much in it, where should X go?" | This command (Step 2 routing table answers it) |
