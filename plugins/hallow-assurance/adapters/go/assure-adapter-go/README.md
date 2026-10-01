# assure-adapter-go

The Go adapter for `assure`. Build it into a directory on `PATH`, never into `GOBIN` when a version
manager owns that directory:

```sh
go build -o "$HOME/go/bin/assure-adapter-go" ./adapters/go/assure-adapter-go
```

## Tools

| Objective | Tool |
|---|---|
| `VER-TESTS-PASS`, `VER-FAIL-ON-BASE` | `go` |
| `CODE-ZERO-WARNINGS` | `go vet`, `golangci-lint` v2 |
| `CODE-CHECK-RETURNS`, `CODE-RESOURCE-BOUNDS` | `golangci-lint` v2 |
| `CODE-COMPLEXITY`, `CODE-NO-UNSAFE` | built into the adapter |
| `VER-MUTATION-CHANGED` | Gremlins v0.6.0 |

Install Gremlins the same way:

```sh
GOBIN="$HOME/go/bin" go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
```

## Mutation

Gremlins mutates only lines changed since `--changed-from`, found with `git diff --merge-base`, so
untracked files are not mutated. `git add` a new file to have it tested locally; CI has no
untracked files. Mutants on unchanged lines appear as `Ignored`. Each mutant runs its own package's
tests only, so code tested only from another package scores `NoCoverage`.

`RAPID_SEED` is set from the `HEAD` commit, so rerunning on one commit generates the same property
inputs.

## Fail-on-base

The adapter extracts the module at `--changed-from` with `git archive` into a temporary directory,
copies in the change's `_test.go` files, and runs the top-level `Test*` and `Fuzz*` functions the
change added or whose text changed. A module whose `go.mod` uses `replace` with a path outside the
module does not build there, and its tests count as errors.
