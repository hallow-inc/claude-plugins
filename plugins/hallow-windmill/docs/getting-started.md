# Building tools on Windmill — start here

You want to automate something at Hallow. Maybe send a Slack notification when a new signup happens, run a daily report, build a button that exports data to a spreadsheet, or kick off a manual process from a form. Windmill is the platform we use for this kind of thing, and Claude Code (the tool you're using right now) can build, run, and manage Windmill tools for you.

You do not need to know how to write code. You need to be able to describe what you want.

## What can I build?

A tool on Windmill is usually one of these shapes:

| Shape | Example | When to use |
|---|---|---|
| **Script** | "When I call this, run a SQL query and return the rows." | One-off computation. Building block. |
| **Flow** | "First query the DB, then summarize the result with an LLM, then post to Slack." | Multi-step process. |
| **Raw app** | "A page with a form for date range + a button that runs my flow and shows results." | You (or others) want a UI. |
| **Schedule** | "Run this script every weekday at 8:00am." | Recurring jobs. |
| **HTTP trigger** | "When Slack sends my workspace this webhook, run this flow." | React to external events. |

Most useful tools combine these. A typical "daily report" tool is a **script** that does the work, a **schedule** that runs it every morning, and the script ending with a call to the shared `slack_post` tool so the result lands in a channel.

## What's already built that I can reuse?

Before building anything new, check what already exists. Common tools live in shared folders:

- **`f/shared/slack_post`** — post a message to a Slack channel or webhook
- **`f/shared/error_to_slack`** — automatic error notification
- **`f/shared/assert_principal`** — gate a tool to certain users or groups
- **`f/storage/s3_write_json` / `s3_read_json`** — write/read JSON to the sandbox S3 bucket
- **`f/warehouse/snowflake_query`** — query Snowflake
- **`f/warehouse/supabase_query`** — query the Hallow Supabase
- **`f/platform/flows/provision_supabase_area`** — request a Postgres role + schema

Full catalog: `${CLAUDE_PLUGIN_ROOT}/docs/toolbox.md` or ask Claude "what Windmill tools already exist?"

If something close exists, prefer composing over rebuilding. Ask Claude to chain it into your tool.

## Where does my tool live?

Every Windmill tool lives at a path. The folder controls who can see and run it.

| Path | Means | Use for |
|---|---|---|
| `u/<your-email>/<name>` | Your personal namespace | Drafts, experiments, things only you should run |
| `f/<team-or-domain>/<name>` | Team or domain folder | Tools your team uses |
| `f/shared/<name>` | Workspace-wide shared | Something everyone at Hallow can use (admins only can write here) |

If you don't know which to pick, start in `u/<your-email>/` and ask Claude to move it later.

## How do people run my tool?

Four ways:

1. **From Claude / the CLI** — `wmill --workspace dev run f/<folder>/<name> --args '{"date":"2026-05-19"}'`. Best for one-offs.
2. **From a URL (HTTP trigger)** — Windmill creates a URL for each HTTP trigger. Paste it into Slack, a browser, a webhook config. Best for "click to run" or external integrations.
3. **From a UI (raw app)** — a page with form fields and a button. Best for non-technical users who shouldn't have to talk to Claude every time.
4. **On a schedule** — runs itself. Best for recurring jobs (daily report, hourly cleanup).

Pick one based on who is going to run it. The `windmill-build` skill will ask you which.

## What's safe and what's not?

A few rules baked into how Hallow uses Windmill. The plugin enforces these automatically — you do not need to memorize them, but it helps to know they exist:

- **Secrets never go in code.** Passwords, API keys, tokens live in Windmill's encrypted variable store under `f/platform_secrets/` (admin-managed). Your script references them by name; it never sees the plaintext in source.
- **One workspace: `dev`.** Always pass `--workspace dev` on any CLI command. The `staging` and `prod` workspaces are admin-managed and you should not touch them directly.
- **No `wmill sync push`.** This is a Windmill command that exists but is **banned at Hallow** — it can delete server state and clobber secrets. Claude is configured not to run it. If you ever see a doc that suggests it, ignore the suggestion.
- **All Windmill changes go through the MCP or the Windmill UI**, not by editing files directly on the server.

## How do I actually build something?

Just ask. Phrases that load the `windmill-build` skill:

- "I want to automate X"
- "Build me a tool that does Y"
- "I want a button that runs Z"
- "Send me a Slack when …"
- "Run X every morning"
- "Make a daily report of …"
- "I want a form to do …"

The skill will ask three questions:

1. What should it do (in one sentence)?
2. How should it run (button / schedule / webhook / one-off)?
3. Who else needs to access it (just you / your team / everyone)?

Then Claude walks you through it, writes the files, mirrors them to Windmill, and gives you a URL or schedule to verify.

## When something breaks

- **"Claude can't find Windmill"** → run `/hallow-windmill:wmill-doctor`. It checks the dev loop end-to-end.
- **"My tool failed when it ran"** → say "my tool failed" — the `windmill-debug` skill auto-loads, fetches the failing job's logs, and tells you the actual error in plain language.
- **"Is there already a tool that does X"** → say "what tools exist for X" — the `windmill-discover` skill catalogs what's available.
- **"I changed my tool and nothing happened"** → make sure you mirrored the local file to the server. Edits to a local file alone do not push to Windmill. The `write-*` skills handle this automatically; if you edited by hand, ask Claude to re-publish.

## Keep the plugin fresh

The plugin updates frequently — new shared atoms, new conventions, new gotchas baked into skills. Pull updates roughly weekly, or anytime Claude does something that contradicts what a colleague told you:

```
/plugin marketplace update hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

What's new since your last update: `infra/windmill/docs/changelog.md` in the platform repo.

If Claude is doing something stale (still suggesting `wmill sync push`, missing a new shared atom, ignoring a convention), update first before debugging — the fix has often already shipped.

## Where to learn more

You don't need to read any of these to start building. They exist for when you want to go deeper.

- `${CLAUDE_PLUGIN_ROOT}/docs/toolbox.md` — catalog of reusable tools
- `${CLAUDE_PLUGIN_ROOT}/docs/folders-groups.md` — who can see what (ACLs)
- `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` — Hallow conventions (technical, for engineers)
- `${CLAUDE_PLUGIN_ROOT}/docs/shared-tool-template.md` — recipe for publishing a tool others can reuse
- `${CLAUDE_PLUGIN_ROOT}/docs/onboarding.md` — full first-time setup procedure (driven by `/wmill-setup`)
