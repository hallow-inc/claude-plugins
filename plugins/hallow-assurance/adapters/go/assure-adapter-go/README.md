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
| `VER-MUTATION-CHANGED` | Gremlins v0.6.0 |

Install Gremlins the same way:

```sh
GOBIN="$HOME/go/bin" go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
```

## Reference

`assure-adapter-go reference` prints `reference.md`, embedded at build time: the Go half of the
plugin's `assure-testing` skill. `describe` names it with `"reference": "go"`, and
`assure reference go` prints it. Edit `reference.md` and rebuild the adapter to change it.

## Mutation

Gremlins mutates only lines changed since `--changed-from`, found with `git diff --merge-base`, so
untracked files are not mutated. `git add` a new file to have it tested locally; CI has no
untracked files. Mutants on unchanged lines appear as `Ignored`. Each mutant runs its own package's
tests only, so code tested only from another package scores `NoCoverage`.

With `--scope`, Gremlins runs on each selected module without `--diff`, and the adapter drops every
mutant outside the scope's ranges, along with files left with none. The whole module is mutated
even for a one-line range.

`RAPID_SEED` is set from the `HEAD` commit, so rerunning on one commit generates the same property
inputs.

## Fail-on-base

The adapter extracts the module at `--changed-from` with `git archive` into a temporary directory,
copies in the change's `_test.go` files, and runs the top-level `Test*` and `Fuzz*` functions the
change added or whose text changed. A module whose `go.mod` uses `replace` with a path outside the
module does not build there, and its tests count as errors. A scope has no base commit, so
`VER-FAIL-ON-BASE` with `--scope` is a usage error.

## Fuzz seeds

For each package with a changed source file, the adapter runs `go test -run '^Fuzz'`: every `Fuzz*`
target runs over its `f.Add` seeds and its committed `testdata/fuzz/<Name>/` corpus, without
`-fuzz`, so no new corpus files are written. Evidence is one JUnit suite per package, with an
`assure.source` property per changed source file and one case per target; a package with no target
is a suite with zero cases. Generated files and files under `testdata/` are not listed.

Coverage-guided fuzzing runs nightly in CI (`hallow-assurance-fuzz.yml`, 60 s per target), not in
`evaluate`. A fuzz worker reruns `TestMain` with the parent's environment, so a test package that
narrows `PATH` in `TestMain` fails there with "terminated without fuzzing: EOF".

## Coverage

`go test -covermode=set -coverpkg=<selected packages> ./...` per module, with `RAPID_SEED` from
`HEAD` and without `-race`. The profile becomes LCOV with repo-relative `SF` paths: each line takes
the maximum count of the profile blocks covering it, and every top-level function or method gets an
`FN` record spanning its declaration, named `Func` or `Recv.Method` (pointer and type parameters
dropped). Every non-test file of a selected package that builds on the current platform gets a record,
including files with no statements.
