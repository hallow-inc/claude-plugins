## What and why

<!-- What changes, and the reason. Link the OpenSpec change or issue. -->

## CI, config, and protected files changed

<!-- List every change to .github/, thresholds, waivers, baselines, or other protected files, with the
reason. Write "None" if there are none. -->

## Reviewer checklist

Approving this PR attests that you read every change listed above and agree it was intended.
The PR author's approval does not count.

- [ ] The diff matches "What and why"; nothing unrelated is included.
- [ ] Every CI, config, or protected-file change is listed above with a reason.
- [ ] No test, lint rule, or threshold was weakened to get a green check.

### `plugins/hallow-assurance/` (skip if the PR does not touch it)

- [ ] The PR covers one milestone, named in "What and why".
- [ ] Protected files (invariant 6) changed only on purpose: `catalog/`, thresholds, manifest levels
      in `assurance.yaml`, `.assure/waivers.yaml`, `.assure/baseline*`, `.assure/provenance/`,
      formal-model challenge files, `.github/workflows/`.
- [ ] Provenance gaps reported by CI on level A–B files were read line by line. Your approval is what
      lets a gap pass IND-VERIFIER-DISTINCT.
- [ ] `plugin/` and `internal/core/` contain no language-specific logic; it lives in `adapters/`.
- [ ] No model call takes part in a pass/fail decision.
- [ ] Only `internal/hookio/` parses Claude Code hook JSON.
- [ ] Every new file format has a JSON Schema in `schemas/` with valid and invalid fixtures.
- [ ] Bug fixes start with a test that fails on the base commit; generated properties are used
      where a table of hand-picked cases would otherwise be.
