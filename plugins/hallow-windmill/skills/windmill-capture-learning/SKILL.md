---
name: windmill-capture-learning
description: Capture a Windmill behavior, gotcha, or plugin-doc-contradiction discovered during development. Writes both a memory entry (tag windmill-plugin-update) and a scratchpad bullet in WINDMILL_LEARNINGS.md. Use after any session where Windmill behavior surprised you, a doc was wrong, or a CLI/API quirk needed a workaround. Triggers on phrases like "capture this learning", "windmill quirk", "doc was wrong", "this is a gotcha".
---

# Capture a Windmill learning

Use this skill any time you discover something about Windmill that should be
reflected in the plugin (skills, patterns, folders-groups) but isn't yet.

## When to use

- Schema fields turn out to be read-only / server-set when docs imply they're writeable
- CLI flag doesn't work as documented
- `wmill sync push` produces unexpected diffs
- Secret resource auto-link creates surprising state
- HTTP trigger ACL gating surprises a non-admin caller
- Variable / resource path collisions
- Lockfile drift / lineage conflicts on path moves
- Anything that, on the next session, you'd want a colleague to know without re-discovering

If the answer is "yes, future-me would want to know this," capture.

## What to capture

For each learning, gather:

1. **Observation** — one-sentence summary: what surprised you, OR what doc X
   says vs what actually happens.
2. **Evidence** — error message verbatim, dry-run output, API response, or
   commit URL. Concrete, not paraphrased.
3. **Affected docs** — which plugin file(s) need updating:
   `skills/<name>/SKILL.md`, `docs/<name>.md`, or `WINDMILL_LEARNINGS.md` only
   (if too small to promote).
4. **Action** — specific edit needed, or "investigate further" if not yet
   clear how to fix.
5. **Tags** — choose from: `trigger-schema`, `script-schema`, `flow-schema`,
   `cli-bug`, `sync-push`, `secret-resource`, `acl-gating`, `lockfile`,
   `lineage`, `wmill-api`, or add a new short tag.

## How to capture

Do BOTH:

### 1. Memory entry

Use `memory_store` with these fields:

```
type:   "insight"
scope:  "project"
title:  "Windmill: <short topic>"  e.g. "Windmill: HTTP trigger permissioned_as is read-only"
tags:   ["windmill-plugin-update", "<your topic tag>"]
content: <use the structured body below>
```

Content body:

```
**Observation:** <one sentence>

**Evidence:**
<error msg / dry-run / API response / commit URL>

**Affected docs:**
- plugins/hallow-windmill/<path>

**Action:** <specific edit needed>
```

### 2. Scratchpad append

Append to `~/dev/hallow-claude-plugins/plugins/hallow-windmill/WINDMILL_LEARNINGS.md`
under `## Pending entries`:

```markdown
### YYYY-MM-DD — <short topic>
- **Observation:** ...
- **Evidence:** ...
- **Action:** [ ] update <doc path>
- **Tags:** <comma-separated>
```

Keep the bullet terse. Detail lives in the memory entry. Scratchpad is the
human-glanceable index of "what's pending".

## End-of-session drain (optional but encouraged)

When closing out a working session, query open captures:

```
memory_query(tags=["windmill-plugin-update"], statuses=["pending"])
```

For each, decide:

- **Promote to docs:** edit the affected plugin file, then mark memory + scratchpad as resolved (memory status=done, scratchpad `[x]` + date + commit).
- **Defer:** leave as-is, will revisit.
- **Discard:** if turned out to be wrong or trivial.

## Trigger phrasings

Auto-invoke this skill when the user says any of:
- "capture this learning"
- "we should write this down"
- "windmill gotcha"
- "doc was wrong"
- "add to plugin"
- "this is non-obvious"
- "/windmill-capture-learning"
