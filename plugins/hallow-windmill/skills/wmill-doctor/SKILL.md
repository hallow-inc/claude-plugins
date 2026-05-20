---
name: wmill-doctor
description: Run the Windmill onboarding end-to-end smoke test (§8 of the onboarding doc) and report which step failed plus the matching fix from §9. Read-only — does not modify any config or re-mint tokens. Use when the engineer types /hallow-windmill:wmill-doctor, asks "is my Windmill setup still working", reports their wmill or MCP tools stopped working, or wants to verify the dev loop after a reboot, machine change, or token rotation.
allowed-tools: Bash, Read, AskUserQuestion
---

# /wmill-doctor

Run the end-to-end smoke test from `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` §8. Read the §8 block for the user's platform, run each step in order, and report pass/fail per step.

## Procedure

1. Detect the user's platform (macOS / Linux / WSL / Windows) via the shell environment or a one-question AskUserQuestion.
2. Read `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` §8 for the matching platform.
3. Run steps 1–4 (Claude-runnable) in order. Stop on the first failure.
4. For step 5 (in-session MCP check), explicitly tell the engineer what to do manually — do not pretend to run it.

## Steps to run

1. Tailnet still up — `tailscale status | head -1` (or Windows equivalent).
2. CLI auth works — `wmill --workspace dev workspace whoami`.
3. Server reachable + token valid — `wmill --workspace dev script list --json | head -5`.
4. MCP config parses + is registered — `node -e "JSON.parse(...)"` on `.mcp.json` + `claude mcp list | grep windmill`.
5. (Engineer, not you) — Launch Claude Code from the workdir, ask Claude to call `mcp__windmill__listScripts`.

## Reporting

After each step, output one line:
- `[OK] §8.<n> <description>` on success
- `[FAIL] §8.<n> <description>: <error excerpt>` on failure, followed by the matching row from §9 failure table

At the end, output a single summary line:
- `wmill-doctor: ALL PASS (steps 1-4) — engineer to verify step 5`
- `wmill-doctor: FAILED at §8.<n> — see fix above`

## Hard rules

- Read-only. Do not modify any config, re-mint tokens, or re-register the MCP automatically.
- If a fix requires action, recommend it; do not execute without explicit user approval.
- Never run `wmill sync push` under any circumstance.
