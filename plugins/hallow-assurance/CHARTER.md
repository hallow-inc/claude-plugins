# hallow-assurance — Charter & Build Outline

> Owner: Brandon · Status: **Draft v0** · Last updated: 2026-10-27
>
> This document is the source of truth for what we're building and why. Design decisions that
> contradict it need a charter change first. Items marked `TODO(decide)` are open decisions.

---

# Part 1 — Charter

## Purpose

Give every Hallow codebase, in any language, a consistent and enforceable way to verify software in
proportion to how much its failure matters, and help coding agents meet that bar while they work.
It verifies the code through tests, mutation, fuzzing, and Lean proofs; it does not restrict who
edits what.

It is inspired by RTCA DO-178B (criticality levels, objectives tables, structural coverage
resolution, tool qualification), NASA NPR 7150.2D (software classes, tailoring with rationale),
NASA-STD-8739.8 (independent verification), NASA-STD-8739.9 (structured inspections), the
Power of 10 rules, and SQLite's testing practice. Formal-methods objectives draw on RTCA DO-333 and
AWS Cedar's verification-guided development (Lean model + differential random testing); simulation
objectives draw on TigerBeetle's VOPR and TigerStyle. We take their **objectives and evidence
model**, not their paperwork.

## What we're building

1. **`assure`** — a Go CLI and deterministic evaluator. Reads a repo's manifest, a versioned
   objectives catalog, normalized evidence, waivers, and coverage resolutions; decides pass/fail
   per objective.
2. **Language adapters** — external executables (`assure-adapter-<lang>`) speaking a small JSON
   protocol. They classify files, run tools, and emit normalized evidence. They never decide
   pass/fail.
3. **An objectives catalog** — language-neutral objectives (our Annex A), each with the levels it
   applies at, evidence type, thresholds, and per-language
   implementations or declared alternatives.
4. **A Claude Code plugin** (`hallow-assurance`) — two hooks, optional subagents, and skills that
   tell an agent what the framework expects at session start and check its work before it stops.
   Distributed through the existing Hallow plugin marketplace.

## Design invariants (non-negotiable)

1. **The plugin contains no language knowledge.** It calls `assure`; `assure` asks adapters.
   Adding a language must require zero changes to the plugin or the core.
2. **Pass/fail is deterministic.** No LLM participates in any gate decision. Model judgment may
   produce findings (inspections) but never verdicts.
3. **CI reruns everything.** `assure evaluate` decides the same way on a laptop and in CI, and the
   hooks are early warning. A repo that makes the `assure-gate` job a required check gets its
   verdict from a machine the agent cannot write to. A developer can adopt hallow-assurance alone,
   without the team or project adopting it.
4. **Adapters emit evidence; the evaluator decides.** No thresholds live in adapters.
5. **Fail closed.** Missing or malformed evidence fails the objective unless a valid waiver covers it.
6. **The gate measures the code, not the developer.** Nothing restricts who edits what. A weakened
   test still has to survive the objectives: mutation, not coverage, is the quality signal on
   changed code; a fix's tests must fail on the base commit; an expired waiver fails evaluation.
   An edit to the catalog, a threshold, a waiver, or the manifest shows in the PR diff.
7. **Every objective is computable from evidence.** If a check can't be computed, it doesn't belong
   in the catalog.
8. **Reproducible.** Every result is tied to a commit SHA and toolchain versions.
9. **Hooks are fast.** SessionStart < 3s p95. Stop-hook fast check < 2 min p95 on the pilot repo.
10. **Standard formats first.** JUnit XML, LCOV/Cobertura, and the Stryker
    mutation-testing-report schema before anything custom.

## Scope (v0 → v1)

In scope:

- `assure` CLI: `audit`, `check`, `classify`, `context`, `evaluate`, `hook`, `init`, `reference`, `tools`
- Manifest, catalog, waiver, coverage-resolution, report, stop-state, and adapter-protocol schemas
- Go adapter (v0), TypeScript adapter (v1 — the language-agnosticism test), Python adapter (v1,
  M6a), Lean adapter (v1, staged M7a–c)
- Claude Code plugin: SessionStart and Stop hooks, three optional subagents, skills
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
- A Rust adapter
- Restricting or attesting who edits what: edit-blocking hooks, protected files, edit records, and review
  approvals were built and then removed in #48 (`drop-policing`)
- Code-shape rules (lint, complexity, unsafe use, dropped errors). `assure` judges behaviour
  evidence and the integrity of that evidence, never how code is written; the adopting repo's own
  lint owns code shape.

## Success criteria

v0 (pilot, report-only):

- Pilot repo runs `assure check` locally via hooks and `assure evaluate` in CI
- Agents are blocked from stopping with failing fast checks
- p95 hook latencies within invariant 9

v1:

- `assure evaluate` is a required check on the pilot repo, blocking on levels A–B
- The TypeScript adapter ships with **zero** diffs to `plugin/` and `internal/core/`
- Each adapter passes its qualification fixtures in CI
- Within six weeks of blocking mode: at least a few real bugs attributed to framework layers
  (`found-by:*` labels), mutation efficacy on changed code trending up, test count flat or down

## Risks

| Risk | Mitigation |
|---|---|
| Claude Code hook schema changes | All hook I/O goes through one `assure hook <event>` entrypoint that parses stdin; the rest of the CLI never sees hook JSON. Contract tests against recorded hook payloads. |
| Agents gaming gates | Invariants 3 and 6: CI reruns everything; mutation (not coverage) as the quality signal; fix tests fail on base; expired waivers fail; catalog, threshold, and waiver edits show in the PR diff |
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
`./plugins/hallow-assurance/plugin`. CI for invariant 3 is that repo's.

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
    evidence/                    # parsers: JUnit, LCOV, mutation report
    adapterproto/                # adapter protocol client
    adapterkit/                  # helpers the adapters share: git, tool runs, tar, JUnit
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
    agents/{implementer,verifier,pruner}.md
    skills/{assure-testing,check,bugfix}/SKILL.md   # no commands/: Claude Code lists it as legacy
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
default_level: C
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
  evidence: mutation.report
  threshold: { A: 80, B: 65, C: 50 }
  implementations:
    go: gremlins
    typescript: stryker
  alternative_for: VER-STRUCT-MCDC
```

Optional `applies_to: formal` restricts an objective to components that declare a `formal:` block;
`applies_to: inputs` restricts it to components that declare `inputs: true`;
`applies_to: fix` restricts it to changes with a commit, between the ref and `HEAD`, whose subject
has the Conventional Commits type `fix` (`fix:`, `fix(<scope>):`, either with `!`; case-insensitive).

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

**Report** — `.assure/state/report.json`, written by `assure evaluate` (schema
`schemas/report.schema.json`): commit, base ref and SHA, evaluation date, catalog version, adapter
tool versions, and one status per applicable objective and language (`pass`, `fail`,
`advisory-fail`, `waived`), with details and applied waivers. The markdown summary is rendered from the report alone, so a
stored report reproduces it.

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

**Coverage resolution** — `coverage.resolution` evidence is LCOV with `FN` records.
`.assure/coverage-resolutions.yaml` (human-only) holds `{path, function, resolution, rationale,
approver}` entries, `resolution` one of `missing-test`, `missing-requirement`, `dead`, `deactivated`.
Each added line with zero hits must fall in a function that has a resolution. Resolutions key on the
function name, never the line, and do not expire; one whose function is gone is reported as
removable. "Verdict" is reserved for gate decisions (invariant 2).

**Adapter protocol v0** — subprocess, JSON on stdin/stdout:

```
assure-adapter-go describe            → {protocol:2, languages:["go"], claims:[..], patterns:{test:[..], generated:[..], fuzz_corpus:[..], config:[..]}, objectives:{<id>:{tool, fast?}}, reference?:"go"}
assure-adapter-go classify  <paths>   → {protocol:0, files:[{path, language, role}]}
assure-adapter-go run <objective> --changed-from <ref> --out <dir> → {protocol:2, evidence:[{type, path}], tool_versions:{...}, pinned_by:{...}, environment?:{message}}
assure-adapter-go tools --root <dir>  → {protocol:2, tools:[{name, version, pinned_by, install}], environment?:{message}}
assure-adapter-go reference          → Markdown (the testing reference named by describe.reference)
```

Every response except `reference` is an object carrying an integer `protocol`, and each
message is versioned on its own: `classify` carries `0`; `describe`, `run`, and `tools` carry `2`.
`assure` and its adapters are installed from the same release; a mismatch is an error naming both
numbers.
`pinned_by` maps each resolved tool to where its version was pinned (a repo-relative pin file, or
`default`); its keys are a subset of `tool_versions`', and the core treats each value as opaque.
`environment` means the adapter could not get the tools the objective needs; the response then
carries no evidence, and the objective fails outside the agent's reach. `tools` lists the resolved
version of every tool the adapter's objectives use, with one shell line installing it; `assure
tools --install-script` concatenates those lines so a developer and CI install the same versions.
An adapter resolves its tools from the repository's own tracked pin files first, then from its
compiled defaults, accepts only exact versions inside the range its parsers were built against, and
fails closed when the binary on `PATH` reports a different version. Whether a lockfile counts as a
pin file is decided per adapter, against the rule that an agent-editable file cannot be the only
pin.
`claims` lists the files the adapter owns. A claimed file's role is the first matching pattern list
in the order `generated`, `fuzz_corpus`, `test`, `config`, and otherwise `source`.
`fast: true` marks an objective cheap enough for the Stop-hook fast check. Cost belongs to the tool
the adapter picks, so it lives here, not in the catalog. `run` exits 0 whenever it produced a valid
response, including when the tools found failures.
Schemas: `schemas/adapter-{describe,classify,run,tools}.schema.json`.

## Starter catalog (v0)

| ID | Intent | A | B | C | D |
|---|---|---|---|---|---|
| VER-TESTS-PASS | Tests pass with race/shuffle where supported | req | req | req | req |
| VER-MUTATION-CHANGED | Mutation threshold on changed code | req | req | adv | – |
| VER-FAIL-ON-BASE | Bugfix tests fail on base commit | req | req | req | adv |
| VER-ROBUST-FUZZ | Fuzz targets exist and ran for input-handling code | req | req | adv | – |
| VER-COVERAGE-RESOLUTION | Uncovered code has an approved resolution (missing test / missing req / dead / deactivated) | req | adv | – | – |
| FM-COMPLETE | Lean model builds with `--wfail`; zero `sorry`; axiom-audit reports no violations | req | req | adv | – |
| FM-AXIOMS | Axioms used ⊆ {propext, Classical.choice, Quot.sound}; native-evaluation axioms need a waiver | req | req | adv | – |
| FM-RECHECK | Independent kernel re-check passes (`leanchecker` at every level; `lake comparator` later, once Lean ≥ 4.35 runs on Linux) | req | req | adv | – |
| FM-TRACE | Each requirement in `.assure/requirements.yaml` cites challenge theorems that build, use standard axioms only, and prove by reference over the protected `Spec` library | req | req | adv | – |
| FM-LINK | Linked tests named in `.assure/requirements.yaml` pass and each reports ≥ N inputs (N = 100), same commit | req | req | adv | – |

FM-* objectives apply only to components that declare `formal:`; FM-LINK also applies to changed
files matching a component's `formal.code` globs. FM-* gate as the table states once their milestone ships (M7a for
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
as non-blocking, so the hooks are early warning and CI is the check that cannot be skipped
(invariant 3). Only Stop blocks; no hook denies an edit.

- The shim exits 0 when no `assurance.yaml` is found above `cwd` (repo not adopted). When `assure`
  is missing or `assure hook protocol` prints a different hook-protocol number than the shim
  expects, it prints install advice as a `systemMessage` and exits 0 for every event, because the
  agent cannot fix the install. The shim checks a protocol number rather than a release version,
  because a build from a commit SHA or a checkout carries a pseudo-version yet speaks its tag's
  protocol; its install advice is the `go install …/...@v<version>` line built from `plugin.json`.
  The hook protocol is 3: #48 removed the PreToolUse, PostToolUse, and SubagentStop events.
- `internal/hookio` recovers from every internal error in the Stop hook and emits a block decision.
- Every hook declares an explicit `timeout`. File roles come from each adapter's `describe` globs,
  cached in `.assure/state/adapters.json` and keyed by the adapter executable's SHA-256.
- The manifest is resolved from `cwd`, not `CLAUDE_PROJECT_DIR`, which stays at the session root
  inside worktrees.

| Event | Matcher | Calls | Effect |
|---|---|---|---|
| SessionStart | – | `context` | Injects level map + applicable objectives for the working area |
| Stop | – | `check --changed --fast` | Blocks stopping while fast objectives fail; retry cap (own counter + `stop_hook_active`) then escalate |

Subagents are optional helpers with no enforcement behind their roles: **implementer** (source),
**verifier** (tests/fuzz/properties), **pruner** (deletes/merges tests, cites the mutation report).
Using them keeps tests written by someone other than the code's author; nothing checks that they
were used.

Skills: one generic `assure-testing` skill (philosophy, fail-first loop, coverage resolutions,
how to read `assure` output, designing for deterministic simulation, formal model + differential
testing). Per-language references ship inside each adapter (`assure-adapter-<lang> reference`), so the
plugin carries no language text; `assure reference <lang>` prints one.

Commands: `/hallow-assurance:check`, `/hallow-assurance:bugfix <issue|seed>`, shipped as user-invoked skills. Claude Code always prefixes plugin skills with the plugin name.

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
- The guard, protected paths, and role rules were removed in #48 (`drop-policing`)

**M2 — Plugin v0 (report-only pilot)**
- `internal/hookio` + `assure hook` with contract tests against recorded payloads
- `hooks.json`: SessionStart, PreToolUse guard, Stop fast check (build, vet, lint, `test -race` on
  changed packages)
- Retry cap / escalation for Stop
- Pulled forward from M3 so the Stop check has evidence to decide from: JUnit and SARIF parsers
  (minimal), Go adapter `run` for `VER-TESTS-PASS` and the compiler/linter objective, `describe` `fast`
- Install in the pilot repo (this repo first), report-only in CI
- ✅ Latency within invariant 9 on the pilot repo
- ✅ Manual session: agent edits a protected file → blocked; agent stops with failing test → blocked
- The PreToolUse guard and the Stop drift check were removed in #48 (`drop-policing`)

**M3a — Evaluator, waivers, baseline**
- JUnit keeps failure text; SARIF results carry an optional numeric `properties.metric`
- Go adapter `run` for the other lint objectives: rule packs with adapter-written lint config,
  complexity as raw metrics; the compiler/linter objective reports every finding (the ratchet is the baseline)
- `assure evaluate` → `report.json` + markdown job summary; waivers with expiry; baseline ratchet;
  `assure baseline`; the fast check applies the baseline and active waivers
- Lint objectives, SARIF, and the baseline were later removed by `drop-code-lint`
- CI runs `assure evaluate` (fails the job, not yet a required check)
- ✅ Fail-closed: missing evidence fails; expired waiver fails

**M3b-1 — Mutation, fail-on-base**
- Parser: Stryker mutation report
- Go adapter `run` for mutation (Gremlins, changed lines, converted to Stryker) and fail-on-base
- `applies_to: fix`, from a commit subject typed `fix`
- ✅ Three `evaluate` runs on one commit agree on `VER-MUTATION-CHANGED`

**M3b-2 — Remaining evidence, required check**
- Parser: LCOV
- Go adapter `run` for: fuzz (seed corpus in PR, time-boxed nightly);
  `VER-COVERAGE-RESOLUTION`
- CI: an always-running `assure-gate` job is the required check, so a path-filtered skip never
  leaves an unrelated PR Pending
- ✅ Required status check on the pilot repo (blocking levels A–B)

**M4a — Subagents, provenance, independence**
- Three subagents; role rules in `guard` keyed on `agent_type`; frontmatter `tools` /
  `disallowedTools` as defense-in-depth
- `assure record` + IND-VERIFIER-DISTINCT and CFG-PROTECTED evaluation; `--reviews` input;
  manifest `human_review`
- ✅ Evaluator rejects a PR where implementer == verifier on level B code
- Provenance became opt-in in M4c (`provenance-opt-in`); the record and independence check above run
  only with `provenance: true`
- Role rules, provenance, `assure record`, IND-VERIFIER-DISTINCT, CFG-PROTECTED, `--reviews`,
  `human_review`, and the SubagentStop check were removed in #48 (`drop-policing`); the three
  subagents remain as optional helpers

**M4b — Skills, commands**
- `assure-testing` skill + Go reference embedded in the adapter (`describe` names it); `/hallow-assurance:*` commands
- SubagentStop `check --role` for the verifier

**M4c — Solo adoption**
- `release-channel`: one `go install …/...@<tag>` line installs `assure` and every adapter from a
  `plugins/hallow-assurance/v0.N.x` tag, and `plugin.json` `version` moves with the tag; reusable
  `assure-evaluate.yml` whose `uses:` ref picks the binaries; `assure reviews --pr <n>` writes the
  reviews file, so `assure reviews` + `assure evaluate --merge-base-with` is the one entrypoint locally
  and in CI; README licensing consent for personal repos
- `review-optional-default`: `human_review` defaults to `optional` at every level including A; a base
  ref with no manifest uses the default, so the adopting PR needs no special order
- `provenance-opt-in`: manifest key `provenance` (default `false`); off, the hooks record nothing and
  IND-VERIFIER-DISTINCT is not applicable, read from the base manifest
- `assure-init`: `assure init` prints an annotated manifest template to stdout (every key with its allowed values and default, objectives per level from the catalog, `languages` from the adapters on `PATH`) and writes no file. Suggesting packages and `inputs: true` candidates is not built yet. It never proposes `formal:` values
- ✅ `hello-world-bfree/g` adopts at level B with no second reviewer and no `human_review` key
- `assure reviews`, `human_review`, and `provenance` were removed in #48 (`drop-policing`)

**M4d — Evidence loops**
- `fuzz-crasher-loop`: a nightly fuzz crasher becomes a committed seed and a
  `/hallow-assurance:bugfix` PR
- `audit`: advisory hardening of any scope, not only the changed set — `assure audit <scope>` on
  demand for paths, globs, or `path:start-end`, and scheduled `assure audit .` from its own
  reusable `assure-audit.yml`. Reports mutation survivors, coverage gaps, and missing fuzz targets
  on input-handling code; feeds the verifier and pruner; never a gate. Replaces `mutation-sweep`
- Acceptance criteria are set in each change's grill

**M5 — Qualification**
- `qualification/go/`: seeded-bug fixtures with expected outcomes (mutants that must be killed,
  known coverage)
- Qualification gates every tag: the adapter must pass it in CI before release. M5 lands before
  `release-channel` (listed under M4c), so the first published tag is already qualified.
- ✅ A deliberately broken adapter build fails qualification

**M6 — TypeScript adapter (agnosticism proof)**
- `assure-adapter-typescript` + `typescript.md` reference + qualification fixtures
- ✅ **Zero diffs** to `plugin/` and `internal/core/` in the M6 PR. Any needed change is a design bug:
  fix the abstraction first in a separate PR, then land the adapter.

**M6a — Python adapter**
- `assure-adapter-python` for uv projects: pytest with pytest-randomly (`VER-TESTS-PASS`,
  `VER-FAIL-ON-BASE`), cosmic-ray (`VER-MUTATION-CHANGED`), Hypothesis (`VER-ROBUST-FUZZ`),
  pytest-cov (`VER-COVERAGE-RESOLUTION`); Python reference; `qualification/python/` fixtures
- Catalog: `python: cosmic-ray` under `VER-MUTATION-CHANGED`; CI: `setup-uv` in qualification and an
  `evaluate-python` smoke job in the gate
- ✅ **Zero diffs** to `plugin/` and `internal/core/`, as in M6
- ✅ A mutant whose test run exits with a usage error or internal error is never scored as killed

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
- Re-adds the `VER-DST-*` objectives to the catalog and the `dst:` block to the manifest
- Seed runner (budget, timeout, concurrency) and seed-record store with failing-first retention
- Replay check: run a seed twice, compare trace digests → VER-DST-REPLAY
- ✅ A deliberately nondeterministic harness fails VER-DST-REPLAY

**Post-v1:** attestations, DuckLake evidence history, a Rust adapter, cross-repo reporting, Ziggy authoring front-end for manifest/catalog (JSON Schema
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
  even across prompts; failures outside the agent's reach (adapter that cannot start, `assure`
  itself missing) allow the stop with a `systemMessage` on the first attempt.
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
