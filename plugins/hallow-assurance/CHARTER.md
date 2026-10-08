# hallow-assurance — Charter & Build Outline

> Owner: Brandon · Status: **Draft v0** · Last updated: 2026-10-05
>
> This document is the source of truth for what we're building and why. Design decisions that
> contradict it need a charter change first. Items marked `TODO(decide)` are open decisions.

---

# Part 1 — Charter

## Purpose

Give every Hallow codebase, in any language, a consistent and enforceable way to verify software in
proportion to how much its failure matters, and make coding agents operate inside those rules
rather than around them.

It is inspired by RTCA DO-178B (criticality levels, objectives tables, structural coverage
resolution, tool qualification), NASA NPR 7150.2D (software classes, tailoring with rationale),
NASA-STD-8739.8 (independent verification), NASA-STD-8739.9 (structured inspections), the
Power of 10 rules, and SQLite's testing practice. Formal-methods objectives draw on RTCA DO-333 and
AWS Cedar's verification-guided development (Lean model + differential random testing); simulation
objectives draw on TigerBeetle's VOPR and TigerStyle. We take their **objectives and evidence
model**, not their paperwork.

## What we're building

1. **`assure`** — a Go CLI and deterministic evaluator. Reads a repo's manifest, a versioned
   objectives catalog, normalized evidence, waivers, and provenance; decides pass/fail per
   objective; enforces a ratchet baseline.
2. **Language adapters** — external executables (`assure-adapter-<lang>`) speaking a small JSON
   protocol. They classify files, run tools, and emit normalized evidence. They never decide
   pass/fail.
3. **An objectives catalog** — language-neutral objectives (our Annex A), each with the levels it
   applies at, independence requirements, evidence type, thresholds, and per-language
   implementations or declared alternatives.
4. **A Claude Code plugin** (`hallow-assurance`) — hooks, subagents, skills, and commands that make
   agents follow the framework during development. Distributed through the existing Hallow plugin
   marketplace.

## Design invariants (non-negotiable)

1. **The plugin contains no language knowledge.** It calls `assure`; `assure` asks adapters.
   Adding a language must require zero changes to the plugin or the core.
2. **Pass/fail is deterministic.** No LLM participates in any gate decision. Model judgment may
   produce findings (inspections) but never verdicts.
3. **CI is the authority.** Hooks are early warning. A hook passing locally is never evidence; CI
   reruns everything.
4. **Adapters emit evidence; the evaluator decides.** No thresholds live in adapters.
5. **Fail closed.** Missing or malformed evidence fails the objective unless a valid waiver covers it.
6. **Agents cannot move the goalposts.** Agents may not edit the catalog, thresholds, manifest
   levels, waivers, baselines, provenance, formal-model challenge files, or gate config. Enforced
   by `assure guard` locally and by CI server-side: a PR that changes a protected file needs an
   approving review from someone other than the PR author. At levels the base manifest marks
   `human_review: optional`, CI detects and reports protected-file changes but does not prevent
   them; prevention there is local and best-effort (guard, Stop drift check).
7. **Every objective is computable from evidence.** If a check can't be computed, it doesn't belong
   in the catalog.
8. **Reproducible.** Every result is tied to a commit SHA and toolchain versions.
9. **Hooks are fast.** Per-edit hooks < 3s p95. Stop-hook fast check < 2 min p95 on the pilot repo.
10. **Standard formats first.** JUnit XML, LCOV/Cobertura, SARIF, and the Stryker
    mutation-testing-report schema before anything custom.

## Scope (v0 → v1)

In scope:

- `assure` CLI: `classify`, `context`, `guard`, `lint`, `check`, `evaluate`, `hook`, `record`,
  `explain`
- Manifest, catalog, waiver, provenance, evidence-index, and adapter-protocol schemas
- Go adapter (v0), TypeScript adapter (v1 — the language-agnosticism test), Lean adapter (v1,
  staged M7a–c)
- Claude Code plugin: hooks, four subagents, skills, commands
- CI integration: required status check, PR report, nightly runs
- Tool qualification fixtures (seeded-bug repos) for each adapter
- Pilot: the Hallow AI repo (Go)

## Non-goals (for now)

- Formal certification or regulatory compliance
- Building MC/DC instrumentation for languages that lack it (use declared alternatives)
- Supply-chain attestations (in-toto/SLSA) — planned post-v1
- Dashboards; cross-repo aggregation (evidence history goes to DuckLake post-v1)
- Supporting agent harnesses other than Claude Code in the plugin (the CLI and evaluator stay
  harness-neutral, so others can integrate later)
- Python/Rust adapters before the TypeScript adapter proves the design

## Success criteria

v0 (pilot, report-only):

- Pilot repo runs `assure check` locally via hooks and `assure evaluate` in CI
- Agents are blocked from editing protected files and from stopping with failing fast checks
- p95 hook latencies within invariant 9

v1:

- `assure evaluate` is a required check on the pilot repo, blocking on levels A–B
- The TypeScript adapter ships with **zero** diffs to `plugin/` and `internal/core/`
- Independence Tier 1 verified from provenance on every PR touching level A–B code
- Each adapter passes its qualification fixtures in CI
- Within six weeks of blocking mode: at least a few real bugs attributed to framework layers
  (`found-by:*` labels), mutation efficacy on changed code trending up, test count flat or down

## Risks

| Risk | Mitigation |
|---|---|
| Claude Code hook schema changes | All hook I/O goes through one `assure hook <event>` entrypoint that parses stdin; the rest of the CLI never sees hook JSON. Contract tests against recorded hook payloads. |
| Agents gaming gates | Invariant 6; guard protects `.assure/`; CI reruns; mutation (not coverage) as the quality signal |
| Stop-hook loops and cost | Retry cap in `.assure/state/`, honor the hook's stop-active flag, escalate to human after N attempts |
| Rule-pack false positives cause suppression fatigue | New rules ship advisory for one catalog version before becoming required |
| Mutation tooling maturity (esp. Go) | Adapter qualification fixtures; mutation starts advisory at level C |
| Framework becomes paperwork | Invariant 7; anything not computable gets cut |
| Evaluator bugs let bad code through | The repo applies the framework to itself at level B; qualification fixtures for the evaluator too |

---

# Part 2 — Build Outline

## Repository layout

Lives at `plugins/hallow-assurance/` inside the `hallow-claude-plugins` marketplace repo. OpenSpec
changes and specs live in that repo's root `openspec/`; the marketplace entry's `source` points at
`./plugins/hallow-assurance/plugin`. CI for invariant 6 is that repo's.

```
plugins/hallow-assurance/
  CHARTER.md
  CLAUDE.md
  assurance.yaml                 # dogfooding: this repo's own manifest
  go.mod
  cmd/
    assure/                      # CLI entrypoint
  internal/
    core/                        # language-neutral: manifest, catalog, waivers, classify, evaluate
    hookio/                      # ONLY place that knows Claude Code hook JSON
    evidence/                    # parsers: JUnit, LCOV, SARIF, mutation report
    adapterproto/                # adapter protocol client
    provenance/
    baseline/
  adapters/
    go/assure-adapter-go/        # assure-adapter-go (separate binary; dir named after the binary)
    typescript/…                 # assure-adapter-typescript (v1)
  catalog/                       # embedded in the assure binary; adopting repos carry no copy
    v0/objectives.yaml
  schemas/                       # JSON Schemas for every file format below
  plugin/                        # the Claude Code plugin
    .claude-plugin/plugin.json
    hooks/hooks.json
    bin/assure-hook              # shim: no manifest → exit 0; assure missing → exit 2; else exec
    agents/{implementer,verifier,inspector,pruner}.md
    skills/{assure-testing,check,bugfix,inspect}/SKILL.md   # no commands/: Claude Code lists it as legacy
  qualification/
    go/                          # seeded-bug fixture repos + expected results
  testdata/
    hooks/                       # recorded hook payloads for contract tests
```

## File formats (v0 sketches — formalize in `schemas/`)

**Manifest** — `assurance.yaml` in each adopting repo:

```yaml
version: 0
catalog: v0
languages: [go]                                    # runs assure-adapter-go from PATH
components:
  - path: internal/billing/**
    level: A
  - path: internal/chat/**
    level: B
    inputs: true                                   # optional: handles external input (fuzzed)
  - path: cmd/tools/**
    level: D
  - path: internal/ledger/**
    level: A
    formal:                                        # optional: Lean model of this component
      model: formal/Ledger                         # lake package
      challenge: formal/Ledger/Challenge.lean      # optional; trusted theorem statements; protected
      spec: formal/Ledger/Spec/**                  # optional; trusted definitions; protected
      link: conformance                            # conformance | drt | none
      code: [internal/ledger/**]                   # optional; changes here bring FM-LINK into scope
    dst:                                           # optional: deterministic simulation harness
      harness: ./sim/ledger
default_level: C
protected:                                         # optional: extra paths guard denies to agents
  - .github/workflows/**
```

Path → level resolution: a path takes the level of the matching glob with the most literal
(wildcard-free) segments. Ties go to the stricter level, and `assure lint` warns on every tie.
`internal/billing/**` (2 literals) beats `internal/**` and `**/billing/**` (1 each); if only the
latter two match, the stricter of their levels applies. Unmatched paths take `default_level`.

**Catalog objective** — `catalog/v0/objectives.yaml`:

```yaml
- id: VER-MUTATION-CHANGED
  title: Tests on changed code kill mutants
  source: [DO-178B 6.4.4.2 (alternative to MC/DC)]
  levels: { A: required, B: required, C: advisory }
  independence: { A: tier3, B: tier1 }
  evidence: mutation.report
  threshold: { A: 80, B: 65, C: 50 }
  implementations:
    go: gremlins
    typescript: stryker
  alternative_for: VER-STRUCT-MCDC
```

Optional `applies_to: formal | dst` restricts an objective to components that declare that block;
`applies_to: inputs` restricts it to components that declare `inputs: true`;
`applies_to: fix` restricts it to changes with a commit, between the ref and `HEAD`, carrying the
git trailer `Assure-Kind: fix`.

**Waiver** — `.assure/waivers.yaml` (human-only):

```yaml
- objective: VER-ROBUST-FUZZ
  scope: internal/chat/legacy/**
  rationale: Legacy parser scheduled for removal in Q4
  approver: hello-world-bfree
  expires: 2026-12-31
```

A waiver is active through its `expires` date (UTC calendar date). An active waiver suppresses
located findings of its objective in files matching `scope`, and suppresses an unlocated failure or
missing evidence only when `scope` matches every changed file at a level where the objective
applies. Any expired waiver fails `assure evaluate`, whatever its scope; the Stop fast check ignores
expired waivers because an agent cannot fix a protected file.

**Baseline** — `.assure/baseline.json` (human-only, written by `assure baseline`):

```json
{"version":0,"entries":[{"objective":"CODE-RESOURCE-BOUNDS","rule":"noctx","path":"internal/app/check.go","message":"os/exec.Command must not be called. use os/exec.CommandContext","count":1}]}
```

The ratchet for old code. A located `lint.sarif` finding matches an entry on objective, rule, path,
and message (never line); an entry suppresses at most `count` matches. Entries with fewer matches
than `count` are reported as removable. Schema: `schemas/baseline.schema.json`.

**Report** — `.assure/state/report.json`, written by `assure evaluate` (schema
`schemas/report.schema.json`): commit, base ref and SHA, evaluation date, catalog version, adapter
tool versions, and one status per applicable objective and language (`pass`, `fail`,
`advisory-fail`, `waived`), with details, applied waivers, and counts of baselined and
under-threshold results. The markdown summary is rendered from the report alone, so a stored
report reproduces it. For `lint.sarif`, catalog thresholds are ceilings on a result's
`properties.metric`.

**Mutation** — `mutation.report` evidence is a Stryker mutation-testing report. `Killed` and
`Timeout` mutants are detected; `Survived` and `NoCoverage` are undetected; compile and runtime
errors are invalid; `Ignored` is left out; `Pending` fails the objective. Per level, over mutants
outside active waivers' scopes, the score is detected ÷ (detected + undetected), and the catalog
threshold is a floor: the level passes when `100 × detected ≥ threshold × valid`, or when it has no
valid mutants. The baseline does not apply.

**Fail-on-base** — `test.fail_on_base` evidence is JUnit from running, against the base commit, the
top-level tests the change added or modified. It passes only when it has at least one case and every
case failed; a passing, skipped, or erroring case (including base code that does not compile with
the new tests) fails it.

**Fuzz** — `fuzz.run` evidence is JUnit from running each changed package's fuzz targets over their
committed seed corpus only, one suite per package, with an `assure.source` property per changed
source file. Each changed file in an `inputs: true` component fails when no suite names it, its suite
has no cases, or any case did not pass. Coverage-guided fuzzing runs nightly in its own workflow; a
crasher fails that workflow and is uploaded as an artifact, and feeds no objective (its result
depends on wall-clock time).

**Test budget** — `test.budget` evidence is a small JSON document (`schemas/test-budget.schema.json`;
no standard format exists for test counts) listing added cases per test file and added lines per
source file. Per level, the change may add max(`floor`, ⌈added source lines ÷ `lines_per_case`⌉)
cases, from the catalog's `budget` field (not `threshold`, which stays a 0–100 percentage). Over
budget fails the level; only an owner waiver justifies it.

**Coverage resolution** — `coverage.resolution` evidence is LCOV with `FN` records.
`.assure/coverage-resolutions.yaml` (human-only) holds `{path, function, resolution, rationale,
approver}` entries, `resolution` one of `missing-test`, `missing-requirement`, `dead`, `deactivated`.
Each added line with zero hits must fall in a function that has a resolution. Resolutions key on the
function name, never the line, and do not expire; one whose function is gone is reported as
removable. "Verdict" is reserved for gate decisions (invariant 2).

**Provenance** — `.assure/provenance/<session_id>.jsonl`, one committed file per session
(append-only, written only by `assure record`; protected from agent edits):

```json
{"v":0,"session":"7f3a2c9e","agent_type":"hallow-assurance:verifier","agent_id":"a4d2c8f1e0b3a297","tool":"Edit","path":"internal/chat/stream_test.go","role":"test","pre":"9f2c3b1d0e7a6f5c4b3a29180716253443526170","post":"a41e5d6c7b8a99887766554433221100ffeeddcc"}
```

`pre` is the file's blob hash (`git hash-object --path`) captured by the PreToolUse hook after guard
allows the call, held in `.assure/state/pending/<session>/<tool_use_id>.json`; `post` is captured by
`record` at PostToolUse, which appends the record and deletes the pending entry. `null` means
absent / deleted. Renames are delete + create. The Stop drift snapshot excludes
`.assure/provenance/`, because `record` appends to it mid-session; CI's append-only check covers it.

Threat model: a shortcut-taking agent (edits tests while implementing, edits through Bash), not a
forging one. Any agent running as the developer's OS user can reach every local secret, so local
provenance is detective, never preventive; CI is the authority.

CI check, per changed file in a level A–B component: the records must form an unbroken blob chain
`merge-base blob → pre→post → … → final blob`. Order comes from the hash links, not timestamps. Any
break is a **gap**: unattributed, never assumed human. A gap fails IND-VERIFIER-DISTINCT for that
file unless the PR has an approving review from someone other than the PR author. Gaps where the
file also changed on the base branch are merge-shaped; counting them to decide whether a 3-way
check is worth building is not built yet. CI also checks that a PR only appends to existing session files.

Any non-author approval counts; there is no approver list. GitHub rejects an author's approval of
their own PR, so an agent running on the author's credentials cannot produce one. The PR template
lists what a reviewer is attesting to. CI passes reviews to `assure evaluate --reviews <file>` as
`{author, head, reviews:[{login, state, commit}]}` (fetched with `gh api`), so the evaluator stays
offline. An approval counts only when it is the reviewer's latest review and was made on `head`.

A repo with no second reviewer may set `human_review: {B|C|D: optional}` in the manifest (never A,
which needs a named human). At those levels, gaps and protected-file changes pass and are listed as
unreviewed; role violations, `agent_id` conflicts, and rewritten provenance still fail. The value is
read from the manifest at the base ref, so a PR cannot relax its own gate.

Independence tiers (computed by the evaluator):

| Tier | Meaning | Evidence |
|---|---|---|
| tier1 | Distinct context: test-role files' chains written only by verifier-side agent types (`verifier`, `pruner`: the pruner edits tests and is not the code's author); source- and config-role files never by them; unclassified files (fixtures) by either; distinct `agent_id` (the main thread is its own identity) | provenance chains |
| tier3 | tier1 + approving review from someone other than the PR author | GitHub review API |

There is no tier2: a model reviewer is not code verification. Until `tier3-review` ships, the
evaluator still fails `tier2`/`tier3` closed, so level A cannot block.

`guard` denies main-thread (no `agent_type`) edits to test-role files in level A–B components, with
a message to spawn `hallow-assurance:verifier`, so the tier1 failure surfaces at edit time rather
than in CI.

**Adapter protocol v0** — subprocess, JSON on stdin/stdout:

```
assure-adapter-go describe            → {protocol:0, languages:["go"], claims:[..], patterns:{test:[..], generated:[..], fuzz_corpus:[..], config:[..]}, objectives:{<id>:{tool, fast?}}, reference?:"go"}
assure-adapter-go classify  <paths>   → {protocol:0, files:[{path, language, role}]}
assure-adapter-go lint      <paths>   → SARIF
assure-adapter-go run <objective> --changed-from <ref> --out <dir> → {protocol:0, evidence:[{type, path}], tool_versions:{...}}
assure-adapter-go reference          → Markdown (the testing reference named by describe.reference)
```

Every response except `lint` and `reference` is an object carrying `protocol: 0`, so each message is versioned on its own.
`claims` lists the files the adapter owns. A claimed file's role is the first matching pattern list
in the order `generated`, `fuzz_corpus`, `test`, `config`, and otherwise `source`.
`fast: true` marks an objective cheap enough for the Stop-hook fast check. Cost belongs to the tool
the adapter picks, so it lives here, not in the catalog. `run` exits 0 whenever it produced a valid
response, including when the tools found failures.
Schemas: `schemas/adapter-{describe,classify,run}.schema.json`; `lint` is validated against SARIF 2.1.0.

## Starter catalog (v0)

| ID | Intent | A | B | C | D |
|---|---|---|---|---|---|
| CODE-ZERO-WARNINGS | Compiler/vet/linters clean (ratcheted for old code) | req | req | req | adv |
| CODE-RESOURCE-BOUNDS | Bounded reads, retries, loops, buffers (P10-2, P10-3) | req | req | adv | – |
| CODE-CHECK-RETURNS | No dropped errors/promises (P10-7) | req | req | req | adv |
| CODE-COMPLEXITY | Cyclomatic complexity ≤ 15 per function (P10-4) | req | req | adv | – |
| CODE-NO-UNSAFE | No unsafe/reflect/eval outside allow-list (P10-8, P10-9) | req | req | adv | – |
| VER-TESTS-PASS | Tests pass with race/shuffle where supported | req | req | req | req |
| VER-TRACE-REQ | Every requirement ID has tests; level A: every test traces to a requirement | req | adv | – | – |
| VER-MUTATION-CHANGED | Mutation threshold on changed code | req | req | adv | – |
| VER-FAIL-ON-BASE | Bugfix tests fail on base commit | req | req | req | adv |
| VER-TEST-BUDGET | New test cases within budget or justified | req | req | req | adv |
| VER-ROBUST-FUZZ | Fuzz targets exist and ran for input-handling code | req | req | adv | – |
| VER-COVERAGE-RESOLUTION | Uncovered code has an approved resolution (missing test / missing req / dead / deactivated) | req | adv | – | – |
| IND-VERIFIER-DISTINCT | Verification authored by a different agent/human than implementation | tier3 | tier1 | – | – |
| CFG-PROTECTED | No agent-authored changes to protected files | req | req | req | req |
| FM-COMPLETE | Lean model builds with `--wfail`; zero `sorry`; axiom-audit reports no violations | req | req | adv | – |
| FM-AXIOMS | Axioms used ⊆ {propext, Classical.choice, Quot.sound}; native-evaluation axioms need a waiver | req | req | adv | – |
| FM-RECHECK | Independent kernel re-check passes (`leanchecker` at every level; `lake comparator` later, once Lean ≥ 4.35 runs on Linux) | req | req | adv | – |
| FM-TRACE | Each requirement in `.assure/requirements.yaml` cites challenge theorems that build, use standard axioms only, and prove by reference over the protected `Spec` library | req | req | adv | – |
| FM-LINK | Linked tests named in `.assure/requirements.yaml` pass and each reports ≥ N inputs (N = 100), same commit | req | req | adv | – |
| VER-DST-REPLAY | A failing seed replays to an identical trace digest | adv | adv | adv | – |
| VER-DST-FAULTS | Every declared fault class fired at least once across the seed set | adv | adv | adv | – |
| VER-DST-LIVENESS | At least one liveness run (faults heal or freeze; core must converge) | adv | adv | adv | – |
| VER-DST-BUDGET | Aggregate simulated time ≥ T at a minimum acceleration ratio | adv | adv | adv | – |

FM-* objectives apply only to components that declare `formal:`; VER-DST-* only to components that
declare `dst:`; FM-LINK also applies to changed files matching a component's `formal.code` globs.
VER-DST-* are advisory in v1. FM-* gate as the table states once their milestone ships (M7a for
COMPLETE, AXIOMS and RECHECK; M7b for TRACE; M7c for LINK) and are advisory until then. A `formal:`
component without `challenge` or `spec` fails FM-TRACE closed. Lean evidence without FM-LINK counts
only toward design- and spec-level objectives, never code-level ones: a proof about a model is not
a proof about the code. FM-LINK is law conformance: linked Go tests check that the code obeys each
mirrored theorem's law over generated or enumerated inputs. It is not a refinement proof, and a
theorem with no linked test says nothing about the code. Differential testing that executes the
Lean model against the code (`link: drt`) is deferred.

## Claude Code plugin design

All hooks route through `plugin/bin/assure-hook` → `assure hook <event>`, which reads the hook JSON
from stdin, calls the relevant core function, and translates the result into exit codes / JSON
output.

Claude Code treats a hook timeout, a missing binary (exit 127), and any exit code other than 0 or 2
as non-blocking. The local guard is therefore best-effort; CI (invariant 3) is the enforcement. To
keep local behavior as close to fail-closed as the harness allows:

- The shim exits 0 when no `assurance.yaml` is found above `cwd` (repo not adopted) and exits 2 when
  `assure` is missing or `assure hook protocol` prints a different hook-protocol number than the
  shim expects. The shim checks a protocol number rather than a release version because there is no
  release channel yet. The hook protocol is 2: M4b added the `subagent-stop` event, which an older
  `assure` would reject with exit 2 and so block every subagent stop. For `stop` and `subagent-stop`
  the shim allows with a `systemMessage` instead, because the agent cannot fix the install.
- `internal/hookio` recovers from every internal error and emits a deny/block decision.
- Every hook declares an explicit `timeout`; `guard` is a pure in-memory decision. It reads file
  roles from each adapter's `describe` globs, cached in `.assure/state/adapters.json` and keyed by
  the adapter executable's SHA-256, so no adapter runs at edit time while the cache is fresh.
- Edits made through Bash bypass the `Edit|Write` matcher. SessionStart records hashes of protected
  files in `.assure/state/`; the Stop check blocks on any drift. CI checks the PR diff the same way.
  SessionStart also fires on `clear` and `compact`, so it never replaces a session's existing
  snapshot.
- `.claude/settings.json` and `.claude/settings.local.json` under the manifest root are protected by
  `internal/hookio` in addition to the manifest's set, because editing them can disable the hooks.
  They live in `hookio` because they are Claude Code concepts; the core stays harness-neutral.
- The manifest is resolved from `tool_input.file_path` / `cwd`, not `CLAUDE_PROJECT_DIR`, which stays
  at the session root inside worktrees.

| Event | Matcher | Calls | Effect |
|---|---|---|---|
| SessionStart | – | `context` | Injects level map + applicable objectives for the working area |
| SessionStart | – | `guard --snapshot` | Records protected-file hashes for the Stop drift check |
| PreToolUse | `Edit\|Write\|NotebookEdit` | `guard` | Blocks protected files; enforces subagent role rules keyed on the hook input's `agent_type` (e.g. verifier can't edit source) |
| PostToolUse | `Edit\|Write` | `lint` | Fast per-file rule-pack findings fed back to the agent |
| PostToolUse | `Edit\|Write\|NotebookEdit` | `record` | Appends provenance (never blocks; a failure is a CI gap) |
| Stop | – | `check --changed --fast` | Blocks stopping while fast objectives fail or protected files drifted; retry cap (own counter + `stop_hook_active`) then escalate |
| SubagentStop | `^hallow-assurance:(verifier\|inspector)$` | `check --role <agent>` | Verifier must leave passing tests (`VER-TESTS-PASS` only); inspector must emit one fenced `sarif` block in its final message, which the hook reads from `last_assistant_message`, validates, and writes to `.assure/state/inspections/` |

Plugin subagent names are plugin-scoped (`hallow-assurance:verifier`), so the SubagentStop matcher is
an anchored regex; an unanchored `verifier|inspector` never matches.

Subagents: **implementer** (source only), **verifier** (tests/fuzz/properties only), **inspector**
(read-only, checklist → SARIF), **pruner** (deletes/merges tests, must cite mutation report).

Role rules are enforced by `guard`, keyed on the `agent_type` field that PreToolUse carries for
subagent tool calls. Claude Code ignores `hooks` and `permissionMode` in plugin subagent frontmatter,
so frontmatter cannot enforce roles; `tools` / `disallowedTools` stay as defense-in-depth.

Skills: one generic `assure-testing` skill (philosophy, fail-first loop, coverage resolutions,
how to read `assure` output, designing for deterministic simulation, formal model + differential
testing). Per-language references ship inside each adapter (`assure-adapter-<lang> reference`), so the
plugin carries no language text; `assure reference <lang>` prints one.

Commands: `/hallow-assurance:check`, `/hallow-assurance:bugfix <issue|seed>`, `/hallow-assurance:inspect`,
shipped as user-invoked skills. Claude Code always prefixes plugin skills with the plugin name.

## Milestones

Each milestone ends with its acceptance criteria met, tests passing, and, from M3a on, this repo
passing its own `assure evaluate`. M0–M2 gate on `go vet`, `golangci-lint`, and
`go test -race -shuffle=on`. Milestones ship in dependency order, not numeric order; numbers are
never reassigned, and new work takes a letter suffix.

**M0 — Scaffolding & schemas**
- Repo layout, `go.mod`, CI, `CLAUDE.md`, `assurance.yaml`
- JSON Schemas for manifest, catalog, waiver, provenance, adapter protocol
- ✅ Schema validation tests with valid/invalid fixtures for each format

**M1 — Core: classify, context, guard**
- Manifest + catalog loaders; glob → component → level resolution
- Adapter protocol client; `assure-adapter-go describe|classify`
- `assure classify`, `assure context`, `assure guard` (protected paths + role rules)
- ✅ Property tests for path→level resolution (most-specific match wins, deterministic)
- ✅ Property tests: any generated path under a protected glob is denied; any generated
  (agent_type, path role) pair gets the decision the role table specifies. Example tests only for
  named regressions.

**M2 — Plugin v0 (report-only pilot)**
- `internal/hookio` + `assure hook` with contract tests against recorded payloads
- `hooks.json`: SessionStart, PreToolUse guard, Stop fast check (build, vet, lint, `test -race` on
  changed packages)
- Retry cap / escalation for Stop
- Pulled forward from M3 so the Stop check has evidence to decide from: JUnit and SARIF parsers
  (minimal), Go adapter `run` for `VER-TESTS-PASS` and `CODE-ZERO-WARNINGS`, `describe` `fast`
- Install in the pilot repo (this repo first), report-only in CI
- ✅ Latency within invariant 9 on the pilot repo
- ✅ Manual session: agent edits a protected file → blocked; agent stops with failing test → blocked

**M3a — Evaluator, waivers, baseline**
- JUnit keeps failure text; SARIF results carry an optional numeric `properties.metric`
- Go adapter `run` for the other `CODE-*` objectives: rule packs with adapter-written lint config,
  complexity as raw metrics; `CODE-ZERO-WARNINGS` reports every finding (the ratchet is the baseline)
- `assure evaluate` → `report.json` + markdown job summary; waivers with expiry; baseline ratchet;
  `assure baseline`; the fast check applies the baseline and active waivers
- CI runs `assure evaluate` (fails the job, not yet a required check)
- ✅ Fail-closed: missing evidence fails; expired waiver fails

**M3b-1 — Mutation, fail-on-base**
- Parser: Stryker mutation report
- Go adapter `run` for mutation (Gremlins, changed lines, converted to Stryker) and fail-on-base
- `applies_to: fix`, from the `Assure-Kind: fix` commit trailer
- ✅ Three `evaluate` runs on one commit agree on `VER-MUTATION-CHANGED`

**M3b-2 — Remaining evidence, required check**
- Parser: LCOV
- Go adapter `run` for: fuzz (seed corpus in PR, time-boxed nightly), test budget;
  `VER-COVERAGE-RESOLUTION`
- CI: an always-running `assure-gate` job is the required check, so a path-filtered skip never
  leaves an unrelated PR Pending
- ✅ Required status check on the pilot repo (blocking levels A–B)

**M4a — Subagents, provenance, independence**
- Four subagents; role rules in `guard` keyed on `agent_type`; frontmatter `tools` /
  `disallowedTools` as defense-in-depth
- `assure record` + IND-VERIFIER-DISTINCT (tier1) and CFG-PROTECTED evaluation; `--reviews` input;
  manifest `human_review`
- ✅ Evaluator rejects a PR where implementer == verifier on level B code

**M4b — Skills, commands**
- `assure-testing` skill + Go reference embedded in the adapter (`describe` names it); `/hallow-assurance:*` commands
- SubagentStop `check --role` for verifier and inspector

**M4c — Solo adoption**
- `release-channel`: `go install` of `assure` and `assure-adapter-go` from one
  `plugins/hallow-assurance/v0.N.x` tag; reusable evaluate workflow with a required `version` input;
  README licensing consent for personal repos; README adoption order: the manifest PR
  (`assurance.yaml`, `.assure/**`) merges before the PR that adds the evaluate workflow, so the
  first evaluated base already carries `human_review`. The evaluator has no exception for a base
  without a manifest
- `assure-init`: prints a suggested manifest (packages, `inputs: true` candidates, baseline counts per
  objective); never writes protected files and never proposes `formal:`
- ✅ `hello-world-bfree/g` adopts at level B with no second reviewer

**M4d — Evidence loops**
- `fuzz-crasher-loop`: a nightly fuzz crasher becomes a committed seed and a
  `/hallow-assurance:bugfix` PR
- `mutation-sweep`: scheduled, advisory whole-repo mutation run that feeds the verifier and pruner;
  never a gate
- `orchestration-skills`: when VER-TEST-BUDGET fails on a tests-only branch, `assure-testing` and the
  verifier hand the owner a ready-to-paste campaign waiver (7-day expiry)
- Acceptance criteria are set in each change's grill

**M5 — Qualification**
- `qualification/go/`: seeded-bug fixtures with expected outcomes (mutants that must be killed,
  known coverage, known lint findings)
- Adapter must pass qualification in CI before release, from v1. Pre-M5 `v0.x` tags are unqualified.
- ✅ A deliberately broken adapter build fails qualification

**M6 — TypeScript adapter (agnosticism proof)**
- `assure-adapter-typescript` + `typescript.md` reference + qualification fixtures
- ✅ **Zero diffs** to `plugin/` and `internal/core/` in the M6 PR. Any needed change is a design bug:
  fix the abstraction first in a separate PR, then land the adapter.

**M7a — Lean adapter, stage 1**
- `assure-adapter-lean`: `lake build --wfail` and axiom-audit → `lean.build` (FM-COMPLETE);
  axiom-audit `--json` → `lean.axioms` (FM-AXIOMS); `leanchecker` → `lean.recheck` (FM-RECHECK)
- `formal.challenge` optional in the manifest schema; `lean.*` readers in `internal/evidence` and
  `internal/app`; catalog rows required at A–B
- Qualification fixtures: a seeded `sorry`, a seeded `native_decide`, and a custom axiom must each fail
- ✅ Evidence about a Go component comes from a non-Go adapter with zero diffs to `plugin/` and
  `internal/core/`

**M7b — FM-TRACE**
- Protected `.assure/requirements.yaml` (schema + fixtures) maps requirement IDs to challenge
  theorems; `formal.spec` names the protected `Spec` library
- Lean adapter compiles the challenge → `lean.trace` (per theorem: built, refs, axioms,
  `untrusted_refs`)
- ✅ A challenge theorem whose statement uses a constant outside `Spec` and the toolchain fails
  FM-TRACE

**M7c — FM-LINK**
- `requirements.yaml` entries gain `link:` test ids; `formal.code` globs scope FM-LINK
  (`applies_to: formal_code`)
- Go adapter runs the linked tests and reads the `assure-link inputs=<n>` marker → `link.run`
  (renamed from `drt.run`)
- ✅ A linked test without the input marker fails FM-LINK

**M8 (post-v1) — DST runner**
- Seed runner (budget, timeout, concurrency) and seed-record store with failing-first retention
- Replay check: run a seed twice, compare trace digests → VER-DST-REPLAY
- ✅ A deliberately nondeterministic harness fails VER-DST-REPLAY

**Post-v1:** attestations, DuckLake evidence history, Python and
Rust adapters, cross-repo reporting, Ziggy authoring front-end for manifest/catalog (JSON Schema
stays authoritative) once a stable Go implementation and JSON mapping exist.

## Open decisions

- `TODO(decide)` Catalog and waiver approvers per level
- Distribution: resolved to `go install …/cmd/assure@<tag>` and
  `…/adapters/go/assure-adapter-go@<tag>` from `plugins/hallow-assurance/v0.N.x` tags (one tag
  versions both). A hook-protocol bump forces a minor bump. No prebuilt binaries until a non-Go
  adopter needs them.
- `TODO(decide)` Level assignments for the pilot repo's packages
- Retry cap for the Stop hook: resolved to 3 for v0 (M2), then allow the stop and escalate to the
  human with a `systemMessage`. It sits under Claude Code's own 8-block cap, so the escalation
  message always comes from `assure`. M4 (`stop-hook-no-dead-loops`): the cap also counts blocks
  per failure fingerprint for the whole session, so an unchanged failure stops blocking after 3
  even across prompts; failures outside the agent's reach (missing snapshot, adapter that cannot
  start, `assure` itself missing) allow the stop with a `systemMessage` on the first attempt.
- Level A cannot block until `tier3-review` ships (tier3 = tier1 + non-author approving review;
  the evaluator fails `tier2`/`tier3` closed today). Level B (tier1) is unaffected.
- `TODO(decide)` Whether FM-LINK + FM-COMPLETE may substitute for VER-MUTATION-CHANGED at level A
  (`alternative_for`). Leaning no for v1.

## Starting the first Claude Code session

1. Add `CHARTER.md` and `CLAUDE.md` under `plugins/hallow-assurance/` in `hallow-claude-plugins`.
2. Start in plan mode. Prompt:
   > Read CHARTER.md and CLAUDE.md. Propose a plan for M0 only: file list, schemas, and the tests
   > you'll write. Don't write code until I approve the plan.
3. After M0 lands, repeat per milestone. Keep each milestone to its own PR.
4. Check the current Claude Code hooks and plugin docs before M2 — field names and events have
   been changing: https://code.claude.com/docs/en/hooks
