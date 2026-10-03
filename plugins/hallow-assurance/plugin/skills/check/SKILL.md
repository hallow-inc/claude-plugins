---
name: check
description: Run the hallow-assurance fast check on the working tree, then optionally the full evaluation.
disable-model-invocation: true
---

# Check

1. Run `assure check --fast` from the repository. Report each objective's status and, for every
   `fail`, the details it prints. Say which failures are outside the agent's reach (a missing
   adapter, a protected file) and what the human must do about them.
2. Offer the full evaluation. It runs every applicable objective, including mutation testing, and
   can take several minutes. If the human accepts:
   1. Find the merge-base with the default branch:
      `git merge-base HEAD "$(git rev-parse --abbrev-ref origin/HEAD)"`.
   2. Run `assure evaluate --changed-from <merge-base>`.
   3. Report each objective's status, the failures with their details, and the report path it
      prints.

Report results only. Fixing a failure is a separate task the human asks for.
