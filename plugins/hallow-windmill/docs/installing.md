# Installing the Hallow Windmill plugin

For Hallow team members installing the `hallow-windmill` plugin for the first time, or updating to the latest version.

The plugin is distributed via the public GitHub repo [`hallow-inc/claude-plugins`](https://github.com/hallow-inc/claude-plugins). Source is public but use rights are restricted to Hallow personnel per [`LICENSE`](https://github.com/hallow-inc/claude-plugins/blob/master/LICENSE). No GitHub auth required.

The plugin is harmless without our backend — Windmill is tailnet-only, so non-Hallow people who clone it can't use it anyway.

## Prerequisites

**Claude Code installed** — `docs.anthropic.com/en/docs/claude-code`. That's it.

## Install

In Claude Code, paste these three commands:

```
/plugin marketplace add hallow-inc/claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

Claude Code handles the clone for you (cached under `~/.claude/plugins/marketplaces/hallow-claude-plugins/`). No `git clone`, no auth setup, no path-typing.

> ⚠️ **Name asymmetry**: GitHub repo = `claude-plugins`. Marketplace name = `hallow-claude-plugins`. The `marketplace add` command takes the repo (`hallow-inc/claude-plugins`). Every other plugin command (`install`, `update`, `remove`, `uninstall`) takes the marketplace name (`hallow-claude-plugins`).

## Use it

Start a fresh Claude Code session and say one of:

- `set me up with Windmill` — first-time Windmill dev-loop setup
- `I want to automate something` — build a tool
- `what Windmill tools already exist` — discover what's already built
- `my Windmill tool failed` — debug a failing tool

The plugin auto-loads the right skill based on what you say.

## Update

```
/plugin marketplace update hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

- `marketplace update` fetches new commits from GitHub.
- `install` re-resolves the plugin against the refreshed marketplace.
- `/reload-plugins` re-reads files into the running session.

**Warning**: re-running `/plugin marketplace add` on an already-registered marketplace does **not** fetch new commits. Use `update`.

## Uninstall / clean reinstall

```
/plugin uninstall hallow-windmill@hallow-claude-plugins
/plugin marketplace remove hallow-claude-plugins
```

Then re-run the Install steps.

## Pinning to a specific version (manual clone fallback)

`/plugin marketplace add hallow-inc/...` always tracks the latest commit on `master`. To pin to a specific SHA, fall back to a manual clone:

```bash
# macOS / Linux / WSL
git clone https://github.com/hallow-inc/claude-plugins.git \
  $HOME/.claude/plugins/marketplaces/hallow-claude-plugins
cd $HOME/.claude/plugins/marketplaces/hallow-claude-plugins
git checkout <commit-sha>
```

```powershell
# Windows PowerShell
$dest = "$env:USERPROFILE\.claude\plugins\marketplaces\hallow-claude-plugins"
New-Item -ItemType Directory -Path (Split-Path $dest) -Force | Out-Null
git clone https://github.com/hallow-inc/claude-plugins.git $dest
cd $dest
git checkout <commit-sha>
```

Then in Claude Code:

```
/plugin marketplace add $HOME/.claude/plugins/marketplaces/hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

To go back to latest: `git checkout master && git pull` in the clone, then `/reload-plugins`. (You don't need `/plugin marketplace update` here because Claude Code reads directly from your local clone path.)

## Air-gapped / no GitHub access (manual ZIP fallback)

If your machine cannot reach GitHub at all:

1. On a machine with internet: browse to `https://github.com/hallow-inc/claude-plugins`, **Code** → **Download ZIP**.
2. Transfer the ZIP to the target machine.
3. Extract. Rename the extracted folder to `hallow-claude-plugins` (no `-main` / `-master` suffix).
4. Move to `$HOME/.claude/plugins/marketplaces/hallow-claude-plugins` (Linux/macOS) or `%USERPROFILE%\.claude\plugins\marketplaces\hallow-claude-plugins` (Windows).
5. In Claude Code: `/plugin marketplace add $HOME/.claude/plugins/marketplaces/hallow-claude-plugins` (or Windows path).

Updates require re-downloading the ZIP. Use the github-source install above wherever possible.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `marketplace add` returns `(no content)` | Marketplace already registered under that name | Use `/plugin marketplace update hallow-claude-plugins` to refresh, then re-install. |
| Plugin installed but skills don't auto-load | `/reload-plugins` not run | Run `/reload-plugins` in Claude Code. |
| `Plugin already installed` after update | Stale plugin state | `/plugin uninstall hallow-windmill@hallow-claude-plugins` then `/plugin install hallow-windmill@hallow-claude-plugins` then `/reload-plugins`. |
| Want a clean reinstall | Stale install dir | `/plugin marketplace remove hallow-claude-plugins`, then `/plugin marketplace add hallow-inc/claude-plugins`. |
| `Permission denied (publickey)` on manual clone | Trying SSH without a key on file | Use HTTPS instead (the `https://github.com/...` URL). Or add an SSH key to your GitHub account. |
| `marketplace.json not found` after `marketplace add <local-path>` | Wrong path, or clone was incomplete | Confirm: `ls $HOME/.claude/plugins/marketplaces/hallow-claude-plugins/.claude-plugin/marketplace.json`. If missing, the clone failed — retry. |

## Questions

- Slack: `#platform`
- DM: `@brandon`

## What's actually installed

Audit before installing:

- **Plugin source**: `plugins/hallow-windmill/` in [`hallow-inc/claude-plugins`](https://github.com/hallow-inc/claude-plugins).
- **No secrets** in the plugin. No tokens, no credentials. The plugin reads Hallow-specific values (workspace names, resource paths) from your own Windmill instance + `f/platform_secrets/` (admin-managed, never bundled).
- **Distribution model**: every commit on `master` is a new version. There is no separate "release" step. `/plugin marketplace update` is your refresh.
