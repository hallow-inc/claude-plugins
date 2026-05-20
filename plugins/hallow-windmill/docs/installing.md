# Installing the Hallow Windmill plugin

For Hallow team members installing the `hallow-windmill` plugin for the first time, or updating to the latest version.

This plugin is distributed via a private GitHub repo, `hallow-inc/hallow-claude-plugins`. To install you need read access to that repo. Ping `#platform` if you don't have it yet.

## Prerequisites

1. **Claude Code installed** — `docs.anthropic.com/en/docs/claude-code`.
2. **GitHub account joined to `hallow-inc`** with read on `hallow-claude-plugins`.
3. **Some way to `git clone` from GitHub** — pick whichever you already have working:
   - **GitHub Desktop** (no terminal — easiest for non-devs)
   - **`gh` CLI** (`gh auth login`)
   - **SSH key** added to your GitHub account
   - **HTTPS + credential helper** (macOS Keychain / Windows Credential Manager — default if you've used `git` before)
   - **Manual ZIP download** (no clone needed; see below)

If none of those work yet, the simplest from zero: install GitHub Desktop (`desktop.github.com`), sign in with your `@hallow.app` GitHub account, done.

## Install

### Option 1 — GitHub Desktop (no terminal)

1. Open GitHub Desktop → **File** → **Clone repository**.
2. Search for `hallow-inc/hallow-claude-plugins`.
3. Local path: `~/.claude/plugins/marketplaces/hallow-claude-plugins` (macOS/Linux) or `%USERPROFILE%\.claude\plugins\marketplaces\hallow-claude-plugins` (Windows).
4. Click **Clone**.

### Option 2 — Terminal

```bash
# macOS / Linux / WSL
mkdir -p ~/.claude/plugins/marketplaces
git clone https://github.com/hallow-inc/hallow-claude-plugins.git \
  ~/.claude/plugins/marketplaces/hallow-claude-plugins
```

```powershell
# Windows PowerShell
$dest = "$env:USERPROFILE\.claude\plugins\marketplaces\hallow-claude-plugins"
New-Item -ItemType Directory -Path (Split-Path $dest) -Force | Out-Null
git clone https://github.com/hallow-inc/hallow-claude-plugins.git $dest
```

First time you run `git clone` on a private repo it will prompt for GitHub credentials. Username = your GitHub username. Password = a personal access token (NOT your GitHub password). To make a token:

1. https://github.com/settings/tokens?type=beta
2. **Generate new token** → name `claude-plugins` → expiration `1 year` → **Only select repositories** → `hallow-inc/hallow-claude-plugins` → **Repository permissions** → set **Contents** to **Read-only** → **Generate**
3. Copy. Paste into the git password prompt when asked.

The credential helper caches it after first use; you won't be prompted again.

### Option 3 — Manual ZIP (no git, no terminal)

If even `git clone` is too much:

1. Browse to `https://github.com/hallow-inc/hallow-claude-plugins` in your browser (you must be signed in to GitHub).
2. **Code** → **Download ZIP**.
3. Extract the ZIP. Rename the extracted folder to `hallow-claude-plugins` (no `-main` suffix).
4. Move it to `~/.claude/plugins/marketplaces/` (or `%USERPROFILE%\.claude\plugins\marketplaces\` on Windows). Final path must be `~/.claude/plugins/marketplaces/hallow-claude-plugins`.

Updates require re-downloading the ZIP. The terminal / GitHub Desktop options are nicer for that.

## Register the plugin

Open Claude Code from any directory:

```bash
claude
```

Then paste these three commands inside Claude Code:

```
/plugin marketplace add ~/.claude/plugins/marketplaces/hallow-claude-plugins
/plugin install hallow-windmill@hallow-claude-plugins
/reload-plugins
```

Windows: use `%USERPROFILE%\.claude\plugins\marketplaces\hallow-claude-plugins` for the path.

## Use it

Start a fresh Claude Code session and say one of:

- `set me up with Windmill` — first-time Windmill dev-loop setup
- `I want to automate something` — build a tool
- `what Windmill tools already exist` — discover what's already built
- `my Windmill tool failed` — debug a failing tool

The plugin auto-loads the right skill based on what you say.

## Updating

### GitHub Desktop

1. Open GitHub Desktop.
2. Select the `hallow-claude-plugins` repo.
3. Click **Fetch origin** → **Pull origin**.
4. In Claude Code: `/reload-plugins`.

### Terminal

```bash
cd ~/.claude/plugins/marketplaces/hallow-claude-plugins
git pull
```

Then in Claude Code: `/reload-plugins`.

### Manual ZIP

Re-download per the manual ZIP install steps above. Overwrite the existing folder. Then `/reload-plugins`.

## Pinning to a specific version

Check out the commit SHA in the clone:

```bash
cd ~/.claude/plugins/marketplaces/hallow-claude-plugins
git checkout <commit-sha>
```

Then `/reload-plugins`. To go back to latest: `git checkout main && git pull`.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `git clone` says `Repository not found` | No read access on the repo | Ping `#platform` to request access. Confirm at https://github.com/hallow-inc/hallow-claude-plugins (must be signed in to GitHub). |
| `git clone` prompts forever and fails | Wrong PAT scope, or PAT expired | Mint a new PAT per the steps above. Repository permissions → Contents read. |
| `Permission denied (publickey)` | Trying SSH without a key on file | Use HTTPS instead (the `https://github.com/...` URL). Or add an SSH key to your GitHub account. |
| `marketplace.json not found` after `/plugin marketplace add` | Wrong path, or clone was incomplete | Confirm: `ls ~/.claude/plugins/marketplaces/hallow-claude-plugins/.claude-plugin/marketplace.json`. If missing, the clone failed — retry. |
| Plugin installed but skills don't auto-load | `/reload-plugins` not run | Run `/reload-plugins` in Claude Code. |
| `Plugin already installed` after update | Stale plugin state | `/plugin uninstall hallow-windmill@hallow-claude-plugins` then `/plugin install hallow-windmill@hallow-claude-plugins` then `/reload-plugins`. |
| Want a clean reinstall | Stale install dir | Delete `~/.claude/plugins/marketplaces/hallow-claude-plugins`, re-clone, re-add marketplace. |

## Questions

- Slack: `#platform`
- DM: `@brandon`

## What's actually installed

Audit before installing:

- **Plugin source**: `plugins/hallow-windmill/` in [`hallow-inc/hallow-claude-plugins`](https://github.com/hallow-inc/hallow-claude-plugins).
- **No secrets** in the plugin. No tokens, no credentials. The plugin reads Hallow-specific values (workspace names, resource paths) from your own Windmill instance + `f/platform_secrets/` (admin-managed, never bundled).
- **Distribution model**: every commit on `main` is a new version. There is no separate "release" step. `git pull` is your update.
