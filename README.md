# hallow-claude-plugins

Claude Code plugin marketplace for the Hallow team. Private repo. Install is `git clone` + local-path marketplace.

## Install (users)

```bash
# 1. Clone the repo (whatever auth you already have — gh CLI, SSH key, https + PAT, GitHub Desktop)
git clone https://github.com/hallow-inc/hallow-claude-plugins.git ~/.claude/plugins/marketplaces/hallow-claude-plugins

# 2. In Claude Code, register the marketplace and install the plugin
claude
> /plugin marketplace add ~/.claude/plugins/marketplaces/hallow-claude-plugins
> /plugin install hallow-windmill@hallow-claude-plugins
> /reload-plugins
```

Full walkthrough (incl. troubleshooting, Windows paths, no-CLI options): [`plugins/hallow-windmill/docs/installing.md`](./plugins/hallow-windmill/docs/installing.md).

**Updates**: `cd` into the clone, `git pull`, then `/reload-plugins` in Claude Code.

## Available plugins

| Plugin id | Folder | Purpose |
|---|---|---|
| `hallow-windmill` | `plugins/hallow-windmill/` | Build automation tools on Hallow's self-hosted Windmill. Includes `/wmill-setup`, `/wmill-doctor`, onboarder subagent, `windmill-build` / `windmill-discover` / `windmill-debug` entry skills, `windmill-capture-learning` for recording gotchas, full set of authoring skills (write-flow, write-script-*, raw-app, triggers, schedules, resources, preview, cli-commands), and reference docs. macOS, Linux/WSL, native Windows. |

Folder names are `hallow-` prefixed to mark ownership; the plugin `name` field in `.claude-plugin/plugin.json` is the identifier users see when installing and invoking.

## Local development

```bash
# Test a plugin without installing (loads for one session only)
claude --plugin-dir ./plugins/hallow-windmill

# Add this marketplace from a local path
/plugin marketplace add /Users/<you>/dev/hallow-claude-plugins

# Validate the marketplace + plugin manifests
claude plugin validate .

# Reload after editing a plugin
/reload-plugins
```

## Adding a new plugin

1. `mkdir -p plugins/hallow-<name>/.claude-plugin` and create `plugins/hallow-<name>/.claude-plugin/plugin.json`.
2. Add skills under `plugins/hallow-<name>/skills/<skill-name>/SKILL.md`.
3. Add agents under `plugins/hallow-<name>/agents/<agent>.md`.
4. (Optional) Hooks at `hooks/hooks.json`, MCP servers at `.mcp.json`, LSP servers at `.lsp.json`.
5. Register the plugin in `.claude-plugin/marketplace.json` under `plugins`. Set `source` to the relative folder path; the plugin `name` in marketplace.json must match the `name` in `plugin.json`.
6. Test locally: `claude --plugin-dir ./plugins/hallow-<name>`.
7. Validate: `claude plugin validate .`
8. Bump `version` in `plugin.json` when shipping a change users should pick up — omitting `version` makes every commit a new version, which is fine for internal/active development.

## Conventions

- **Folder names** use a `hallow-` prefix (`hallow-windmill`, `hallow-foo`) to mark ownership inside this repo.
- **Plugin ids** (the `name` field) are short and user-facing (`hallow-windmill`, `foo`). They drive the install command and slash-command namespace (`/hallow-windmill:wmill-setup`).
- **Components live at the plugin root**, not inside `.claude-plugin/`. Only `plugin.json` belongs in `.claude-plugin/`.
- **Plugin-level `.mcp.json` is forbidden** for servers that require user secrets — Claude Code would try to start them at session load with placeholder tokens and fail noisily. Ship an `.example` template and have a `/wmill-setup`-style flow write the real config via `claude mcp add -s project`.

## Hosting + distribution

Private repo: `github.com/hallow-inc/hallow-claude-plugins`. All install paths reduce to `git clone` + `/plugin marketplace add <local-path>`. No tarballs, no S3, no Windmill side. The repo IS the distribution channel.

Each user needs read access to the repo. The plugin doesn't dictate how they auth — anything that gets a working `git clone` is fine: GitHub Desktop, `gh` CLI, SSH key, HTTPS + credential helper, manual ZIP download.

## Versioning

Plugins **omit the `version` field** in `plugin.json` — every commit on `main` is a new version. Users update by `git pull` in their local clone, then `/reload-plugins`.

To pin to a specific commit, check out that SHA in the clone (`git checkout <sha>`) and `/reload-plugins`.

See [`CHANGELOG.md`](./CHANGELOG.md) for human-readable change history.

## References

Plugin-specific runbooks live in `plugins/<folder>/docs/`. Official Claude Code docs: `docs.anthropic.com/en/docs/claude-code` — see `plugins-reference`, `skills`, `sub-agents`, `slash-commands`, `plugin-marketplaces`.
