# hallow-assurance plugin

Claude Code hooks for repos that adopt hallow-assurance with an `assurance.yaml`. In any other repo
the hooks exit immediately and do nothing.

| Event | What happens |
|---|---|
| SessionStart | Injects the level map and applicable objectives |
| Stop | Blocks stopping while changed code fails a fast objective. Each distinct set of failures blocks at most 3 times per session; after that the agent may stop, and every later stop with the same failures warns you instead of blocking. Failures the agent cannot fix (an adapter not on `PATH`, tools not installed) warn you on the first stop and never block |

No hook restricts which files the agent edits. The Stop hook is early warning; the verdict is
`assure evaluate`, run locally or in CI.

See [Solo, local-only](#solo-local-only) for using the plugin on a project that has not adopted it.

## Subagents

| Agent | Edits |
|---|---|
| `hallow-assurance:implementer` | source and config files; generated files at levels C–D |
| `hallow-assurance:verifier` | test and fuzz-corpus files |
| `hallow-assurance:pruner` | test and fuzz-corpus files, removing or merging tests on mutation evidence |

The verifier and pruner preload the `assure-testing` skill. The agents are optional helpers: their
prompts say which files each should edit, and nothing enforces it.

## Skills

| Skill | Invoked | What it does |
|---|---|---|
| `assure-testing` | by the model, when it writes tests or reads `assure` output | How tests are written here: properties over examples, fail-first bug fixes, coverage resolutions, reading `assure` output, deterministic simulation, formal models. Points to `assure reference <lang>` for the language detail, which ships inside each adapter |
| `/hallow-assurance:check` | by you | Runs `assure check --fast`, then offers `assure evaluate` against the merge-base |
| `/hallow-assurance:bugfix <issue\|seed>` | by you | Verifier writes a failing test, implementer fixes it, fast check passes; you commit with a `fix(<scope>):` subject |

## Solo, local-only

You can hold your own changes to hallow-assurance on a project that has not adopted it, such as an
open-source project. The project never sees the manifest.

1. Install `assure` and the adapters as in [Install](#install).
2. Write `assurance.yaml` at the repository root, and keep it and `.assure/` out of git by adding both to `.git/info/exclude`:

   ```
   assurance.yaml
   .assure/
   ```

3. Pin tool versions in an untracked file if you like, such as `mise.local.toml`. An untracked
   pin is listed in the summary and changes no verdict.
4. Commit your work, then run the verdict against the branch you will open the pull request into:

   ```sh
   assure evaluate --merge-base-with upstream/main
   ```

   The changed files are those since your branch forked from `upstream/main`. If the ref does not
   resolve, `git fetch` it first.

To run it on every push, save this as `.git/hooks/pre-push` and make it executable:

```sh
#!/bin/sh
set -e
assure evaluate --merge-base-with upstream/main
```

The hook is a seatbelt, not enforcement. `git push --no-verify` skips it, and so can the agent. It
lives in one clone only, and mutation testing makes each push slow.

A local verdict detects problems after the fact; it does not prevent them.

## Install

The hooks need `assure` on `PATH`, plus the adapter for each language the manifest's `languages`
lists. One line installs `assure` and every adapter at a release tag:

```sh
go install github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/...@v0.2.0
```

Install the tools the adapters run at the versions your repository pins (`assure tools` lists them):

```sh
assure tools --install-script | sh
```

Then install the plugin from the `hallow-claude-plugins` marketplace:

```
/plugin install hallow-assurance@hallow-claude-plugins
```

If `assure` is missing or speaks a different hook protocol than the plugin, SessionStart and Stop
show you a message giving the `go install` line for the plugin's version and block nothing, because
the agent cannot fix the install.

### Upgrading from v0.1.x

v0.2.0 removes the edit guard, protected files, provenance, and reviews. Before upgrading:

1. Delete these keys from `assurance.yaml` if present: `protected`, `authority`, `human_review`,
   `provenance`. v0.2.0 rejects a manifest that still has one.
2. Delete any `.assure/waivers.yaml` entry for `CFG-PROTECTED` or `IND-VERIFIER-DISTINCT`. They
   apply to nothing now, and an expired one still fails `assure evaluate`.
3. Update both halves together, then restart Claude Code. A v0.1.x plugin denies every edit when a
   v0.2.0 `assure` is on `PATH`:

   ```sh
   go install github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/...@v0.2.0
   ```

   ```
   /plugin update hallow-assurance
   ```

4. Drop `--reviews` and any `assure reviews` step from your own scripts.

## Running in CI

The shared workflow runs `assure evaluate` on every pull request.

1. Add `assurance.yaml` and a workflow that calls the shared workflow at the same tag as your
   install line. Both can land in one pull request:

   ```yaml
   on:
     pull_request:

   jobs:
     assure:
       permissions:
         contents: read
       uses: hallow-inc/claude-plugins/.github/workflows/assure-evaluate.yml@plugins/hallow-assurance/v0.2.0
   ```

   Pass `with: {working-directory: <dir>}` when `assurance.yaml` is not at the repository root.
   The workflow has no path filter, so you can require its job (`assure / evaluate`) in branch
   protection.

2. Pin toolchains so CI and your machine run the same versions:
   - Go: add a `toolchain` line to `go.mod` (for example `toolchain go1.27.1`). The workflow sets
     `GOTOOLCHAIN=auto`, so CI downloads that exact toolchain.
   - TypeScript: pin Node in a tracked `mise.toml`. The workflow runs `mise install` when git
     tracks a mise config, and installs no Node otherwise.

Run the same verdict locally:

```sh
assure evaluate --merge-base-with origin/master
```

## Releases

A tag `plugins/hallow-assurance/vX.Y.Z` on `master` versions `assure`, every adapter, and the
plugin together.

- While `X` is `0`, a change to the hook protocol or the adapter protocol ships as a minor release
  (`Y` goes up), never a patch. CI does not check this; the hook's protocol check catches a
  mismatched install when it runs.
- `plugin/.claude-plugin/plugin.json` carries `version` equal to the tag's `X.Y.Z`; the release job
  fails a tag that differs. The marketplace entry carries no `version`.
- Claude Code updates a plugin only when its version changes, so plugin-only edits (skills, docs)
  reach users at the next release.

## Declaring a bug fix

Give a fix commit a subject with the Conventional Commits type `fix`:

```
fix(p): off-by-one in Last
```

`assure evaluate` then requires every test the change adds or modifies to fail on the base commit
(`VER-FAIL-ON-BASE`). Any commit between the base and `HEAD` counts; after a squash merge, the
subject is the pull request title.

Pair every fix with a test that reproduces the bug. A fix that changes no test passes
`VER-FAIL-ON-BASE` with a note recommending one; set `require_fix_test: true` in `assurance.yaml`
to make it fail instead.
