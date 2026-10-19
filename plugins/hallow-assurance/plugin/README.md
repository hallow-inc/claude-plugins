# hallow-assurance plugin

Claude Code hooks for repos that adopt hallow-assurance with an `assurance.yaml`. In any other repo
the hooks exit immediately and do nothing.

| Event | What happens |
|---|---|
| SessionStart | Injects the level map and applicable objectives; records hashes of protected files |
| PreToolUse (`Edit`, `Write`, `NotebookEdit`) | Denies edits to protected files and edits the role rules forbid; before an allowed edit, records the file's blob |
| PostToolUse (`Edit`, `Write`, `NotebookEdit`) | With `provenance: true`, appends a provenance record (file, blobs before and after, agent) to `.assure/provenance/<session>.jsonl`. Never blocks; if recording fails it warns you. Otherwise does nothing |
| Stop | Blocks stopping while changed code fails a fast objective or a protected file changed. Each distinct set of failures blocks at most 3 times per session; after that the agent may stop, and every later stop with the same failures warns you instead of blocking. Failures the agent cannot fix (no protected-file snapshot, an adapter not on `PATH`) warn you on the first stop and never block |
| SubagentStop (`hallow-assurance:verifier`) | Blocks the verifier from finishing while a changed package has a failing test (only `VER-TESTS-PASS`; source lint is the parent's Stop check). Same retry cap as Stop, counted per subagent. Other subagents are ignored |

Local hooks are early warning. The verdict is `assure evaluate`, and `authority` in
`assurance.yaml` says whose run counts:

| `authority` | Verdict |
|---|---|
| `ci` | The CI run of `assure evaluate`, as a required check |
| `local` (the default) | Your own run of `assure evaluate`; its summary says the verdict is self-attested |

With no `authority` key, a manifest that has `human_review` is read as `ci`, so manifests written
before `authority` existed keep their meaning.

See [Solo, local-only](#solo-local-only) for using the plugin on a project that has not adopted it.

## Subagents

| Agent | May edit |
|---|---|
| `hallow-assurance:implementer` | source and config files; generated files at levels C–D |
| `hallow-assurance:verifier` | test and fuzz-corpus files |
| `hallow-assurance:pruner` | test and fuzz-corpus files, removing or merging tests on mutation evidence |

The verifier and pruner preload the `assure-testing` skill.

At levels A and B, only the verifier or pruner may write tests; the main thread is denied. The
guard enforces this; the agents' tool lists are a second line.

## Skills

| Skill | Invoked | What it does |
|---|---|---|
| `assure-testing` | by the model, when it writes tests or reads `assure` output | How tests are written here: properties over examples, fail-first bug fixes, coverage resolutions, reading `assure` output, deterministic simulation, formal models. Points to `assure reference <lang>` for the language detail, which ships inside each adapter |
| `/hallow-assurance:check` | by you | Runs `assure check --fast`, then offers `assure evaluate` against the merge-base |
| `/hallow-assurance:bugfix <issue\|seed>` | by you | Verifier writes a failing test, implementer fixes it, fast check passes; you commit with a `fix(<scope>):` subject |

## Provenance and review

Provenance is off unless `assurance.yaml` sets `provenance: true`. Off, nothing is recorded, CI does
not check who wrote each file, and the guard hook's role rules are the only enforcement; an edit
through Bash goes unnoticed. `assure evaluate` reads the setting from the manifest at the base ref,
so turning it on takes effect from the next PR. A manifest kept out of git has no base ref version,
so its working-tree content is used instead.

With provenance on, commit `.assure/provenance/` with your change. CI rebuilds each changed level
A–B file's history from those records: a test file must be written only by the verifier or pruner,
a source or config file never by them, and a file no adapter claims (a fixture, a recorded payload)
by either. Tests written by the wrong agent fail. An edit made outside the file tools (through Bash,
an editor, or a formatter) leaves a **gap**, which passes and is listed as unreviewed.

Protected-file changes pass and are listed as unreviewed, whatever the provenance setting. A team
that wants a second reviewer sets `authority: ci` and `human_review: {B: required}` (any of `A`–`D`)
in `assurance.yaml`: at those levels a protected change, and with provenance on a gap, fails unless
someone other than the PR author approves the PR's head commit. `human_review` with
`authority: local` is a manifest error, because a review needs a pull request.

## Solo, local-only

You can hold your own changes to hallow-assurance on a project that has not adopted it, such as an
open-source project. The project never sees the manifest.

1. Install `assure` and the adapters as in [Install](#install).
2. Write `assurance.yaml` at the repository root without an `authority` key, and keep it and
   `.assure/` out of git by adding both to `.git/info/exclude`:

   ```
   assurance.yaml
   .assure/
   ```

3. Pin tool versions in an untracked file if you like, such as `mise.local.toml`. Under the local
   authority an untracked pin is listed in the summary and changes no verdict.
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

What the local authority does not protect against:

- The agent runs as your OS user, so a local verdict detects problems after the fact; it does not
  prevent them. The guard hook and the Stop check for protected-file changes are the only barriers.
- With `.assure/` out of git, the append-only check on provenance records has no earlier version to
  compare against, so a rewritten record goes unnoticed.
- The working-tree manifest stands in for the base manifest, so an edit to it made outside a session
  is invisible to `assure evaluate`.

`authority: ci` with a manifest that is not in git makes `assure evaluate` exit 2, because CI could
not read the manifest.

## Install

The hooks need `assure` on `PATH`, plus the adapter for each language the manifest's `languages`
lists. One line installs `assure` and every adapter at a release tag:

```sh
go install github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/...@v0.1.0
```

Install the tools the adapters run at the versions your repository pins (`assure tools` lists them):

```sh
assure tools --install-script | sh
```

Then install the plugin from the `hallow-claude-plugins` marketplace:

```
/plugin install hallow-assurance@hallow-claude-plugins
```

If `assure` is missing or speaks a different hook protocol than the plugin, SessionStart and
PreToolUse exit 2 with a message giving the `go install` line for the plugin's version, so edits
stay blocked. Stop and SubagentStop let the agent stop and show you the same message, because the
agent cannot fix the install.

## Adopting with CI as the verdict

1. Open a pull request that adds `assurance.yaml` with `authority: ci`, and merge it first. The
   workflow pull request is then judged against a base that already holds the manifest. If your
   tool pins live in `mise.toml`, list it under `protected:` so an agent cannot move a pin:

   ```yaml
   authority: ci
   protected:
     - mise.toml
   ```

2. Open a second pull request that calls the shared workflow at the same tag as your install line:

   ```yaml
   on:
     pull_request:
     pull_request_review:
       types: [submitted, dismissed]

   jobs:
     assure:
       permissions:
         contents: read
         pull-requests: read
       uses: hallow-inc/claude-plugins/.github/workflows/assure-evaluate.yml@plugins/hallow-assurance/v0.1.0
   ```

   Pass `with: {working-directory: <dir>}` when `assurance.yaml` is not at the repository root.
   The workflow has no path filter, so you can require its job (`assure / evaluate`) in branch
   protection.

3. Pin toolchains so CI and your machine run the same versions:
   - Go: add a `toolchain` line to `go.mod` (for example `toolchain go1.27.1`). The workflow sets
     `GOTOOLCHAIN=auto`, so CI downloads that exact toolchain.
   - TypeScript: pin Node in a tracked `mise.toml`. The workflow runs `mise install` when git
     tracks a mise config, and installs no Node otherwise.

Run the same verdict locally after pushing; the reviews file must name the pushed head, so
unpushed or amended commits exit 2:

```sh
assure reviews --pr 42 > r.json
assure evaluate --merge-base-with origin/master --reviews r.json
```

`assure reviews` needs `gh`, authenticated with `gh auth login` or `GH_TOKEN`.

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
