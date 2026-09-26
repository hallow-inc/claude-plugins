# RESUME — hallow-assurance M1 (`m1-core-classify-context-guard`)

Context at writing: ~29%. Supersedes the repo-root `.claude/handoff/RESUME.md` (M0, now stale:
M0 is archived and PR #1 is open).

## Current task
OpenSpec change `m1-core-classify-context-guard` (repo root
`openspec/changes/m1-core-classify-context-guard/`), applied via `/opsx:apply`. 21/22 tasks done.
Branch `feat/assurance-m1`, cut from `feat/sw-assurance-util` (PR #1, unmerged). Nothing committed.

## Done and VERIFIED (do not redo)
- M0: archived to `openspec/changes/archive/2026-09-26-m0-scaffold-schemas/`; 6 `assure-*` main
  specs synced into `openspec/specs/`. PR #1 (hallow-inc/claude-plugins#1) both workflows green on
  `44d45dc`; body filled from template. CODEOWNERS removed by user decision (design D8).
- M1 tasks 1.1–5.2, verified with `cd plugins/hallow-assurance && go vet ./... &&
  golangci-lint run ./... && go test -race -shuffle=on -count=1 ./...` → 0 lint issues, 8 packages ok.
- Guard latency measured: p95 19.4 ms / 50 runs on this repo (record in M1 PR body).
- Invariant greps clean: no language strings in `internal/core` or `cmd/assure` impl; adapter
  imports nothing from `internal/`; no hook-JSON parsing anywhere.

## Written but NOT verified
- 5.3: needs CI green on the M1 PR. Left unticked on purpose.

## Exact next step
1. User reviews, commits, pushes `feat/assurance-m1`; opens PR (base `feat/sw-assurance-util`, or
   `master` after #1 merges). PR body must flag protected-file edits: `schemas/manifest.schema.json`,
   `schemas/adapter-describe.schema.json`, new `schemas/adapter-cache.schema.json`,
   `assurance.yaml`, `CHARTER.md`; plus the 19.4 ms p95.
2. On green CI: tick 5.3 in `openspec/changes/m1-core-classify-context-guard/tasks.md`, then
   `/opsx:archive m1-core-classify-context-guard`.
3. After #1 merges: open any PR and confirm GitHub pre-fills `.github/pull_request_template.md`
   (M0 task 5.3's deferred check).
4. Next milestone M2 (plugin v0: hookio, hooks.json, guard --snapshot, Stop check) — plan first.

## Files in flight (all under plugins/hallow-assurance/, uncommitted)
| Path | State |
|---|---|
| `internal/core/*` (glob, load, catalog, manifest, levels, roles, guard, context + tests) | new, verified |
| `internal/adapterproto/*` (client, cache + tests) | new, verified |
| `adapters/go/main.go`, `main_test.go` | new, verified |
| `catalog/catalog.go`, `catalog_test.go` | new, verified |
| `cmd/assure/{guard,context,classify,workspace}.go`, `cli_test.go`; `main.go`, `main_test.go` | new/edited, verified |
| `e2e_test.go` | new, verified |
| `schemas/*.schema.json`, `schemas.go`, `properties_test.go`, `schemas_test.go`, `testdata/**` | edited, verified |
| `CHARTER.md`, `assurance.yaml`, `repo_test.go` | edited, verified |

## Do not touch
- `plugins/hallow-assurance/__to_delete/` — scratch (rapid failfiles, stray `go-binary-*`); user deletes.
- `.assure/state/adapters.json` in this repo — written by `assure context` during latency check; gitignored.
- `openspec/` is in `.git/info/exclude` — change artifacts are local-only by user choice.
- `validate.yml` push trigger on `main` — pre-existing bug, out of scope.
- 5 old main specs failing `openspec validate --specs --strict` (TBD Purpose) — not this change's.
- `plugins/hallow-windmill/` — unrelated.
