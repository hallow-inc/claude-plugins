# Tasks — fix-skill-frontmatter-yaml

Frontmatter-only change. For each skill, convert the `description:` plain scalar to a folded block scalar (`>-`), preserving the wording **verbatim**. Do not touch `name:`, the skill body, or the description text itself.

Pattern (per file):
```
---
name: <unchanged>
description: >-
  <the existing description text, wrapped across indented lines,
  word-for-word — the ': ' in "NOT for:" / "tag:" is now safe>
---
```

## 1. Convert the 9 affected skills

- [x] 1.1 `skills/raw-app/SKILL.md` — description has `NOT for: `.
- [x] 1.2 `skills/schedules/SKILL.md` — `NOT for: `.
- [x] 1.3 `skills/triggers/SKILL.md` — `NOT for: `.
- [x] 1.4 `skills/write-flow/SKILL.md` — `tag: ` and `NOT for: `.
- [x] 1.5 `skills/write-script-bun/SKILL.md` — `tag: ` and `NOT for: `.
- [x] 1.6 `skills/write-script-bunnative/SKILL.md` — `NOT for: `.
- [x] 1.7 `skills/write-script-duckdb/SKILL.md` — `tag: `.
- [x] 1.8 `skills/write-script-nativets/SKILL.md` — `NOT for: `.
- [x] 1.9 `skills/write-workflow-as-code/SKILL.md` — `NOT for: `.

## 2. Verify

- [x] 2.1 `claude plugin validate ./plugins/hallow-windmill` reports **0** frontmatter errors (was 9).
- [x] 2.2 For each converted skill, confirm the parsed `description` matches the original text word-for-word (block-scalar folding collapses newlines to spaces — no accidental wording change, no dropped clause).
- [x] 2.3 Confirm `name:` is unchanged in all 9 (block-scalar conversion applied only to `description:`).
- [x] 2.4 Sanity: the 13 already-passing skills are untouched (`git status --short` shows only the 9 target SKILL.md files modified).
