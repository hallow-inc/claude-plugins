# hallow-claude-plugins

Claude Code plugin marketplace for the Hallow team. Public repo, **internal-use license** — see [`LICENSE`](./LICENSE). Source is visible to anyone but only Hallow employees, contractors, and authorized agents are granted rights to use it.

## Install (users)

In Claude Code:

```
/plugin marketplace add hallow-inc/claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

Claude Code handles the clone for you (cached under `~/.claude/plugins/marketplaces/hallow-claude-plugins/`). No `git clone`, no auth setup required — repo is public.

> ⚠️ **Name asymmetry**: the GitHub repo is `claude-plugins`; the marketplace name (used in `install` / `update` commands) is `hallow-claude-plugins`. Don't conflate them. `marketplace add` takes the repo, `install` / `update` / `remove` take the marketplace name.

Full walkthrough (incl. troubleshooting, Windows paths, no-CLI options): [`plugins/hallow-windmill/docs/installing.md`](./plugins/hallow-windmill/docs/installing.md).

## Update

```
/plugin marketplace update hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

The `marketplace update` step fetches new commits. The `install` step re-resolves the plugin against the fresh marketplace. `/reload-plugins` re-reads files into the running session.

Re-running just `/plugin marketplace add` on an already-registered marketplace does **not** fetch new commits — use `/plugin marketplace update` to refresh.

To pin to a specific commit, install with explicit ref (if supported) or fall back to manual clone (see [`installing.md`](./plugins/hallow-windmill/docs/installing.md)).

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

Public repo: `github.com/hallow-inc/claude-plugins`. Primary install path is `/plugin marketplace add hallow-inc/claude-plugins` (Claude Code clones + caches). Fallback is manual `git clone` into `~/.claude/plugins/marketplaces/` for users behind GitHub-blocking networks or who want pinned SHAs. The repo IS the distribution channel — no tarballs, no S3, no Windmill side.

Source is publicly visible; use rights are restricted to Hallow personnel per [`LICENSE`](./LICENSE). No GitHub auth required to clone — HTTPS clone works anonymously.

The plugin itself is harmless without tailnet access: Windmill (`windmill.platform.hallow.app`) is tailnet-only, so an outsider who clones the repo cannot reach the backend.

## Licensing

Every plugin here is under the Hallow Internal Use License ([`LICENSE`](./LICENSE)), which limits use to work for Hallow.

**Consent for hallow-assurance in personal repositories (2026-10-19).** Under §2 of `LICENSE`, Hallow, Inc. gives its written consent for every current Authorized User to use, copy, and run hallow-assurance (`plugins/hallow-assurance/`) in their personal repositories, including work not performed for Hallow. The consent covers no other plugin and grants no right to distribute, sublicense, or publish the Software. It ends for a person when their license terminates under §3.

## Versioning

Plugins **omit the `version` field** in `plugin.json` — every commit on `master` is a new version. Users update via `/plugin marketplace update hallow-claude-plugins` + `/plugin install hallow-windmill@hallow-claude-plugins` + `/reload-plugins`.

Exception: `hallow-assurance` sets `version` in its `plugin.json`, equal to its release tag `plugins/hallow-assurance/vX.Y.Z`, so the plugin and its `assure` binaries stay on the same hook protocol. It updates only on a release. Its marketplace entry carries no `version`.

Change history: `git log` is authoritative.

## References

Plugin-specific runbooks live in `plugins/<folder>/docs/`. Official Claude Code docs: `docs.anthropic.com/en/docs/claude-code` — see `plugins-reference`, `skills`, `sub-agents`, `slash-commands`, `plugin-marketplaces`.
