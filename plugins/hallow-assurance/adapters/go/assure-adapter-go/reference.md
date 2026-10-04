# Go testing reference

How `assure-adapter-go` turns Go tests into evidence. Read this with the `assure-testing` skill; this
file covers only what is specific to Go.

## Running tests

`VER-TESTS-PASS` runs, per module, for the packages with a changed file:

```sh
go test -race -shuffle=on -short -json ./pkg/...
```

Run the same flags locally before stopping. A test that passes only in one order, or only without
`-race`, fails here. `-short` is set, so a test that calls `testing.Short()` to skip slow work is
skipped in the fast check and still runs in CI.

`RAPID_SEED` is set from the `HEAD` commit for coverage, fuzz seeds, mutation, and fail-on-base, so
rerunning on one commit generates the same property inputs.

## Properties with `pgregory.net/rapid`

Prefer a property over a table of hand-picked cases. Generate the input space and assert what must
hold for every input:

```go
func TestRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := rapid.SliceOf(rapid.Byte()).Draw(t, "in")
		out, err := Decode(Encode(in))
		if err != nil || !bytes.Equal(in, out) {
			t.Fatalf("round trip: in=%x out=%x err=%v", in, out, err)
		}
	})
}
```

- `*rapid.T` does not satisfy `testing.TB`. A helper used from both kinds of test takes a small
  interface such as `interface{ Helper(); Fatalf(string, ...any) }`.
- A failing property writes a fail file under `testdata/rapid/`. Do not commit it; add
  `**/testdata/rapid/` to `.gitignore`.
- One `rapid.Check` counts as one test case for `VER-TEST-BUDGET`, however many inputs it draws.

## Native fuzz targets

A parser, decoder, or any function over untrusted bytes gets a `Fuzz*` target:

```go
func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"a":1}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = Decode(b)
	})
}
```

- Seeds come from `f.Add` and from the committed corpus at `testdata/fuzz/FuzzDecode/`. Commit a
  corpus file for every crasher you fix; that file is the regression test.
- `evaluate` runs every target over its seeds with `go test -run '^Fuzz'`, without `-fuzz`. Coverage-
  guided fuzzing runs nightly in CI.
- A fuzz worker reruns `TestMain` with the parent's environment. A `TestMain` that narrows `PATH`
  makes fuzzing fail with "terminated without fuzzing: EOF"; pass settings through environment
  variables instead.

## Mutation (`VER-MUTATION-CHANGED`)

Gremlins mutates only lines changed since `--changed-from`. The score is
`(Killed + Timeout) / (Killed + Timeout + Survived + NoCoverage)`, against the level's threshold.

- Each mutant runs only its own package's tests. Code exercised only from another package's tests
  scores `NoCoverage`: put the test in the package that owns the code.
- Untracked files are not mutated. `git add` a new file to have it mutated locally.
- A surviving mutant means no test observes that line's behavior. Add an assertion that would
  change if the line changed; do not add a test that only executes the line.

## Fail-on-base (`VER-FAIL-ON-BASE`)

For a commit with the trailer `Assure-Kind: fix`, the adapter extracts the module at the base
commit, copies in the change's `_test.go` files, and runs the top-level `Test*` and `Fuzz*`
functions the change added or edited. Each must fail there.

1. Write the test first and run it on the unfixed code. It must fail for the reason the bug report
   gives, not because it does not compile.
2. Fix the code. The test passes.
3. The human commits with `Assure-Kind: fix` in the message trailer. Agents do not commit.

A test that calls a function the fix added does not compile on the base, and counts as an error, not
as a failure. Reproduce the bug through the API that already existed.

## Test budget (`VER-TEST-BUDGET`)

Counted from source for each changed `_test.go` file: each new top-level `Test*` or `Fuzz*` counts 1,
and each test function adds the increase in its `t.Run` call sites. A loop around one `t.Run` is one
site. Table rows, `rapid.Check` draws, examples, and benchmarks are not cases. A renamed test counts
as new.
