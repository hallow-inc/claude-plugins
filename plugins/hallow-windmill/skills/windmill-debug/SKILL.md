---
name: windmill-debug
description: Diagnoses Windmill tool failures in plain language. Triggers when the user says their script/flow/app failed, errored, crashed, didn't run, returned nothing, hung, timed out, ran but did the wrong thing, "my tool is broken", "Windmill is throwing an error", "the job failed", "nothing happens when I click", "the schedule didn't fire". Fetches the most recent failing job, reads logs + result, summarizes the error in plain English, and suggests a concrete next step. Does NOT diagnose dev-loop setup problems — `/hallow-windmill:wmill-doctor` owns that.
allowed-tools: Bash, Read, AskUserQuestion
---

# Debug a Windmill tool

You are diagnosing a Windmill **tool failure** — a script, flow, app, or trigger that ran but didn't behave correctly. The user may not know which entity failed, what its path is, or even whether it actually ran.

This skill is NOT for dev-loop setup problems. If `mcp__windmill__listScripts` is missing entirely, the user's MCP isn't connected — route to `/hallow-windmill:wmill-doctor`, do not try to debug.

## Step 0 — Confirm the dev loop is alive

Try calling `mcp__windmill__listScripts` with no args. If it fails:
- Tell the user the Windmill MCP isn't responding. Route them to `/hallow-windmill:wmill-doctor`. Stop.
- Do not proceed.

If it works, continue.

## Step 1 — Narrow down what failed

Ask via a single `AskUserQuestion` call (skip questions the user already answered):

1. **What did you do that broke?** Free-text. "Clicked the button", "It runs on a schedule", "Posted to the webhook URL", "Ran the script from Claude".
2. **Do you know the path?** Options:
   - "Yes — `f/<folder>/<name>` or `u/<user>/<name>`" → free-text path
   - "No, but I clicked on it in the UI" → ask for the URL or the entity name
   - "No idea" → free-text whatever they remember
3. **When did it last work?** "Just now (regression)" / "First time trying it (never worked)" / "Hours/days ago" / "Don't know"

## Step 2 — Find the failing job

Pick a strategy based on Step 1:

| Have | Use |
|---|---|
| Job ID | `wmill --workspace dev job get <id>` for status; `wmill --workspace dev job logs <id>` for output; `wmill --workspace dev job result <id>` for the JSON result. |
| Entity path | `wmill --workspace dev job list --script-path <path> --limit 5` → grab the most recent failed ID, then get logs. |
| Path unknown but description known | `mcp__windmill__listScripts` filtered to plausible folder; show 3-5 matches; ask user which. |
| User clicked a raw app button | The app calls a backend script. Get the app's path from the user, find the backend script via the app's `raw_app.yaml`, then `job list --script-path <backend>` for recent runs. |
| Scheduled job that didn't fire | `wmill --workspace dev schedule get <path>` to confirm the schedule exists + `enabled: true`. Then `job list --script-path <path>` for recent runs to see if it ran at all. |
| HTTP trigger that didn't react | Confirm the URL the caller hit matches the trigger's path. Check `job list` for that script — Windmill records every triggered invocation. If nothing's there, the request never reached Windmill (wrong URL, missing token, not on tailnet). |

Always pass `--workspace dev` on `wmill` commands.

## Step 3 — Read the actual error, don't speculate

For the failing job:

1. Run `wmill --workspace dev job logs <id>` and read the **whole** output.
2. Run `wmill --workspace dev job result <id>` to get the final error object (Windmill captures thrown errors as JSON: `{ error: { message, name, stack } }`).
3. If it's a flow, `wmill --workspace dev job get <id>` returns the step tree with sub-job IDs. **Drill into the failing step** — the flow-level error is usually a re-wrap; the real error is in the step.

**Hard rule:** never diagnose without reading the actual logs. "Probably a token issue" without seeing the logs is wrong every time.

## Step 4 — Classify and explain

Map the error to one of these buckets and tell the user in plain language. Quote the exact error line.

| Pattern in logs | Plain-English diagnosis | Fix |
|---|---|---|
| `401 Unauthorized` / `Token expired` | A token used by the script is dead or wrong. | Identify which resource (Snowflake / Supabase / Slack / etc.). Re-seed via Windmill UI → resource page. |
| `403 Forbidden` / `permission denied` | The script is running as a user who doesn't have access to a resource it needs. | Either move the resource into a folder the caller can read, OR wrap the script in an HTTP trigger with `permissioned_as: u/<admin>`. See `${CLAUDE_PLUGIN_ROOT}/docs/patterns.md` §5. |
| `Resource not found: f/...` | A `wmill.getResource()` path is wrong, or the resource was deleted. | List resources in that folder via `mcp__windmill__listResource`; correct the path. |
| `Variable not found: f/platform_secrets/...` | Secret reference broken — wrong path, or admin hasn't seeded yet. | Check the variable exists in the Windmill UI under `f/platform_secrets/`. Ask an admin if missing. |
| `TypeError`, `ReferenceError`, `SyntaxError` | Real code bug. | Read the line number from the stack. Open the script in the workdir, fix, re-publish via MCP. |
| `Job timed out` | Hit Windmill's per-job timeout. | Default is 30s for many entities. Increase `timeout:` in `.script.yaml` (or `flow.yaml` step), or split the work into smaller chunks. |
| `runScriptAsync` queued but never ran | Used `runScriptAsync` for a flow path. | Replace with `runFlowAsync`. See `patterns.md` §6. |
| Empty logs + `success: false` | Script returned a non-OK result without throwing — caller treated it as failure but the script didn't crash. | Read the result JSON; the script returned an error shape. Fix the upstream service or the script's success criteria. |
| `EACCES`, `ENOENT` on filesystem | Script tried to read/write a local file path. Windmill workers don't have that filesystem. | Use S3 (`f/storage/s3_write_json` / `s3_read_json`) instead. |
| `429 Too Many Requests` | Hit a rate limit on the called service. | Add backoff, lower concurrency in `.script.yaml`, or cache results. |
| `WM_PERMISSIONED_AS` missing / wrong | Script depending on principal identity ran as the wrong user. | Scripts can't elevate. Wrap in HTTP trigger with `permissioned_as`. |
| `HTTP trigger: 404` from caller | Wrong URL — workspace name, trigger path, or method mismatch. | Fetch the trigger's actual URL via `mcp__windmill__listResource` or the Windmill UI. |
| Slack post failed: `not_in_channel` | Bot not invited to the channel. | Invite the Hallow Slack bot to the channel manually in Slack. |
| Slack post failed: `channel_not_found` | Channel name wrong, or it's a private channel. | Use the channel ID instead of `#name`. |

If the error doesn't fit any of these, say so explicitly. Don't fabricate a diagnosis. Quote the error and ask the user for more context (when did it last work, did anything change, etc.).

## Step 5 — Suggest, don't act

Tell the user what to do. Do not auto-fix unless they confirm.

For each fix:
- If it's a code change → offer to edit the script + re-publish via MCP (`mcp__windmill__updateScript` or the `write-script-*` skills).
- If it's a config change (timeout, schedule, ACL) → tell them the field, the file, and the new value. Offer to apply.
- If it's an external dependency (Slack invite, admin seeding a secret, missing tailnet ACL) → tell the user exactly what to ask and whom.

If the user wants the visual context, route to the `preview` skill to open the entity's Windmill page.

## Step 6 — Verify the fix

After any fix:
1. Re-run the entity (`mcp__windmill__runScript` for scripts; `wmill --workspace dev job list --script-path <path>` to grab the next job ID after a manual trigger).
2. Confirm `success: true` on the new job.
3. Tell the user the new job ID + result summary.

## Hard rules

- **Read the actual logs.** Never diagnose blind.
- **Never `wmill sync push`** — even when something is broken. Use MCP API to push fixes.
- **Always pass `--workspace dev`.**
- **Don't auto-fix without confirmation.** Diagnose, suggest, ask.
- **One-job-at-a-time.** If the user reports "everything is broken", pick the most recent failure and start there. Symptoms cluster; root cause usually one.
- **Quote, don't paraphrase, the error.** The exact message matters for grepping docs and tickets.

## Routing

| User says | Route to |
|---|---|
| "MCP isn't working / Claude can't find Windmill" | `/hallow-windmill:wmill-doctor` |
| "I want to make a new tool" | `windmill-build` |
| "What does X tool do" / "is there a tool for Y" | `windmill-discover` |
| "Show me the visual of this entity" | `preview` skill |
| "How do I structure a flow" / Hallow conventions | `windmill-patterns` |
