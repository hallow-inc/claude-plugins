---
name: windmill-build
description: Helps any Hallow team member get something done on the self-hosted Windmill instance — and first decides whether a tool even needs to be built. Triggers when the user wants to automate something, build a tool, make a button, send themselves a Slack when something happens, run a thing on a schedule, build a daily report, schedule a job, fill out a form to run a thing, export data on a click, set up a webhook, or turn a manual task into an automated one. ALSO triggers on one-off data asks phrased as outcomes — "pull me X", "show me last week's signups", "how many users did Y", "get me the numbers for Z", "export X to a spreadsheet", "look up X" — because the right answer may be to just do it directly with no tool built (see Step 0.5). Drives the build-or-not decision, discovery (does it exist already?), creation (which entity type), and surfacing (how does someone run it). Routes to the right authoring skill once the user has answered three plain-language questions.
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

## Step 0.5 — Do they even need to build a tool?

Not every "I want X" needs a Windmill entity. The dev workspace exposes other MCP tools you can use **right now, with no building**. Building a tool only pays off when the work must **recur, be shared, or be triggered by something else**. For a one-off, building is overkill — just do it.

Decide before asking the Step 1 questions:

| What the user wants | Do this instead of building | Tool |
|---|---|---|
| "Pull / show me / count X from the database" (one-off) | Run the query and show the rows | `mcp__hallow__execute_query` (or `preview_table` / `get_schema` to explore first) |
| "Export X to a spreadsheet / CSV" (one-off) | Query then export | `mcp__hallow__execute_query` → `mcp__hallow__export_to_csv` |
| "What columns/tables are in X?" | Inspect schema | `mcp__hallow__get_schema` / `mcp__hallow__list_connections` |
| "Look something up / research a topic" | Search the internal library | `mcp__library__semantic_search`, then `mcp__library__chat_book` |
| "Grab data off this web page" (one-off) | Fetch/extract the page | `mcp__lightpanda__lp_fetch` / `lp_extract` |

If it's a genuine one-off, **do the work directly and stop** — tell the user it's done and that you can turn it into a reusable Windmill tool later if they want it to recur, be shared, or run on a schedule. Only continue to Step 1 if the answer is yes to any of: *recurs on a schedule, others need to run it, an external event triggers it, or it's a multi-step process worth saving.*

**Hard rule — direct DB access is READ-ONLY.** `mcp__hallow__execute_query` talks straight to live connections (incl. prod/staging readers) and bypasses Windmill's `assert_principal` + audit layer. So:
- Only ever run **read** queries (SELECT / SHOW / DESCRIBE) directly. Never INSERT/UPDATE/DELETE/DDL.
- Prefer the **reader** connections (e.g. `hallow_reader`, `hallow_staging_reader`, the Snowflake `*_reader`/analytics connections) over writers.
- The moment the work needs to **write data, recur, or be shared**, stop using the direct MCP and build a Windmill tool — its atoms (`assert_principal`, audited runs) are the safe path. Continue to Step 0.75.

## Step 0.75 — Narrow the ask to ONE painful step

Windmill is for **automating a specific step in a workflow** — not for building a product. If the user describes something product-shaped, do not try to build the whole thing. Narrow it first.

**Product-shaped asks (STOP — narrow before building):** "a CRM", "replace Lattice", "a performance-review system", "an onboarding platform", "a project tracker", "a dashboard for everything", "an internal tool for the team to manage X". These describe whole products. A whole product is months of work, needs a UI, auth, data model, ongoing maintenance — and usually already exists as off-the-shelf software that does it better.

**What to do instead:**

1. **Name the real shape back to them, plainly.** "What you're describing is basically a CRM / a Lattice / a full app. Windmill isn't the right tool to build a whole product — and you probably don't want to maintain one."
2. **Find the one step that actually hurts today.** Ask: *"What's the manual thing you do over and over right now that you wish happened by itself?"* That single repetitive/error-prone step is the tool. Examples:
   - "Replace Lattice" → really wants: *a Slack reminder to managers when a review is due.* Build that one reminder. Not the review platform.
   - "Build a CRM" → really wants: *a Slack alert when a high-value lead comes in.* Build the alert. Not the CRM.
   - "An onboarding system" → really wants: *auto-grant the right folder access when someone joins.* Build the grant step (there may already be an atom — check `windmill-discover`).
   - "A dashboard for everything" → really wants: *one number posted to a channel every morning.* Build the daily post.
3. **If an off-the-shelf product already owns this, say so.** When the workflow is genuinely a CRM / HRIS / ticketing job, the answer may be "use the real product for the product, and we'll build only the *glue* — the alert, the sync, the export — that connects it to Hallow." Build the glue, not the clone.
4. **State the scope back before building.** One sentence: *"I'm going to build just this: <the one step>. This is not a <Lattice/CRM/etc> replacement — it does exactly one thing. Sound right?"* Get a yes, then go to Step 1 with the narrowed ask.

Rule of thumb: **a good Windmill tool does one thing and can be described in one sentence.** If you can't describe what you're about to build in one sentence without "and", it's too big — narrow again.

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
| "Should I make a group / new group / how to organize access" | Read `${CLAUDE_PLUGIN_ROOT}/docs/folders-groups.md` §0 (group-vs-folder-ACL decision; CE cap is gone but folder-ACL-first is the convention) |

## Hard rules

- **Never `wmill sync push`** — banned at Hallow. Use MCP `windmill` tools or the Windmill UI.
- **Never commit a token** to any file.
- **Always pass `--workspace dev`** on `wmill` CLI commands.
- **Ask, don't assume.** If the user says "I want to send a Slack when X", ask which channel, what text, what triggers it. Do not invent.
- **Reuse before reinvent.** Step 2 is not optional.
- **One tool, one job.** Never build a product. If the ask is product-shaped (CRM, Lattice, a full app, "a system for X"), run Step 0.75 and narrow to a single step first. If you can't describe the tool in one sentence without "and", it's too big.
- **Defer the actual entity creation** to the matching authoring skill. This skill is the front door, not the builder.

## Output style

Plain language. No jargon until the user has already used the word. "Script" is fine after you've explained what a script is; "permissioned_as" is never fine without explanation.

When the tool is done, give a 3-bullet summary the user can paste to their team:
- What the tool does (one sentence)
- How to run it (URL or schedule)
- Who can run it (folder ACL)
