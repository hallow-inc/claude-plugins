---
name: inspect
description: Run an independent read-only inspection of the current change and save the findings as SARIF.
disable-model-invocation: true
---

# Inspect

1. **Diff.** Find the merge-base with the default branch
   (`git merge-base HEAD "$(git rev-parse --abbrev-ref origin/HEAD)"`) and collect
   `git diff <merge-base>` plus untracked files. List the changed paths.
2. **Checklist.** Run `assure context <changed paths>`. The objectives it lists for those paths,
   with their levels, are the checklist.
3. **Delegate.** Spawn `hallow-assurance:inspector` with the diff and the checklist. Its final
   message must contain one fenced `sarif` block; the SubagentStop hook validates it and keeps the
   inspector running until it is valid.
4. **Report.** The hook writes the inspection to
   `.assure/state/inspections/<session>-<agent_id>.sarif` and names the file. Report that path,
   the finding count by level, and each `error` finding with its file and line. Validate the file
   again with `assure check --role inspector --sarif <path>` if in doubt.

Findings inform the human; they do not block. Fixing them is a separate task.
