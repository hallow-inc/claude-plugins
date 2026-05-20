---
name: hallow-windmill
description: Reference and discovery skill for Hallow's Windmill onboarding. Loads when a new engineer says they want to set up Windmill, install wmill, connect to windmill.platform.hallow.app, mint a Windmill token, configure the Windmill MCP, get started with Hallow's Windmill instance, or asks any "I'm new and need to get up and running" question. Surfaces the authoritative procedure at `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` and points the engineer at `/hallow-windmill:wmill-setup` to run it end-to-end and `/hallow-windmill:wmill-doctor` to verify it later.
---

# Windmill onboarding (reference skill)

This skill loads automatically when a new Hallow team member is asking about Windmill setup. Its job is to:

1. Confirm the engineer is in the right place.
2. Surface the authoritative doc.
3. Hand off to the right driver: `/wmill-setup` for the full flow, `/wmill-doctor` for a smoke test.

The full procedure is documented at `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md`. Treat it as the source of truth.

## What to do when this skill loads

1. **Acknowledge what the user asked.** One sentence.
2. **Detect their platform** (macOS / Linux or WSL / native Windows) and **editor surface** (Claude Code CLI / VS Code extension / both). Ask once via AskUserQuestion if not obvious.
3. **Offer the right next step:**
   - If they want to be walked through onboarding end-to-end → tell them to run `/hallow-windmill:wmill-setup`, or offer to delegate to the `windmill-onboarder` subagent so the flow runs in an isolated context.
   - If they think onboarding is already done and just want to verify → tell them to run `/hallow-windmill:wmill-doctor`.
   - If they want to **build a tool** rather than set up infrastructure → route them to the `windmill-build` skill (just say "tell me what you want to automate" — the skill auto-loads). The authoring skills (`write-flow`, `write-script-*`, `raw-app`, `triggers`, `schedules`, `resources`) ship with this plugin and load on demand from any directory.
4. **Never inline-walk the full flow yourself.** Use `/wmill-setup` or the subagent. This skill is reference, not a driver.

## Operating rules

- **Read `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` before answering specifics.** It is the source of truth; do not paraphrase from memory.
- **Never invent commands** not in the doc. If something is missing or unclear, ask the user a direct question.
- **Never run destructive operations** (rm, force-push, anything that touches shared state) without explicit confirmation.
- **Never `wmill sync push`** under any circumstances. The doc §10 lists this as a hard ban.
- **Never commit a token** to any file under version control.
- **Pass `--workspace dev` on every `wmill` command** that hits the remote. The active-workspace trap is real.

## Companion entry points

| Entry point | When to use |
|---|---|
| `/hallow-windmill:wmill-setup` | User wants to be walked through the full setup flow now. User-invocable only (`disable-model-invocation: true`) because it writes `.mcp.json` and touches tokens. |
| `/hallow-windmill:wmill-doctor` | User wants to verify their dev loop still works. Read-only. Safe to suggest proactively when they report breakage. |
| `windmill-onboarder` subagent | User wants the flow run in an isolated context (parent thread is doing other work). Same procedure, separate context window. |
| `windmill-build` | Setup is done and the user wants to build a tool. |
| `windmill-discover` | Setup is done and the user wants to find a tool that already exists. |
| `windmill-debug` | Setup is fine but a built tool is failing. Don't route here for dev-loop breakage — `/wmill-doctor` owns that. |

## When the user finishes onboarding

Once `/wmill-doctor` reports all-pass and the user has manually verified `mcp__windmill__listScripts` in-session, briefly summarize:

1. CWD to launch Claude Code from for ad-hoc Windmill work (`~/dev/wmill/` or Windows equivalent).
2. To build a tool: just ask, the `windmill-build` skill auto-loads from any directory. Authoring skills (write-flow, write-script-*, raw-app, triggers, schedules, resources) ship with this plugin.
3. The hard "do not do" list from §10 (with `wmill sync push` ban front-and-center).

Do not write a long recap — the engineer can re-read `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` anytime.
