# hallow-assurance plugin

Claude Code hooks for repos that adopt hallow-assurance with an `assurance.yaml`. In any other repo
the hooks exit immediately and do nothing.

| Event | What happens |
|---|---|
| SessionStart | Injects the level map and applicable objectives; records hashes of protected files |
| PreToolUse (`Edit`, `Write`, `NotebookEdit`) | Denies edits to protected files and edits the role rules forbid |
| Stop | Blocks stopping while changed code fails a fast objective or a protected file changed; after 3 blocks it lets the agent stop and warns you |

Local hooks are early warning. CI reruns every check and is the authority.

## Install

The hooks need `assure` on `PATH`, plus `assure-adapter-<lang>` for each language the manifest's
`languages` lists. There is no release channel yet, so build `assure` from a clone of this repo:

```sh
cd plugins/hallow-assurance
go install ./cmd/assure
```

Build each adapter from `adapters/<lang>/` into a directory on `PATH` under the name
`assure-adapter-<lang>`. The tools an adapter runs are listed under `objectives` in its
`describe` output (`assure-adapter-<lang> describe`); install those too.

Then install the plugin from the `hallow-claude-plugins` marketplace:

```
/plugin install hallow-assurance@hallow-claude-plugins
```

If `assure` is missing or was built from a different version than the plugin, every hook in an
adopted repo exits 2 with a message saying which, and Claude Code blocks the action.
