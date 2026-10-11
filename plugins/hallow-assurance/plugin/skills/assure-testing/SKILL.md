---
name: assure-testing
description: >-
  How to write tests in a repo governed by hallow-assurance (an assurance.yaml at the root).
  Use when writing or changing tests, property tests, fuzz targets, or fuzz corpora; when
  reproducing a bug before fixing it; when assure check or assure evaluate reports a failing
  objective; or when uncovered code needs a coverage resolution.
---

# Testing under hallow-assurance

`assure` decides pass or fail from evidence the adapters collect. These rules hold in every
language. For the language's tools, flags, and idioms, run `assure classify <path>` to get the
file's language, then `assure reference <lang>`. Read the reference before the first test edit in
a session.

## Properties over examples

A test protects a behavior, stated as something that holds for every input in a space. Write it as
a **property**: generate inputs, assert the invariant. Typical invariants are round trips
(decode after encode is identity), equivalence with a simpler model, idempotence, ordering, and
"never panics, always returns a value or an error".

- Hand-picked examples are for a case a generator cannot reach, and for regression seeds.
- Any function over untrusted bytes or text gets a fuzz target with a committed seed corpus.
- A test earns its place only if it can fail when the behavior changes. `VER-MUTATION-CHANGED`
  measures this: a surviving mutant is a line whose behavior no test observes.
- Each new test case counts against `VER-TEST-BUDGET`. One property replaces a table of examples
  at the cost of one case.

## Fail-first for bug fixes

A bug fix starts **red**:

1. Write a test that reproduces the bug through the API that existed before the fix.
2. Run it on the unfixed code. It fails, for the reason the report gives. A compile error is not
   red.
3. The implementer fixes the code. The test goes green.
4. The human commits with the trailer `Assure-Kind: fix`. `VER-FAIL-ON-BASE` then reruns the test
   on the base commit and requires it to fail there.

Agents do not commit.

## Coverage resolutions

Uncovered code needs one of four resolutions (`VER-COVERAGE-RESOLUTION`):

| Resolution | Meaning | What to do |
|---|---|---|
| `missing-test` | a requirement exists; no test exercises it | write the test, or report why it cannot be written yet |
| `missing-requirement` | the code does something no requirement asks for | report it; the spec or the code changes |
| `dead` | unreachable | report it for deletion |
| `deactivated` | present but switched off in this build | report it with the switch that disables it |

Resolutions live in `.assure/coverage-resolutions.yaml`, which agents may not edit. Report the
function, the resolution, and a rationale; a human records it.

## Reading assure output

`assure check --fast` and the Stop hook print one line per objective (`pass`, `fail`,
`advisory-fail`), then details for each failure. `assure evaluate --changed-from <ref>` decides
every applicable objective and writes a report. In both:

- `fail` on a required objective blocks. Fix the cause the details name.
- `advisory-fail` does not block. Mention it in your report.
- A failure that names an adapter missing from `PATH`, or a protected file, is outside your reach.
  Report it to the human.
- `assure context <path>` lists the objectives that apply to a path at its level.

## Designing for deterministic simulation

Code is testable when every source of nondeterminism enters through a parameter: time, randomness,
I/O, scheduling. A component written that way can run under a simulator that controls all of them
from one seed, so any failure replays exactly.

- Pass clocks, random sources, and I/O as interfaces; production wires the real ones.
- Make the seed of every generated test visible in its failure message.

## Formal models and differential testing

A component that declares `formal:` has a formal model: a small specification proved correct
separately. The implementation is tied to it by **differential testing**: generate inputs, run both
the model and the implementation, and require identical results. The model's theorem statements
(`challenge`) are protected; only humans change them. `FM-LINK` counts the mismatches and the inputs
tried.
