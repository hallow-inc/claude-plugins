---
name: audit
description: >-
  Audit the test strength of existing code with assure audit, changed or not, and walk the
  human through each finding. Use when asked to audit, find weak tests, harden tests, or measure
  how well a service, directory, file, class, or function is tested; or to act on or document
  findings in .assure/state/audit/findings.json.
argument-hint: <service | directory | file | class | function>...
---

# Audit

`assure audit` measures how well existing code is tested and writes findings to
`.assure/state/audit/findings.json`. It is advisory: findings never gate a merge, and the human
decides what happens to each one. Input: `$ARGUMENTS`, or whatever the human named.

## 1. Resolve the scope

Turn each thing the human named into `assure audit` scope arguments, relative to the current
directory:

| Named | Scope argument |
|---|---|
| service or directory | its directory path |
| file | its path |
| several files by pattern | a glob, matched against repository-relative paths |
| class or function | `<file>:<start>-<end>`, the inclusive 1-based lines of its definition |

Find a class or function's lines with LSP (document symbols or jump to definition) or, without
LSP, grep for its definition and read to its end. When a name matches more than one definition,
show the candidates and let the human pick. The step is done when every named thing maps to an
argument the human recognises.

## 2. Run the audit

A **broad** scope is a directory, a glob, or `.`. Mutation testing over a broad scope can take
hours (a whole-module run took 1h38m). Before a broad run, tell the human it may take a long
time and ask to proceed; then run it in the background and keep working with the human until it
exits. A file or range runs in the foreground.

```sh
assure audit <scope>...
```

| Exit | Meaning | Do |
|---|---|---|
| 0 | findings written, every language audited | go to step 3 |
| 1 | findings written for some languages; the summary names each one not audited and why | report each not-audited line (a missing adapter or tool the human must install), then go to step 3 |
| 2 | usage or manifest error | report the message; with no `assurance.yaml`, the human runs `assure init`, saves the template as `assurance.yaml`, and lists it in `.git/info/exclude` to keep it out of upstream |

## 3. Walk the findings

Read `.assure/state/audit/findings.json`. Report the advisory `scores` (mutation score against
the level's threshold) first, then present every finding, grouped by `path`, with its `kind`,
`line`, `level`, and `detail`:

- `surviving-mutant`: a change to the code no test noticed.
- `uncovered`: lines no test runs.
- `missing-fuzz`: a parser or input handler with no fuzz target.
- `tests-failing`: the scoped tests are red, so audit skipped mutation for that language.

Offer three choices for each finding, or for a group the human treats as one:

- **pass**: record nothing; the human accepts the weakness.
- **act**: harden it (step 4).
- **document**: send it to the configured destination (step 5).

The step is done when every finding has a choice.

## 4. Act

Delegate `surviving-mutant`, `uncovered`, and `missing-fuzz` findings to
`hallow-assurance:verifier`: give it each finding's `path`, `line`, and `detail`, and ask for tests
that kill the mutant, run the lines, or add the fuzz target. The verifier changes tests only.

A hardening test passes on the current code. When the verifier's test fails because the code is
wrong, the finding is a **bug**, not a weakness. So is a `tests-failing` finding whose failure is
real. Stop hardening that finding and tell the human to run `/hallow-assurance:bugfix` with the
failing test or input.

Re-run `assure audit` on the acted-on findings' files or ranges. The step is done when those
findings are gone from `findings.json`, or the human chose another disposition for the rest.

## 5. Document

Read [destinations.md](destinations.md) and send every **document** finding to the destination
configured in `.claude/hallow-assurance.local.md` (repository), else
`$HOME/.config/hallow-assurance/audit.yaml` (user), else the default file, skipping any the
destination already holds.

## 6. Hand off

Report what was hardened, documented, passed, and handed to bugfix. Tell the human to commit
hardening tests with a subject typed `test` (`test:` or `test(<scope>):`), separate from any `fix`
commit. Also tell them that when any commit in the evaluated range has a `fix` subject, `assure
evaluate` requires every changed test to fail on the base commit, and a hardening test passes
there; whether to ship hardening and fixes in separate pull requests is theirs to decide. Leave the
commit to the human.
