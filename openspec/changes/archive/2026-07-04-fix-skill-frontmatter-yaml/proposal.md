# Fix silently-broken skill frontmatter (YAML colon-space)

## Why

Nine `hallow-windmill` skills have a `description:` frontmatter value that contains an **unquoted YAML colon-space** — the substrings `NOT for: ` and `tag: ` sit inside the plain scalar. YAML reads `: ` (colon followed by space) as a mapping key/value separator, so the scalar fails to parse. `claude plugin validate` reports:

> frontmatter: YAML frontmatter failed to parse … At runtime this skill loads with empty metadata (all frontmatter fields silently dropped).

**Impact is not cosmetic.** With frontmatter dropped, the skill loads with no `name` and no `description` — so Claude Code cannot auto-trigger it from its description. These nine include core authoring skills (`write-flow`, `write-script-bun`, `triggers`, `schedules`, `raw-app`, `write-workflow-as-code`, `write-script-bunnative`, `write-script-duckdb`, `write-script-nativets`). The plugin's whole "tell Claude what you want, the right skill loads" model is broken for them.

Root cause is confirmed by exact correlation: **all 9 failing skills contain `: ` in the description value; all 13 passing skills do not.** Zero false positives, zero misses.

## What Changes

Convert the `description:` field of each of the 9 affected skills from a plain scalar to a **YAML folded block scalar** (`description: >-` with the text on indented continuation lines). A block scalar treats the entire body as literal text, so both the colon-space and the embedded double-quotes (`"webhook in windmill"` etc.) are handled with **zero escaping** and the wording is preserved verbatim.

Chosen over the alternatives:
- **Quoting the value** (`description: "…"`) — fails, because nearly every description already contains literal `"` quotes that would each need escaping. Fragile.
- **Rewording the `: ` away** — edits human content, and `tag: fargate` is a literal Windmill keyword whose exact form is a deliberate trigger phrase; rewording muddies it.

Scope is frontmatter YAML only. No skill body, no description *wording*, no behavior changes — the fix makes the existing descriptions actually load.

## Impact

- 9 skills regain working frontmatter → they auto-trigger from their descriptions again.
- `claude plugin validate ./plugins/hallow-windmill` drops from 9 errors to 0.
- Files touched (frontmatter only): `skills/{raw-app,schedules,triggers,write-flow,write-script-bun,write-script-bunnative,write-script-duckdb,write-script-nativets,write-workflow-as-code}/SKILL.md`.
- No spec/doc/body content changes.
