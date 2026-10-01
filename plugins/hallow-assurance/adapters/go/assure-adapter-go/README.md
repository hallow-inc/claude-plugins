# assure-adapter-go

The Go adapter for `assure`. Build it into a directory on `PATH`, never into `GOBIN` when a version
manager owns that directory:

```sh
go build -o "$HOME/go/bin/assure-adapter-go" ./adapters/go/assure-adapter-go
```

## Tools

| Objective | Tool |
|---|---|
| `VER-TESTS-PASS`, `VER-FAIL-ON-BASE`, `VER-ROBUST-FUZZ`, `VER-COVERAGE-RESOLUTION` | `go` |
| `VER-TEST-BUDGET` | built into the adapter |
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

## Fuzz seeds

For each package with a changed source file, the adapter runs `go test -run '^Fuzz'`: every `Fuzz*`
target runs over its `f.Add` seeds and its committed `testdata/fuzz/<Name>/` corpus, without
`-fuzz`, so no new corpus files are written. Evidence is one JUnit suite per package, with an
`assure.source` property per changed source file and one case per target; a package with no target
is a suite with zero cases. Generated files and files under `testdata/` are not listed.

Coverage-guided fuzzing runs nightly in CI (`hallow-assurance-fuzz.yml`, 60 s per target), not in
`evaluate`. A fuzz worker reruns `TestMain` with the parent's environment, so a test package that
narrows `PATH` in `TestMain` fails there with "terminated without fuzzing: EOF".

## Test budget

Counted from source, for each changed `.go` file that is not generated:

- `_test.go` file: each top-level `Test*` or `Fuzz*` function absent at `--changed-from` counts 1,
  and each top-level test function adds the increase, if any, in its `t.Run` call sites. A call
  site is `<t>.Run(name, fn)` where `<t>` is a `*testing.T` parameter of the function or of a
  function literal inside it. A loop around one `t.Run` is one site; table rows, `rapid.Check`,
  examples, and benchmarks are not cases. A renamed test counts as new.
- other `.go` file: lines added by `git diff --numstat`; deletions are free.

Untracked files count as wholly new.

## Coverage

`go test -covermode=set -coverpkg=<selected packages> ./...` per module, with `RAPID_SEED` from
`HEAD` and without `-race`. The profile becomes LCOV with repo-relative `SF` paths: each line takes
the maximum count of the profile blocks covering it, and every top-level function or method gets an
`FN` record spanning its declaration, named `Func` or `Recv.Method` (pointer and type parameters
dropped). Every non-test file of a selected package that builds on the current platform gets a record,
including files with no statements.
