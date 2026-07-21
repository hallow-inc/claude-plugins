---
name: windmill-discover
description: Catalogs existing Windmill tools so users can reuse instead of rebuild — and flags when no tool is needed at all. Triggers when the user asks what tools exist, what's already built, is there something that does X, does anyone have a script for Y, what's in the toolbox, "show me available tools", "list scripts in folder Z", "is there an atom for Slack/S3/Snowflake", "can I already do X", "what can I do with windmill", "what's on windmill.platform.hallow.app", or wants to find a tool by description on windmill / wmill. Combines the static toolbox catalog (curated, high-quality), live MCP listings (everything in the workspace), and the no-build path (direct read-only MCP tools for one-off pulls/exports/lookups) and returns a plain-English verdict the user can act on.
---

# Discover existing Windmill tools

You are answering "does the tool I want already exist?" — the question that should be asked before every `windmill-build` invocation. Goal: prevent reinvention. Three sources:

1. **`${CLAUDE_PLUGIN_ROOT}/docs/toolbox.md`** — curated catalog of high-quality shared atoms (Slack, S3, Snowflake, Supabase, secrets, identity). Hand-maintained. Read this first.
2. **`mcp__windmill__listScripts`** — live workspace contents. Includes user-private drafts, team-folder tools, and shared atoms. Less curated.
3. **The no-build path** — for a one-off read/export/lookup, no Windmill tool is needed at all. The dev workspace exposes direct MCP tools: `mcp__hallow__execute_query` / `export_to_csv` / `get_schema` (read-only DB access), `mcp__library__semantic_search` (internal library), `mcp__lightpanda__lp_fetch` (web pages). If the user's ask is a one-off, the answer to "does a tool exist?" is "you don't need one — I can just do it." Surface this before recommending a build. (Read-only DB rule: SELECT/SHOW/DESCRIBE only; prefer reader connections. See `windmill-build` Step 0.5.)

## Step 0 — Confirm MCP is alive

Try `mcp__windmill__listScripts` with no args. If it fails:
- Toolbox.md still works (it's static). Read from there only.
- Tell the user the MCP isn't responding, suggest `/hallow-windmill:wmill-doctor` if they want live results.

## Step 1 — Understand the search

Ask via a single `AskUserQuestion` call (skip if obvious from user message):

1. **What does the tool need to do?** Free-text. "Post to Slack", "run a Snowflake query", "write JSON to S3", "verify a user is in a group".
2. **Where should you look?** Options:
   - "Shared atoms — anything published for reuse" (recommended default) → `f/shared/`, `f/storage/`, `f/warehouse/`, `f/platform/`
   - "Specific folder I name" → ask which
   - "Everything in the workspace" → broad scan
3. **Just summaries, or also example usage?** "Summaries first" / "Show me how to call them"

## Step 2 — Read toolbox.md first

Always start here. It's curated, deduplicated, and includes:
- Path
- Input/output shape
- When to use
- Code snippet

For each match in toolbox.md, present:
- Path (clickable in most terminals)
- One-line "what it does"
- One-line "when to use it"

If the user wants usage, show the code snippet from toolbox.md.

## Step 3 — Live workspace search

After toolbox.md, supplement with `mcp__windmill__listScripts`:

- Filter to the folder(s) chosen in Step 1.
- For each script, get the `summary` field from its `.script.yaml`. If `summary` is empty or unhelpful, fall back to the script path.
- Skip duplicates already covered by toolbox.md.
- Sort by: shared atoms (`f/shared/`, `f/storage/`, `f/warehouse/`, `f/platform/`) → team folders (`f/<team>/`) → user folders (`u/<user>/`, only if user opted in to "everything").

Show at most 10 results unless the user asks for more. Long lists are useless.

## Step 4 — Recommend

For each candidate match:
- **High confidence** — fits the description, in `f/shared` or `f/storage` or `f/warehouse`, has a clear summary. Recommend using it.
- **Maybe** — close but not exact. Tell the user what's different and let them decide.
- **No** — partial keyword match but wrong shape. Filter out, don't show.

End with one of:
- "**Use `f/shared/slack_post`** — it does exactly what you want. Here's how." → show snippet.
- "**No exact match.** Closest is `f/<...>` but it differs because <reason>. Want me to either (a) adapt it, or (b) build a new tool with `windmill-build`?"
- "**Nothing found.** Let's build it — route to `windmill-build`."

## Step 5 — Composition hints

Sometimes the user's described tool is **already a composition** of existing atoms. Spot this and call it out:

- "Send a Slack when a Snowflake query has new rows" → `f/warehouse/snowflake_query` + `f/shared/slack_post`. No new code, just a flow.
- "Daily report to Slack" → schedule + your-query-script + `f/shared/slack_post`.
- "Write to S3 then notify" → `f/storage/s3_write_json` + `f/shared/slack_post`.

If composition is enough, suggest a **flow** (route to `windmill-build` with the composition pre-described) instead of a fresh script.

## Hard rules

- **Toolbox.md first.** Live MCP supplements; it doesn't replace.
- **Always pass `--workspace dev`** on any CLI calls.
- **Show summaries, not paths only.** A path without context is useless to a non-technical user.
- **Filter, don't flood.** 10 results max unless asked.
- **Recommend a verdict.** Don't dump a list and walk away. Tell the user what to use.
- **Never invent a path.** Only show paths confirmed by toolbox.md or `mcp__windmill__listScripts`.

## Routing

| User says | Route to |
|---|---|
| "Nothing here matches, build it" | `windmill-build` (pass along the description) |
| "How do I use this one" | Read snippet from toolbox.md aloud; if not there, fetch script source via MCP |
| "Show me visually" | `preview` skill |
| "Who can run this" / "what folder is this in" | Read `${CLAUDE_PLUGIN_ROOT}/docs/folders-groups.md` §1 + give the short answer |
| "I want to build my own reusable tool" | `windmill-build` — it writes the tool into a folder you can write (`u/<you>/` or `f/<team>/`) |
