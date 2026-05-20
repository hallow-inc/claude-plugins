---
name: wmill-setup
description: Drive a new Hallow engineer through the full Windmill onboarding flow — preconditions, installs, token mint, workdir, CLI workspace registration, and MCP wiring. Reads the authoritative onboarding doc, detects platform, and walks the user step by step with verification gates after each section. Use when the engineer types /hallow-windmill:wmill-setup, says they want to be walked through Windmill setup, or starts a fresh onboarding flow.
disable-model-invocation: true
allowed-tools: Bash, Read, Edit, Write, AskUserQuestion, WebFetch
---

# /wmill-setup

You are running the Windmill onboarding flow for a new Hallow engineer. The authoritative procedure is at `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md`.

This skill has side effects — it writes `.mcp.json`, calls `claude mcp add`, and handles API tokens. `disable-model-invocation: true` keeps Claude from auto-triggering it; the engineer must invoke `/hallow-windmill:wmill-setup` explicitly.

## Step 0 — Read the doc

Before doing anything else, read `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` in full. Do not skim. Do not paraphrase from memory afterward — refer back to the doc for exact commands.

## Step 1 — Detect the environment

Ask the user (via AskUserQuestion, one call, all questions at once):

1. **Operating system** — macOS / Linux or WSL / native Windows
2. **Editor surface** — Claude Code CLI only / VS Code extension only / Both
3. **Have they already minted a Windmill API token?** — Yes (have it ready) / No (need to mint per §3)

Use these answers to pick the matching command blocks for the rest of the flow. Never blend platforms.

## Step 2 — Run preconditions (§0)

Run the precondition check block for the user's platform. Report each result:
- Tailnet membership
- DNS resolution
- HTTPS reachability
- `wmill --version`
- `claude --version`

If any fail, drive the corresponding fix from §1–§4 before proceeding. Do not skip ahead.

## Step 3 — Walk §1 through §7 in order

For each section:
1. State which section you're entering and what it accomplishes.
2. Run / instruct the platform-specific commands from the doc.
3. Verify the gate at the end of the section before moving on.
4. If a step fails, consult §9 failure table.

Token-handling notes:
- For §3, link the user to `https://windmill.platform.hallow.app/` and tell them where the token UI is. Do not ask for the token until §6 needs it.
- For §6 and §7, use the doc's history-suppression pattern for the user's shell.
- Verify `.gitignore` contains `.mcp.json` **before** the `claude mcp add` call runs.

## Step 4 — Run §8 smoke test

Hand off to `/hallow-windmill:wmill-doctor` to run the full smoke test, or run §8 steps 1–4 inline and explicitly hand step 5 (in-session MCP verification) to the engineer.

## Step 5 — Wrap up

When everything passes, give a 3-bullet summary:
- Where to `cd` for CLI/MCP work (`~/dev/wmill/` or Windows equivalent)
- To build a tool: just describe what you want to automate — the `windmill-build` skill auto-loads and the bundled authoring skills take it from there
- The §10 hard "do not do" list (with `wmill sync push` ban front-and-center)

Tell the user they can re-read `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` anytime, and `/hallow-windmill:wmill-doctor` if anything regresses later.

## Hard rules

- Never run destructive commands without explicit confirmation.
- Never `wmill sync push`.
- Never commit a token.
- Never invent commands not in the doc.
- Never proceed to the next section if the current gate fails.
