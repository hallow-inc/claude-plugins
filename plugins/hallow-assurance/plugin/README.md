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

Local hooks are early warning. CI reruns every check and is the authority.

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
| `/hallow-assurance:bugfix <issue\|seed>` | by you | Verifier writes a failing test, implementer fixes it, fast check passes; you commit with `Assure-Kind: fix` |

## Provenance and review

Provenance is off unless `assurance.yaml` sets `provenance: true`. Off, nothing is recorded, CI does
not check who wrote each file, and the guard hook's role rules are the only enforcement; an edit
through Bash goes unnoticed. CI reads the setting from the manifest at the base ref, so turning it
on takes effect from the next PR.

With provenance on, commit `.assure/provenance/` with your change. CI rebuilds each changed level
A–B file's history from those records: a test file must be written only by the verifier or pruner,
a source or config file never by them, and a file no adapter claims (a fixture, a recorded payload)
by either. Tests written by the wrong agent fail. An edit made outside the file tools (through Bash,
an editor, or a formatter) leaves a **gap**, which passes and is listed as unreviewed.

Protected-file changes pass and are listed as unreviewed, whatever the provenance setting. A team
that wants a second reviewer sets `human_review: {B: required}` (any of `A`–`D`) in
`assurance.yaml`: at those levels a protected change, and with provenance on a gap, fails unless
someone other than the PR author approves the PR's head commit.

## Install

The hooks need `assure` on `PATH`, plus `assure-adapter-<lang>` for each language the manifest's
`languages` lists. There is no release channel yet, so build `assure` from a clone of this repo:

```sh
cd plugins/hallow-assurance
go install ./cmd/assure
```

Build each adapter from `adapters/<lang>/assure-adapter-<lang>/` into a directory on `PATH`; the
directory name makes `assure-adapter-<lang>` the default binary name. The tools an adapter runs
are listed under `objectives` in its `describe` output (`assure-adapter-<lang> describe`); install
those too.

Then install the plugin from the `hallow-claude-plugins` marketplace:

```
/plugin install hallow-assurance@hallow-claude-plugins
```

If `assure` is missing or was built from a different version than the plugin, SessionStart and
PreToolUse exit 2 with a message saying which, so edits stay blocked. Stop and SubagentStop let the agent stop and
show you the same message, because the agent cannot fix the install.

## Declaring a bug fix

End a fix commit's message with the trailer `Assure-Kind: fix`, in the last paragraph:

```
Fix off-by-one in Last

Assure-Kind: fix
```

`assure evaluate` then requires every test the change adds or modifies to fail on the base commit
(`VER-FAIL-ON-BASE`). Only committed messages count, and a squash merge usually turns the trailer
into body text, so the check happens on the pull request.
