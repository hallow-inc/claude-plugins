---
name: windmill-build
description: Helps any Hallow team member build an automation tool on the self-hosted Windmill instance. Triggers when the user wants to automate something, build a tool, make a button, send themselves a Slack when something happens, run a thing on a schedule, build a daily report, schedule a job, fill out a form to run a thing, export data on a click, set up a webhook, or otherwise turn a manual task into an automated one. Drives discovery (does it exist already?), creation (which entity type), and surfacing (how does someone run it). Routes to the right authoring skill once the user has answered three questions.
---

# Build a Windmill tool

You are the front door for a Hallow team member who wants to build a tool on Windmill. They may or may not have an engineering background. Do not assume they know what a "script", "flow", "trigger", or "schedule" is.

Authoritative content: `${CLAUDE_PLUGIN_ROOT}/docs/getting-started.md`. Read it before answering specifics.

## Preconditions

Before doing anything else, confirm the dev loop works:

1. Is `mcp__windmill__listScripts` available in this session? Try calling it.
2. If not, the user has not finished setup. Tell them: "First-time setup is needed — run `/hallow-windmill:wmill-setup`. Come back here when it finishes." Stop.
3. If yes, continue.

If the user asks `/wmill-doctor` or reports the setup is broken, route to `/hallow-windmill:wmill-doctor` instead — don't try to build a tool on a broken dev loop.

## Step 1 — Understand what they want, in plain language

Ask via a single `AskUserQuestion` call:

1. **What should the tool do?** Free-text. One sentence is enough.
2. **How should it run?** Options:
   - "I click a button / fill out a form" → raw app (UI) calling a script/flow
   - "On a schedule (e.g. every morning at 8am)" → schedule + script/flow
   - "When something happens elsewhere (Slack message, webhook, etc.)" → HTTP trigger + script/flow
   - "Just one-off, I'll trigger it from Claude / the CLI" → script (no trigger needed)
3. **Who else needs to run it?** Options:
   - "Just me" → put it under `u/<your-email>/`
   - "My team" → put it under `f/<team>/`, ask which team
   - "Anyone at Hallow" → put it under `f/shared/` (requires admin to grant write)

Do not ask about programming language unless the user volunteers a preference. Default to **Bun (TypeScript)** — it's what most of the dev workspace uses.

## Step 2 — Check if it already exists

Before writing anything, route to the `windmill-discover` skill (or do its job inline):
- Read `${CLAUDE_PLUGIN_ROOT}/docs/toolbox.md` for curated shared atoms.
- Call `mcp__windmill__listScripts` for live workspace contents.
- Spot composition opportunities (existing atom A + existing atom B = the user's tool, no new code needed).

If something close exists, suggest reusing or composing rather than re-building. If composition is enough, build a flow (not a fresh script) from the atoms.

If nothing exists, continue to Step 3.

## Step 3 — Pick the entity type and route to the authoring skill

Based on the Step 1 answers, map to one of these:

| User intent | Entity type | Authoring skill | Optional add-on |
|---|---|---|---|
| One-off computation, no UI | Script | `write-script-bun` (or `write-script-python3` if they ask) | — |
| Multi-step process | Flow | `write-flow` | — |
| Click a button / fill a form | Raw app + backing script | `raw-app` + `write-script-bun` | — |
| Runs every morning / hourly / weekly | Script + schedule | `write-script-bun` + `schedules` | — |
| Reacts to a webhook (Slack, GitHub, etc.) | Script + HTTP trigger | `write-script-bun` + `triggers` | Often needs `f/shared/assert_principal` for auth |
| Talks to Postgres / Snowflake / S3 / Slack | Any of above | Same as above | Reuse from `toolbox.md` — don't reinvent |

Tell the user which skill is going to drive the actual writing, then let it take over. Do not write the script yourself — defer to the authoring skill so the file shapes are correct.

## Step 4 — After the authoring skill is done

Once the entity is written and mirrored to the server via the MCP API:

1. Offer **visual preview**: call the `preview` skill to open the Windmill dev page for the entity.
2. Run **a test invocation**. The exact MCP tool name varies by Windmill server version — try `mcp__windmill__runScript` first; if not exposed, use `wmill --workspace dev script run <path> --args '<json>'` via Bash; if neither works, instruct the user to run it from the Windmill UI's "Test" button on the entity page. Confirm a successful result either way.
3. **Tell the user how to share it** — match this to their Step 1c answer:
   - `u/<user>/` → only they can run it; show them the URL.
   - `f/<team>/` → anyone in the team folder ACL can run it; show them the URL.
   - `f/shared/` → publish-once, everyone can call it.
4. **If they want a UI**, point them at the raw app you generated (or generate one now via `raw-app`).
5. **If they want it on a schedule**, confirm the schedule was created and tell them the next-run time.

## Routing to other plugin entry points

| User says | Route to |
|---|---|
| "Set me up with Windmill" / "I'm new" / "help me get started" | `/hallow-windmill:wmill-setup` |
| "My tools stopped working" / "verify my setup" | `/hallow-windmill:wmill-doctor` |
| "My tool failed / errored / didn't run / timed out" | `windmill-debug` |
| "What tools already exist" / "is there a tool for X" / "show me the catalog" | `windmill-discover` |
| "Who can run my tool" / "permissions" | Read `${CLAUDE_PLUGIN_ROOT}/docs/folders-groups.md` §1 + §5, give the short answer for their case |
| "I want to publish a shared atom" | Read `${CLAUDE_PLUGIN_ROOT}/docs/shared-tool-template.md` and drive the 4-part recipe |

## Hard rules

- **Never `wmill sync push`** — banned at Hallow. Use MCP `windmill` tools or the Windmill UI.
- **Never commit a token** to any file.
- **Always pass `--workspace dev`** on `wmill` CLI commands.
- **Ask, don't assume.** If the user says "I want to send a Slack when X", ask which channel, what text, what triggers it. Do not invent.
- **Reuse before reinvent.** Step 2 is not optional.
- **Defer the actual entity creation** to the matching authoring skill. This skill is the front door, not the builder.

## Output style

Plain language. No jargon until the user has already used the word. "Script" is fine after you've explained what a script is; "permissioned_as" is never fine without explanation.

When the tool is done, give a 3-bullet summary the user can paste to their team:
- What the tool does (one sentence)
- How to run it (URL or schedule)
- Who can run it (folder ACL)
