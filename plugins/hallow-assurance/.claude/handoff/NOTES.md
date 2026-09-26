# NOTES

## 2026-09-26 18:29 EDT — M0 close-out (PR #1) + M1 plan and apply

### Decisions (artifact holds detail)
- No CODEOWNERS, no branch protection: approver = any non-author GitHub review; PR template lists
  what approval attests. → CHARTER.md Provenance + invariant 6; archived M0 design D8.
- gitignore: `/assure`, `**/testdata/rapid/`, `**/.claude/state/` (keep `testdata/fuzz/` tracked).
- validate.yml secret regex requires a token char after `token=` (placeholders `<TOKEN>` false-positived). → M0 design Risks.
- M1: guard uses cached `describe` globs keyed by adapter SHA-256; catalog embedded; manifest
  `languages` required; charter milestone exit "from M3 on". → M1 proposal/design D1–D11.
- M1: describe gains `claims`; role precedence generated > fuzz_corpus > test > config > source;
  all of `.assure/` protected incl. state (cache poisoning). → M1 design D6, D8.
- M1 impl choices not in artifacts: cache-write failure is non-fatal (guard ignores, context warns);
  `classify` errors if an adapter returns an unrequested path.

### Dead ends (settled)
- jq to edit schemas: reflows the compact house style. Hand-edit with Edit tool.
- `testdata/rapid/` gitignore pattern (middle slash) does not match nested dirs; needs `**/`.
- Fake-adapter tests with `PATH=<tempdir>` only: `head`/`sleep` vanish → exit 127. Append `/usr/bin:/bin`.

### Open questions
- Unverified: GitHub pre-fills PR template only from the default branch (asserted, not checked).
  Blocks nothing; checked after PR #1 merges.
- Dogfood manifest root is `plugins/hallow-assurance/`, so `protected` cannot reach repo-root
  `.github/workflows/`. Needs a repo-root manifest someday (M1 design Risks).
- Agent editing `.claude/settings.json` to drop hooks — M2 concern.

### Searched, found nothing
- memory-mcp "hallow-assurance guard classify adapter M1" (insight/context/fact) → no entries.

### One-off claims (skipped from memory; re-judge if needed)
- `go build ./adapters/go` writes a binary named `go` into cwd — standard Go naming; `go help build` answers.
- Schema mutation test ~3 s without `-race`, ~40 s with — a test run answers.
- `openspec validate --specs --strict` fails 5 old windmill/plugin specs (TBD Purpose) — the command answers.
- PR #1 = hallow-inc/claude-plugins#1; validate.yml had never run before it — `gh run list` answers.
- Guard p95 19.4 ms on this repo — re-measurable; recorded in RESUME for the PR body.
