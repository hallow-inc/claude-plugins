# hallow-assurance plugin

Claude Code hooks for repos that adopt hallow-assurance with an `assurance.yaml`. In any other repo
the hooks exit immediately and do nothing.

| Event | What happens |
|---|---|
| SessionStart | Injects the level map and applicable objectives; records hashes of protected files |
| PreToolUse (`Edit`, `Write`, `NotebookEdit`) | Denies edits to protected files and edits the role rules forbid; before an allowed edit, records the file's blob |
| PostToolUse (`Edit`, `Write`, `NotebookEdit`) | Appends a provenance record (file, blobs before and after, agent) to `.assure/provenance/<session>.jsonl`. Never blocks; if recording fails it warns you |
| Stop | Blocks stopping while changed code fails a fast objective or a protected file changed. Each distinct set of failures blocks at most 3 times per session; after that the agent may stop, and every later stop with the same failures warns you instead of blocking. Failures the agent cannot fix (no protected-file snapshot, an adapter not on `PATH`) warn you on the first stop and never block |

Local hooks are early warning. CI reruns every check and is the authority.

## Subagents

| Agent | May edit |
|---|---|
| `hallow-assurance:implementer` | source and config files; generated files at levels C–D |
| `hallow-assurance:verifier` | test and fuzz-corpus files |
| `hallow-assurance:pruner` | test and fuzz-corpus files, removing or merging tests on mutation evidence |
| `hallow-assurance:inspector` | nothing (read-only review) |

At levels A and B, only the verifier or pruner may write tests; the main thread is denied. The
guard enforces this; the agents' tool lists are a second line.

## Provenance and review

Commit `.assure/provenance/` with your change. CI rebuilds each changed level A–B file's history
from those records: a test file must be written only by the verifier or pruner, a source file never
by them. An edit made outside the file tools (through Bash, an editor, or a formatter) leaves a
**gap**. A gap, or any change to a protected file, fails unless someone other than the PR author
approves the PR's head commit. A repo with no second reviewer can set
`human_review: {B: optional}` in `assurance.yaml` (not allowed for level A): gaps and protected
changes then pass and are listed as unreviewed. Tests written by the wrong agent fail either way.

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
PreToolUse exit 2 with a message saying which, so edits stay blocked. Stop lets the agent stop and
shows you the same message, because the agent cannot fix the install.

## Declaring a bug fix

End a fix commit's message with the trailer `Assure-Kind: fix`, in the last paragraph:

```
Fix off-by-one in Last

Assure-Kind: fix
```

`assure evaluate` then requires every test the change adds or modifies to fail on the base commit
(`VER-FAIL-ON-BASE`). Only committed messages count, and a squash merge usually turns the trailer
into body text, so the check happens on the pull request.
