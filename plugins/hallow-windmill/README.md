# hallow-windmill

Claude Code plugin for any Hallow team member who wants to build automation tools on the self-hosted Windmill instance — engineering background not required. Three scopes:

1. **Get started** — `/windmill-build` asks what you want to automate and drives the rest.
2. **First-time dev loop setup** — `/wmill-setup` walks you from "Claude Code installed" to "I can talk to Windmill", `/wmill-doctor` verifies it later.
3. **Authoring + reference** — bundled skills for writing scripts/flows/apps/triggers/schedules/resources, plus reference docs for Hallow conventions, ACLs, shared tools.

Folder: `plugins/hallow-windmill/` (Hallow-prefixed for source-tree ownership).
Plugin id: `hallow-windmill` (what users see in `/plugin install` and the slash-command namespace).

Audience: any Hallow team member. Admin/infra-ops content lives in the platform repo (`__docs/`).

## What you get

### Start here

| Component | Type | Trigger |
|---|---|---|
| `windmill-build` | Skill (reference / auto-load) | Front door for any "I want to automate X" / "build me a tool" / "make a button" request. Confirms the dev loop works, checks if the tool already exists, asks 3 plain-language questions, routes to the matching authoring skill. |
| `windmill-discover` | Skill (reference / auto-load) | "What tools already exist?" / "Is there a tool for X?" — combines curated `toolbox.md` + live MCP listings, recommends a verdict (use this, adapt this, or build new). |
| `windmill-debug` | Skill (reference / auto-load) | "My tool failed / errored / didn't run / timed out" — fetches the failing job's logs + result, diagnoses in plain English, suggests a concrete fix. NOT for dev-loop setup problems (`/wmill-doctor` owns that). |
| `windmill-ask` | Skill (reference / auto-load) | Front door for any *question* about Windmill — "how does X work", "does Windmill support Y", "where are the docs" — that doesn't map to build/discover/debug/setup. Thin router: defers to the specific skill when one fits, otherwise points at the platform docs + matching pattern skill. |
| `docs/getting-started.md` | Reference | Plain-English overview — what you can build, how it's run, what's safe, when to use which entity type. |

### First-time dev loop setup

| Component | Type | Trigger |
|---|---|---|
| `hallow-windmill` | Skill (reference / auto-load) | Loads when a user asks to set up Windmill, install wmill, connect to `windmill.platform.hallow.app`, mint a token, or wire up the Windmill MCP. Points at the driver skills below. |
| `/hallow-windmill:wmill-setup` | Skill (user-invocable only) | Drives the full onboarding flow end-to-end. `disable-model-invocation: true` — Claude cannot auto-trigger it because it writes `.mcp.json` and touches tokens. |
| `/hallow-windmill:wmill-doctor` | Skill (read-only) | Runs the smoke test anytime to verify the dev loop still works. Safe to invoke proactively when the user reports breakage. |
| `windmill-onboarder` | Subagent | Runs the full onboarding flow in an isolated context window. Use when the parent thread is doing unrelated work or the user wants the flow off the main context. |
| `docs/onboarding.md` | Authoritative doc | Source of truth for setup. |
| `docs/mcp.json.example.jsonc` | Reference template | Reference shape for the project-scoped `.mcp.json`. Not auto-loaded — `/wmill-setup` writes the real config via `claude mcp add -s project` using your real token. |

### Authoring skills

Windmill authoring skills, copied from the platform repo so the plugin is self-contained.

**Naming convention** — three groups, each prefix has a fixed meaning:

| Prefix | Meaning | Examples |
|---|---|---|
| `write-*` | Code/format authoring (per language or per definition format) | `write-script-bun`, `write-flow`, `write-workflow-as-code` |
| `windmill-*` | User-facing workflow / UX skills (front doors, not entity authors) | `windmill-build`, `windmill-debug`, `windmill-discover`, `windmill-ask` |
| (bare noun) | One Windmill entity type or operational concept | `triggers`, `schedules`, `resources`, `raw-app`, `preview`, `cli-commands` |

Skills:

- `write-flow` — flows (YAML)
- `write-workflow-as-code` — workflow-as-code scripts (TS / Python, decorator-driven)
- `write-script-bun` (default) plus `write-script-python3`, `write-script-postgresql`, `write-script-duckdb`
- `raw-app` — UI apps
- `triggers` — HTTP routes / webhooks, plus WebSocket / Postgres-CDC / MQTT (OSS-native) and SQS (enabled by a Hallow fork deviation). Hallow runs a customized OSS fork; Kafka / NATS / GCP / Azure and the email-routing trigger remain EE-only and not available — see the skill's availability matrix
- `schedules` — cron schedules
- `resources` — resources + resource types
- `preview` — open the Windmill dev page for visual verification
- `cli-commands` — `wmill` CLI surface, job inspection, preview-vs-run-vs-push decisions

These auto-load when you tell Claude what you want to build; you almost never invoke them directly.

### Reference

| Component | Type | Trigger |
|---|---|---|
| `windmill-patterns` | Skill (reference / auto-load) | Loads when a user is asking about Hallow-specific conventions: shared atoms (`slack_post`, `error_to_slack`, `assert_principal`), the `f/platform_secrets/` pattern, local-yaml-first workflow, `permissioned_as` elevation. Reference; defers actual editing to the authoring skills. |
| `windmill-build-reviewer` | Subagent (read-only critic) | Pre-push gate. Reviews a locally-authored entity against `docs/build-policy.md` and returns PASS or findings; never edits, never pushes. The authoring skills and `windmill-build` Step 4 route to it before the MCP push (build-policy GATE.1). |
| `windmill-capture-learning` | Skill (model-invocable) | Records a Windmill behavior, CLI quirk, or doc-contradiction discovered mid-session. Writes a memory entry + a scratchpad bullet in `WINDMILL_LEARNINGS.md` so the finding survives the session. |
| `docs/build-policy.md` | Authoritative doc | Consolidated pre-push ruleset (GATE + GEN + per-entity `SCRIPT/FLOW/TRIG/SCHED/RES/APP` rules) that `windmill-build-reviewer` checks against. Single source of truth; a `wmill-drain-learnings` target for reviewable gotchas. |
| `docs/patterns.md` | Authoritative doc | Hallow conventions: entity creation, on-disk file shapes, shared atoms catalog, secrets pattern, flow conventions, operational rules. (Engineer-oriented.) |
| `docs/folders-groups.md` | Reference | Folder/group ACL semantics. |
| `docs/toolbox.md` | Reference | Catalog of existing shared tools in the `dev` workspace. |
| `docs/installing.md` | Reference | User-facing install runbook (GitHub Desktop, terminal, ZIP). |
| `WINDMILL_LEARNINGS.md` | Scratchpad | Append-only log of discoveries written by `windmill-capture-learning`. Drained into proper docs periodically. |

## Install

Full runbook (GitHub Desktop, terminal, ZIP, troubleshooting): [`docs/installing.md`](./docs/installing.md). Short form:

```bash
git clone https://github.com/hallow-inc/claude-plugins.git \
  ~/.claude/plugins/marketplaces/hallow-claude-plugins
```

Then in Claude Code:

```
/plugin marketplace add ~/.claude/plugins/marketplaces/hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

Then either:

- **First-time setup:** ask "set me up with Windmill" → the `hallow-windmill` reference skill auto-loads and tells you which command to run next. Or run `/hallow-windmill:wmill-setup` directly.
- **Building a tool:** ask "I want to automate X" / "build me a tool that does Y" / "make a button that runs Z" → `windmill-build` auto-loads, asks three questions, and drives the rest.
- **Finding an existing tool:** ask "what tools exist for X" / "is there an atom for Slack/S3/Snowflake" → `windmill-discover` reads `toolbox.md` + live MCP and recommends what to use.
- **A tool failed:** ask "my tool errored" / "the job failed" / "the schedule didn't fire" → `windmill-debug` fetches logs and diagnoses.
- **A question about Windmill:** ask "how do Windmill schedules handle timezones" / "does Windmill support SQS" / "where are the Windmill docs" → `windmill-ask` routes to the right doc or skill.
- **Reference questions about Hallow conventions:** ask about shared atoms, secrets, ACLs, or local-yaml-first → `windmill-patterns` auto-loads and points at the right doc.

## Verify later

`/hallow-windmill:wmill-doctor` runs the end-to-end smoke test and reports which step failed.

## Stay current

The plugin ships frequently — new shared atoms, new conventions, new gotchas wired into skills. Pull updates weekly (or any time Claude contradicts what a colleague told you):

```
/plugin marketplace update hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

`plugin.json` carries no `version` field — every master commit is effectively a new release, so `/plugin marketplace update` always picks up the latest. Changelog: `infra/windmill/docs/changelog.md` in the platform repo.

If Claude suggests something stale (e.g. `wmill sync push`, a removed atom, an old folder convention), update before debugging. The fix has often already shipped.

## Platform support

macOS, Linux/WSL, native Windows (PowerShell 5.1+).

## Hard rules baked in

- Never `wmill sync push` / `wmill sync pull` (banned at Hallow — they delete server state not in local files and clobber secret variables). All entity mutations go via MCP `windmill` tools or the Windmill UI.
- Never commit tokens.
- Always pass `--workspace dev` on wmill commands.
- DuckLake work is Python only, always tagged `fargate`, using `from f.platform.ducklake.lib import connect`. Never the DuckDB script kind.
- Workdir for any tool work: `~/dev/wmill/` (macOS/Linux/WSL) or `%USERPROFILE%\dev\wmill\` (Windows). All bundled skills load from any directory — the workdir matters only because that's where the project-scoped `.mcp.json` lives.

See `docs/onboarding.md` for the full procedure.

## Authoritative platform docs

The platform repo (`infra/windmill/docs/`) hosts the canonical user-facing doc set — Tutorial, How-to, Reference, Explanation, Architecture, Data flows, Workflow patterns, Possibilities, Building apps, Troubleshooting, Glossary, Changelog, Atoms catalog, Access matrix, Ask checklist. The plugin does NOT mirror them — it references them. When users need conceptual / discovery / lookup content beyond what's in `docs/` here, point at the platform docs index (`infra/windmill/docs/index.md`).

Plugin-local `docs/` carries plugin-specific material only: onboarding procedure, Hallow-specific patterns (`patterns.md`), shared-tool template, install runbook.

## Layout

```
plugins/hallow-windmill/
├── .claude-plugin/
│   └── plugin.json
├── README.md                        ← this file
├── WINDMILL_LEARNINGS.md            ← capture-as-you-go scratchpad
├── agents/
│   ├── windmill-onboarder.md        ← isolated-context subagent
│   └── windmill-build-reviewer.md   ← read-only pre-push critic (build-policy GATE.1)
├── docs/
│   ├── getting-started.md           ← plain-English overview (what windmill-build reads)
│   ├── installing.md                ← user-facing install runbook
│   ├── onboarding.md                ← authoritative setup procedure
│   ├── mcp.json.example.jsonc       ← reference .mcp.json shape (jsonc — has // comments)
│   ├── build-policy.md              ← consolidated pre-push ruleset (reviewer reads this)
│   ├── patterns.md                  ← Hallow conventions for entity creation/usage
│   ├── folders-groups.md            ← ACL semantics
│   └── toolbox.md                   ← shared-tool catalog (dev workspace)
└── skills/
    ├── windmill-build/              ← front door for "build me a tool" (auto-load)
    ├── windmill-discover/           ← "what already exists" (auto-load)
    ├── windmill-debug/              ← "my tool failed" (auto-load)
    ├── windmill-ask/                ← "a question about Windmill" (auto-load, front-door router)
    ├── windmill-onboarding/         ← reference skill, name `hallow-windmill` (auto-load, setup)
    ├── wmill-setup/                 ← drives onboarding flow (user-invocable)
    ├── wmill-doctor/                ← smoke test (read-only)
    ├── windmill-patterns/           ← reference skill (auto-load, conventions)
    ├── windmill-capture-learning/   ← records gotchas to memory + WINDMILL_LEARNINGS.md
    ├── write-flow/                  ← creates flows
    ├── write-script-bun/            ← default scripting (TypeScript / Bun)
    ├── write-script-python3/        ← Python scripts
    ├── write-script-{postgresql,duckdb}/
    ├── raw-app/                     ← UI apps
    ├── triggers/                    ← HTTP / WebSocket / Postgres-CDC / MQTT / SQS triggers
    ├── schedules/                   ← cron schedules
    ├── resources/                   ← resources + resource types
    ├── write-workflow-as-code/      ← workflow-as-code scripts
    ├── preview/                     ← visual preview of an entity
    └── cli-commands/                ← debugging via wmill job CLI
```

Note: the reference skill's folder is `windmill-onboarding/` but its `name:` frontmatter is `hallow-windmill` — that's the identifier Claude Code uses to invoke it. Folder names are not user-facing.

## Development

```bash
# Run this plugin without installing (loads for the session only)
claude --plugin-dir ./plugins/hallow-windmill

# Validate the manifest + frontmatter
claude plugin validate .

# Reload after editing
/reload-plugins
```

Bump `version` in `.claude-plugin/plugin.json` when shipping a change users should pick up. Per the Claude Code docs: if `version` is set, pushing new commits without bumping it does nothing — Claude Code sees the same version string and keeps the cached copy. Omit `version` to make every commit a new version (fine for internal/active development).
