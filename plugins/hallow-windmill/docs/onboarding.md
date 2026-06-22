# Windmill onboarding (Claude Code edition)

Audience: Claude Code, walking a new Hallow engineer from zero to a working Windmill dev loop.

Outcome: user can sign in, list workspaces, edit YAML locally, and have Claude Code (terminal CLI **or** VS Code extension) drive scripts/flows/apps via the Windmill MCP + `wmill` CLI from a single canonical dir.

Supports: **macOS, Linux/WSL, native Windows**. Per platform, follow the matching tab — do not blend.

**Read this top-to-bottom before starting.** Each section is a precondition for the next.

---

## 0. Preconditions (verify before doing anything)

Run all five. If any fail, stop and follow the matching fix in §1–§5 before continuing.

### macOS / Linux / WSL (bash or zsh)

```bash
# 1. Tailnet membership
tailscale status | head -3

# 2. Tailnet DNS resolves the Windmill host
#    macOS/Linux: dig.  WSL: same.
dig +short windmill.platform.hallow.app | head -1

# 3. Browser reachability (200/302 = up; anything else = not on tailnet)
curl -sS -o /dev/null -w '%{http_code}\n' https://windmill.platform.hallow.app/

# 4. wmill CLI present + version
wmill --version 2>/dev/null || echo "wmill not installed"

# 5. claude CLI present — assumed (you are already running it). Sanity-check that
#    the binary is on PATH for the `claude mcp add` call in §7.
claude --version 2>/dev/null || echo "claude not on PATH — fix shell env before §7"
```

### Native Windows (PowerShell 5.1 OR PowerShell 7+)

All Windows blocks in this doc are written to run on the default Windows PowerShell 5.1 shipped with Windows 10/11. They also work in `pwsh` (PS 7+). If a command requires PS 7+, the doc calls it out explicitly.

```powershell
# 1. Tailnet membership
tailscale status | Select-Object -First 3

# 2. Tailnet DNS resolves the Windmill host
#    (Resolve-DnsName ships in the DnsClient module, default on Windows 10/11 desktop.)
Resolve-DnsName windmill.platform.hallow.app -Type A | Select-Object -First 1

# 3. Browser reachability — PS 5.1-compatible try/catch (no -SkipHttpErrorCheck).
try {
  $r = Invoke-WebRequest -Uri https://windmill.platform.hallow.app/ -UseBasicParsing
  Write-Host $r.StatusCode
} catch [System.Net.WebException] {
  # 3xx/4xx/5xx land here on PS 5.1; pull the actual status code out.
  if ($_.Exception.Response) {
    Write-Host ([int]$_.Exception.Response.StatusCode)
  } else {
    Write-Host "no response — likely off-tailnet"
  }
}

# 4. wmill CLI present + version
try { wmill --version } catch { Write-Host "wmill not installed" }

# 5. claude CLI present — assumed; sanity-check it's on PATH for §7.
try { claude --version } catch { Write-Host "claude not on PATH — fix shell env before §7" }
```

### Expected output (all platforms)
- `tailscale status` shows `Logged in.` and a `100.x.x.x` IP for `<user>@hallow.app`.
- DNS resolves to `100.x.x.x` (NOT a public IP).
- Reachability returns `200` or `302`.
- `wmill --version` ≥ `1.699.0` (minimum: earlier versions lack consistent `--workspace` flag coverage on subcommands used in this doc).
- `claude --version` returns any version (assumed already installed; PATH-check only — install instructions live at `docs.anthropic.com/en/docs/claude-code`, out of scope for this doc).

If any check fails, the user is not ready. Fix the failing precondition first. Do **not** attempt to "work around" by, e.g., setting `WMILL_URL` to a public host — Windmill is tailnet-only by design.

---

## 1. Tailscale

### Install + login

**macOS:** App Store ("Tailscale") or `brew install --cask tailscale`. Launch from menu bar → Log in → Google → `<user>@hallow.app`.

**Linux/WSL:** `curl -fsSL https://tailscale.com/install.sh | sh`, then `sudo tailscale up --accept-routes --accept-dns`. Browser opens for Google SSO.

**Windows:** Download from `https://tailscale.com/download/windows` and run installer (or `winget install Tailscale.Tailscale`). Launch from system tray → Log in → Google → `<user>@hallow.app`.

User must authenticate with their `@hallow.app` Google account. SSO + tailnet ACL gate them into `group:dev` (or whichever group an admin assigned). If their device does not appear in the Tailscale admin console after login, ask a platform admin to provision the tailnet user.

### Verify tailnet-only reachability
`windmill.platform.hallow.app` must resolve to `100.x.x.x`. If it resolves to a public IP, the device is using public DNS:
- macOS/Linux/WSL: `sudo tailscale up --accept-dns`
- Windows: Tailscale tray icon → Preferences → enable **Use Tailscale DNS**

---

## 2. Windmill web login

URL: `https://windmill.platform.hallow.app/`

1. Open the URL in a browser **while connected to the tailnet**.
2. Sign in with the `@hallow.app` Google account (same account as Tailscale).
3. Confirm the workspace selector shows `dev`. If multiple workspaces appear, default to `dev` unless an admin scoped them elsewhere.

The user must reach the dashboard at least once via the browser before the CLI/MCP steps — first login provisions their account row server-side.

---

## 3. Mint API token (one-time, used by CLI + MCP)

This token is the credential both `wmill` and the Windmill MCP will use. Mint once; reuse for both.

1. Windmill UI: top-right user menu → **Account settings** → **Tokens**.
2. Click **+ New token**.
3. Label: `claude-code-local-<username>`. For CI tokens (push/drift), do NOT use this flow — ask a platform admin; CI tokens are minted against a bot user, not your personal account.
4. Expiration: longest the policy allows. Rotate when expired or compromised.
5. Scopes: default (workspace-scoped, full access) unless an admin specified narrower scopes.
6. Copy the token. **You will not see it again.** Store in the user's password manager immediately.

Token format: opaque base64-ish string. Paste exactly as shown by the UI — do not truncate, do not trim trailing chars.

Do **not** commit this token. Do **not** paste it into `wmill.yaml` under version control.

---

## 4. Install the `wmill` CLI

Assumes `claude` (Claude Code CLI and/or VS Code extension) is already installed and authenticated — that is the prerequisite for following this doc at all. If it isn't, see `docs.anthropic.com/en/docs/claude-code` first.

### 4a. Verify Node ≥ 20 (required for `wmill`)

```bash
node --version            # macOS/Linux/WSL/Windows — must print v20.x or higher
```

If missing or < 20:
- **macOS:** `brew install node@20 && brew link node@20`, or `volta install node@20`, or `nvm install 20 && nvm use 20`
- **Linux/WSL:** `nvm install 20 && nvm use 20` (preferred), or distro package manager
- **Windows:** `winget install OpenJS.NodeJS.LTS`, or `nvm-windows install 20 && nvm use 20`

Do not attempt `npm install -g windmill-cli` against Node 18 — it may install but fail at runtime with cryptic module errors.

### 4b. Install `wmill` (pinned)

Same on all platforms (npm is the only supported install per windmill.dev):

```bash
npm install -g windmill-cli@1.699.0
wmill --version       # expect 1.699.0
```

**Do not** run `wmill upgrade` blind — unpinned upgrades have broken sync semantics before. To intentionally move to a newer version: `npm install -g windmill-cli@<new-version>` after testing.

If `npm install -g` errors with `EACCES`, the user's Node setup writes to a root-owned prefix. Fix the Node install (volta/nvm/fnm) rather than `sudo npm`. **Never `sudo npm install -g`.**

### 4c. Shell completions (optional)

```bash
# zsh
wmill completions zsh > "${fpath[1]}/_wmill"
# bash
wmill completions bash > /usr/local/etc/bash_completion.d/wmill
# PowerShell
wmill completions powershell | Out-String | Invoke-Expression
```

---

## 5. Pick the canonical local workdir

**Rule: one directory holds all local Windmill development for this user.** Per project policy.

### Pick the path (per platform)

| Platform | Path |
|---|---|
| macOS / Linux / WSL | `~/dev/wmill/` |
| Windows (native) | `%USERPROFILE%\dev\wmill\` (i.e. `C:\Users\<you>\dev\wmill\`) |

Same dir for every Windmill workspace the user touches (dev, prod, personal experiments). One repo-shaped tree.

### Create the dir + pre-gitignore `.mcp.json`

The `.mcp.json` written in §7 will contain a live secret. Gitignore it **before** writing it, unconditionally — covers the case where this dir is later `git init`'d or already lives inside another repo.

**macOS / Linux / WSL:**
```bash
mkdir -p ~/dev/wmill
cd ~/dev/wmill
touch .gitignore
grep -qxF '.mcp.json' .gitignore || echo '.mcp.json' >> .gitignore
```

**Windows PowerShell:**
```powershell
$wmillDir = "$env:USERPROFILE\dev\wmill"
New-Item -ItemType Directory -Path $wmillDir -Force | Out-Null
Set-Location $wmillDir
if (-not (Test-Path .gitignore) -or -not (Select-String -Path .gitignore -Pattern '^\.mcp\.json$' -Quiet)) {
  Add-Content -Path .gitignore -Value '.mcp.json'
}
```

### Why one dir
- `wmill.yaml` binds the directory to specific workspaces — switching dirs means switching configs.
- The MCP config (`.mcp.json` for Claude Code, or `.vscode/mcp.json` for VS Code-native MCP — §7) lives alongside `wmill.yaml`; tools pick it up by CWD.
- Avoids the active-workspace trap: the active workspace is stored globally in `~/.config/windmill/` (Windows: `%APPDATA%\windmill\`), not per-directory or per-shell, and persists across sessions. One canonical dir → one canonical active workspace.

### Skills load from anywhere — the plugin bundles them

Hallow's Windmill authoring skills (`write-flow`, `write-script-bun`, `raw-app`, `triggers`, `schedules`, `resources`, etc.) ship with the `hallow-windmill` plugin. Once the plugin is installed, they auto-load from any directory — no special CWD required.

Rule of thumb:
- **Any CWD works** for tool building. The plugin's skills load globally.
- **Use `~/dev/wmill/` (or Windows equivalent) as your workdir** because that's where `.mcp.json` and `wmill.yaml` live — the MCP server only attaches when CWD has the project-scoped `.mcp.json`.

**Mid-session:** if Claude is asked to author a flow/script/app, the bundled authoring skills will surface on demand. No relaunch needed.

### Initialize the dir

```bash
cd ~/dev/wmill           # or %USERPROFILE%\dev\wmill on Windows
wmill init               # writes wmill.yaml in cwd, no interactive input
```

Verify:
- macOS/Linux/WSL: `ls wmill.yaml || echo "init failed — wmill version < 1.699.0?"`
- Windows: `Test-Path wmill.yaml`

If `wmill init` ever prompts interactively (older versions did), Ctrl-C and create `wmill.yaml` manually using the template below.

### Edit `wmill.yaml`

```yaml
defaultTs: bun

# NO `token:` key. Tokens live in ~/.config/windmill/ (macOS/Linux) or
# %APPDATA%\windmill\ (Windows), set by `wmill workspace add` (§6).
# Tokens in wmill.yaml get committed by accident.
workspaces:
  dev:
    remote: https://windmill.platform.hallow.app/
    workspaceId: dev

includes:
  - f/**            # team folders; start broad, narrow per project

excludes:
  # Personal folders for ALL users including yours. Claude may create things
  # under u/<self>/ via the MCP, but never via sync push (banned — §10).
  - u/**
```

**Critical:** `wmill sync push` is banned at Hallow. It deletes server state not in local files and clobbers secret variables. All Windmill changes go through the API / UI / MCP. This local dir exists to (a) hold YAML you edit before mirroring to the server via API, and (b) house the MCP config so Claude Code can drive Windmill.

---

## 6. Register the workspace with the CLI

Non-interactive — Claude must drive this without a TTY prompt. Use the `--token` flag and suppress the token from shell history.

**macOS / Linux / WSL (zsh or bash) — suppress history correctly:**

```bash
cd ~/dev/wmill

# unset HISTFILE for the current shell so this command is not persisted.
# zsh: `setopt HIST_IGNORE_SPACE` + leading-space prefix also works but is
# unreliable across shells; `unset HISTFILE` works everywhere.
unset HISTFILE

wmill workspace add dev dev https://windmill.platform.hallow.app/ --token <TOKEN_FROM_§3>

# Verify
wmill --workspace dev workspace whoami
# Should print: <user>@hallow.app  ·  dev
```

**Windows PowerShell — clear history after the command:**

```powershell
Set-Location "$env:USERPROFILE\dev\wmill"

wmill workspace add dev dev https://windmill.platform.hallow.app/ --token <TOKEN_FROM_§3>

# Remove from PowerShell session history + persistent history file
Clear-History
$histFile = (Get-PSReadLineOption).HistorySavePath
if (Test-Path $histFile) {
  (Get-Content $histFile) | Where-Object { $_ -notmatch 'wmill workspace add' } | Set-Content $histFile
}

# Verify
wmill --workspace dev workspace whoami
```

If `wmill workspace add` still prompts despite `--token`, upgrade to the pinned version (§4b) and retry. Do NOT paste the token into an interactive prompt — Claude cannot drive a TTY and will stall.

Token is stored at:
- macOS/Linux/WSL: `~/.config/windmill/`
- Windows: `%APPDATA%\windmill\`

Never edit by hand.

### Always pass `--workspace`

Active-workspace trap: the CLI uses `wmill workspace`'s *active* selection (stored globally), not the directory. Always pass `--workspace dev` explicitly:

```bash
wmill --workspace dev script list
wmill --workspace dev job list --failed --limit 5
```

Make this a habit from day one.

---

## 7. Wire up the Windmill MCP

The Windmill MCP exposes scripts, flows, resources, and job tools so Claude Code can drive Windmill directly (list jobs, run scripts, read resources, etc.).

**The same MCP config works for the Claude Code CLI AND the Claude Code VS Code extension** — they share the same config directory (`~/.claude/` on macOS/Linux/WSL; `%USERPROFILE%\.claude\` on Windows) and the project-scoped `.mcp.json` in CWD. Register once via the CLI; both surfaces pick it up.

VS Code's *native* MCP support (used by GitHub Copilot Chat, distinct from the Claude Code extension) reads `.vscode/mcp.json` instead. If the user wants Windmill MCP available in Copilot Chat as well, do the optional §7b step.

### 7a. Register with Claude Code (required — covers CLI + VS Code extension)

Project-scoped. Token passed as `?token=` URL query param — this is Windmill's documented canonical auth form for the MCP endpoint (per windmill.dev/docs). The token ends up inside `.mcp.json` on disk, which is why §5 gitignored it. URL-in-history exposure mitigated via `unset HISTFILE` / `Clear-History` below.

**macOS / Linux / WSL:**

```bash
cd ~/dev/wmill
unset HISTFILE

# -s project   → writes .mcp.json in CWD (default would be user-global ~/.claude.json)
# -t http      → HTTP transport
claude mcp add -s project -t http windmill \
  "https://windmill.platform.hallow.app/api/mcp/w/dev/mcp?token=<TOKEN_FROM_§3>"
```

**Windows PowerShell:**

```powershell
Set-Location "$env:USERPROFILE\dev\wmill"

claude mcp add -s project -t http windmill `
  "https://windmill.platform.hallow.app/api/mcp/w/dev/mcp?token=<TOKEN_FROM_§3>"

Clear-History
$histFile = (Get-PSReadLineOption).HistorySavePath
if (Test-Path $histFile) {
  (Get-Content $histFile) | Where-Object { $_ -notmatch 'api/mcp/w/dev/mcp\?token=' } | Set-Content $histFile
}
```

This writes `.mcp.json` in CWD like:

```json
{
  "mcpServers": {
    "windmill": {
      "type": "http",
      "url": "https://windmill.platform.hallow.app/api/mcp/w/dev/mcp?token=<TOKEN>"
    }
  }
}
```

### 7b. (Optional) Register with VS Code-native MCP (Copilot Chat, etc.)

Skip unless the user wants the Windmill MCP in non-Claude-Code VS Code AI tools. Create `.vscode/mcp.json` in the same workdir:

```json
{
  "servers": {
    "windmill": {
      "type": "http",
      "url": "https://windmill.platform.hallow.app/api/mcp/w/dev/mcp?token=<TOKEN_FROM_§3>"
    }
  }
}
```

Add to `.gitignore` too:

```bash
# macOS/Linux/WSL
grep -qxF '.vscode/mcp.json' .gitignore || echo '.vscode/mcp.json' >> .gitignore
```

```powershell
# Windows
if (-not (Select-String -Path .gitignore -Pattern '^\.vscode/mcp\.json$' -Quiet)) {
  Add-Content -Path .gitignore -Value '.vscode/mcp.json'
}
```

### Verify the MCP is registered (programmatic, pre-launch)

Node is already installed per §4a, so use it for JSON validation (no `python3` dependency).

**macOS / Linux / WSL:**
```bash
node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" \
  ~/dev/wmill/.mcp.json && echo "mcp.json: valid"

claude mcp list | grep -i windmill
```

**Windows PowerShell:**
```powershell
node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" `
  "$env:USERPROFILE\dev\wmill\.mcp.json"; if ($?) { Write-Host "mcp.json: valid" }

claude mcp list | Select-String -Pattern 'windmill'
```

If `claude mcp list` does not show `windmill`, the `claude mcp add` step did not persist — re-run from the workdir. If `claude mcp list` shows `windmill` with an error/disconnected status, the token is wrong or revoked — re-mint per §3 and re-add.

### Verify the MCP is live (post-launch, performed by the engineer)

The remaining verification — that the `mcp__windmill__*` tools surface inside an active Claude Code session — is done by the engineer, not Claude in this bootstrap session.

**For Claude Code CLI users:**
1. `cd ~/dev/wmill` (or Windows equivalent), then `claude`.
2. Ask Claude to call `mcp__windmill__listScripts` or `mcp__windmill__listResource`.
3. Confirm a non-empty result.

**For Claude Code VS Code extension users:**
1. Open VS Code with the workdir as the workspace root (`code ~/dev/wmill` on macOS/Linux, or File → Open Folder on Windows).
2. Open the Claude Code panel (Spark icon in editor toolbar, or `Cmd/Ctrl+Shift+P` → "Claude Code: Open").
3. Type `/mcp` in the prompt box to confirm `windmill` is listed.
4. Ask Claude to call `mcp__windmill__listScripts`.

Common failure: CWD/workspace-root at launch ≠ the dir holding `.mcp.json`. Project-scoped MCPs only load when CWD matches.

---

## 8. End-to-end smoke test

Run in order. Each line must succeed before the next.

**Steps 1–4 are Claude-runnable (in bash or PowerShell). Step 5 is the engineer's manual check.**

### macOS / Linux / WSL

```bash
cd ~/dev/wmill

# 1. Tailnet still up
tailscale status | head -1

# 2. CLI auth works
wmill --workspace dev workspace whoami

# 3. Can list a server resource (proves token + network)
wmill --workspace dev script list --json | head -5

# 4. MCP config parses + is registered
node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" \
  ~/dev/wmill/.mcp.json && echo "mcp.json: valid"
claude mcp list | grep -i windmill
```

### Windows PowerShell

```powershell
Set-Location "$env:USERPROFILE\dev\wmill"

# 1. Tailnet
tailscale status | Select-Object -First 1

# 2. CLI auth
wmill --workspace dev workspace whoami

# 3. Server resource list
wmill --workspace dev script list --json | Select-Object -First 5

# 4. MCP config valid + registered
node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" `
  "$env:USERPROFILE\dev\wmill\.mcp.json"; if ($?) { Write-Host "mcp.json: valid" }
claude mcp list | Select-String -Pattern 'windmill'
```

5. **(Engineer, not Claude)** Launch Claude Code (CLI or VS Code extension) from the workdir, ask Claude to call `mcp__windmill__listScripts`, confirm non-empty result.

If all five pass:
- Claude Code (CLI or VS Code) from the workdir drives Windmill via the MCP.
- `wmill --workspace dev …` from the same shell handles direct CLI ops (job inspection, dependency reload, etc.).
- YAML edits stay local; admins mirror to the server via API per Hallow policy.

---

## 9. Failure mode quick-reference

| Symptom | Likely cause | Fix |
|---|---|---|
| `wmill workspace whoami` → "no active workspace" | Token never registered, or shell switched workspace | Re-run `wmill workspace add dev …` (§6) |
| `wmill … --workspace dev` → `401 Unauthorized` | Token revoked, expired, or wrong workspace | Mint new token (§3), re-add (§6 + §7) |
| `dig` / `Resolve-DnsName` returns a public IP | MagicDNS disabled | macOS/Linux: `sudo tailscale up --accept-dns`. Windows: tray → enable Use Tailscale DNS |
| Reachability check → connection refused/timeout | Not on tailnet | `tailscale up` and re-auth |
| Claude Code missing `mcp__windmill__*` tools | Launched from wrong CWD, or `.mcp.json` malformed | `claude mcp list` from the workdir; confirm JSON parses; relaunch from correct CWD |
| `wmill sync push` somehow ran | **STOP.** Banned at Hallow — push deletes server state not in local files and clobbers secret variables. | Notify a platform admin immediately |
| `wmill --version` fails or misbehaves after install | Node < 20 | `node --version`; install Node 20+ (§4a); reinstall `windmill-cli@1.699.0` |
| `wmill workspace add` opens an interactive prompt | Old CLI version | `npm install -g windmill-cli@1.699.0`; retry with `--token` |
| `claude` command not on PATH | Shell env not picking up Claude Code install dir | macOS/Linux: open a new terminal, or `source ~/.zshrc` / `~/.bashrc`. Windows: open a new PowerShell window. Re-install per `docs.anthropic.com/en/docs/claude-code` if still missing |
| `claude mcp list` shows `windmill` but tools missing in-session | CWD at launch ≠ workdir | CLI: `cd <workdir> && claude`. VS Code: re-open workdir as workspace root |
| `npm install -g` fails with `EACCES` | Root-owned npm prefix | Reinstall Node via volta/nvm/fnm; never `sudo npm install -g` |
| VS Code extension doesn't see MCP after CLI registration | VS Code workspace root ≠ workdir, or extension not signed in | File → Open Folder → select workdir; check sign-in via Spark icon |

---

## 10. What the user should NOT do

Hard rules:

- **Never `wmill sync push`** anywhere, anytime. Use API/UI/MCP.
- **Never commit a token** to git (CLI config, `.mcp.json`, `.vscode/mcp.json`, `wmill.yaml`, scripts, anything).
- **Never edit other users' folders** (`u/<other>/**`).
- **Prefer folder ACLs over new Windmill groups.** (The old CE 3-group cap is GONE — Hallow's customized-OSS fork neutered it via `deviation: neuter CE group cap`, so group creation no longer hard-fails at 3. But folder ACLs on user-owned folders remain the Hallow convention; don't proliferate groups just because you now can. Ask an admin if a new group is genuinely warranted.)
- **Never bypass tailnet** to reach Windmill. If reachability breaks, fix Tailscale, not the workaround.
- **Never `sudo npm install -g`** anything. Fix the Node install instead.

**Known stale guidance to ignore:** older platform-repo docs may still show `wmill sync push` in CI examples. That language predates the Hallow-wide ban. The ban in this onboarding doc takes precedence. Schedule + dep changes go via API / UI / MCP, same as everything else.

---

## Appendix — related docs

Internal-only docs live in the platform repo's `__docs/` directory. If you have access:

- `user-provisioning.md` — who can join the tailnet, how
- `windmill-ops.md` — operator runbook (errors, concurrency, webhooks, deps)
- `windmill-iac.md` — staging/prod sync in CI (admins only)
- `windmill-instance.md` — server config, BASE_URL, encryption
- `admin-access.md` — auth-proxy gating model

If you don't have access to the platform repo, ask a platform admin.
