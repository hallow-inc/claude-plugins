---
name: windmill-ask
description: Front door for any QUESTION about Hallow's self-hosted Windmill — how it works, how to do something, why something behaves a certain way, or where the docs are. Triggers whenever the user mentions windmill, wmill, or windmill.platform.hallow.app and is ASKING rather than requesting a build/debug/setup action. Fires on "how do I ... in windmill", "does windmill support ...", "what's the deal with ... in windmill", "why does windmill ...", "explain windmill ...", "where are the windmill docs", "what is ... in our windmill", or similar question-shaped requests. Thin router — defers to windmill-build for "build me X", windmill-discover for "does a tool exist", windmill-debug for "my job failed", windmill-onboarding for first-time setup; otherwise points at the platform docs and the matching pattern skill. NOT for build/debug/discover/setup actions (those skills own them).
---

# Answer a Windmill question

The user asked a question about Windmill and it did not obviously belong to a more specific skill. Your job is to get them to the right answer — usually by routing, not by answering from memory.

## Step 0 — Is this really a question for me?

Route away FIRST if the intent matches a specific skill. Do not answer these yourself:

| If the user is really... | Defer to |
|---|---|
| Asking to build/automate something ("build me X", "make a button", "run on a schedule") | `windmill-build` |
| Asking whether a tool already exists ("is there a script for X", "what's in the toolbox") | `windmill-discover` |
| Reporting a failure ("my job errored", "the schedule didn't fire", "nothing happens") | `windmill-debug` |
| First-time setup ("set me up", "install wmill", "mint a token", "connect to the instance") | `windmill-onboarding` |
| Asking about Hallow conventions/atoms/secrets/`permissioned_as` while building or modifying | `windmill-patterns` |

If one fits, say so and stop — let that skill take over. Only continue below when the query is a genuine conceptual/where-do-I-look question with no specific owner.

## Step 1 — Answer by pointing, not guessing

Windmill behavior at Hallow is documented and versioned; do not answer from stale memory. Point the user at the authoritative source:

1. **Hallow-specific conventions** (atoms, secrets, ACLs, local-yaml-first, elevation) → `windmill-patterns`, which surfaces `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md`.
2. **Plain-English overview** (what you can build, how it runs, when to use which entity) → `${CLAUDE_PLUGIN_ROOT}/docs/getting-started.md`.
3. **Full user-facing doc set** (Tutorial, How-to, Reference, Troubleshooting, Glossary, Atoms catalog, Access matrix) → the platform docs index at `infra/windmill/docs/index.md` in the platform repo. Name it; don't paraphrase its contents.
4. **A specific entity type** (flow, trigger, schedule, resource, a script language) → name the matching authoring skill (`write-flow`, `triggers`, `schedules`, `resources`, `write-script-*`) so it loads if the user then wants to act.

Give the shortest route that answers the question. If the question is factual and you are confident from the docs, answer it plainly and cite where it came from.

## Step 2 — Stay in the non-admin lane

If the honest answer is admin-gated (workspace-wide grants, publishing shared atoms, elevated run-as, infra), do NOT give an admin how-to. Say what a non-admin CAN do, or name the admin owner. Reading and calling `f/shared/` tools stays in scope.

## Do not

- Do not duplicate the build/discover/debug/patterns skills — route to them.
- Do not answer Windmill-version-specific behavior from memory when a doc exists.
