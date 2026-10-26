# Python testing reference

How `assure-adapter-python` turns pytest tests into evidence. Read this with the `assure-testing`
skill; this file covers only what is specific to Python.

## Setup

A unit is the directory holding the nearest `pyproject.toml`. The adapter needs a `uv.lock` at or
above it and a synced virtualenv (`.venv`, or `UV_PROJECT_ENVIRONMENT`). It never runs `uv` itself;
a missing or out-of-range tool fails with `environment` and the fix `uv sync --locked`.

Add the tools to the `dev` group:

```sh
uv add --dev pytest pytest-randomly pytest-cov hypothesis cosmic-ray
uv sync --locked
```

`pytest-cov` brings `coverage` and `cosmic-ray` brings `parso`. A version outside the adapter's
supported range fails with `environment` naming the range; `assure-adapter-python tools --root .`
lists what resolved.

Add `.hypothesis/` to `.gitignore`; Hypothesis writes its example database there.

`poetry.lock`, `pdm.lock`, or `Pipfile.lock` beside `uv.lock` is refused, and `requirements*.txt` is
ignored. Setting `python_files`, `python_classes`, or `python_functions` in the pytest configuration
is refused for fail-on-base and mutation, which rely on pytest's default test names: files
`test_*.py` or `*_test.py`, functions `test*`, classes `Test*`.

## Running tests

`VER-TESTS-PASS` runs every test in each unit holding a changed file, shuffled by pytest-randomly
with the adapter's seed:

```sh
uv run pytest -p randomly -p hypothesispytest --randomly-seed=<seed> --hypothesis-seed=<seed> --hypothesis-profile=default
```

A test that passes only in one order fails here. A test file that fails to collect (an import or
syntax error) counts as an erroring test, not as no tests.

## The seed

The adapter derives one seed from the `HEAD` commit, sets it as `ASSURE_SEED`, and passes it to both
pytest-randomly and Hypothesis, so reruns on one commit order tests and generate inputs
identically. It also loads Hypothesis's `default` profile, because under `CI` Hypothesis would load
its `ci` profile, which sets `derandomize` and ignores the seed. Leave tests unseeded: no
`conftest.py`, settings profile, `@seed`, or `derandomize`.

## Properties with Hypothesis

```python
from hypothesis import given, strategies as st

from codec import decode, encode


@given(st.binary())
def test_decode_inverts_encode(data):
    assert decode(encode(data)) == data
```

A failing property prints its shrunk counterexample. Keep it as an `@example` (see Fuzz files).

## Fuzz files (`VER-ROBUST-FUZZ`)

A parser, decoder, or any function over untrusted input gets a fuzz file. For
`<unit>/<dir>/<stem>.py` the adapter looks in two places:

- `<unit>/<dir>/test_<stem>_fuzz.py`, beside the source
- `<unit>/tests/<dir>/test_<stem>_fuzz.py`, with a leading `src/` dropped from `<dir>`:
  `src/pkg/decode.py` → `tests/pkg/test_decode_fuzz.py`

```python
from hypothesis import example, given, strategies as st

from pkg.decode import decode


@given(st.text())
@example('{"a":')
@example("\x00")
def test_decode_never_raises_unexpectedly(text):
    try:
        decode(text)
    except ValueError:
        pass
```

- Every crasher you fix becomes an `@example`. Examples run before generated inputs, on every run,
  so the decorator is the regression test.
- The name starts with `test_`, so `VER-TESTS-PASS` runs the file too.
- A changed source file with no fuzz file reports zero fuzz cases.

## Mutation (`VER-MUTATION-CHANGED`)

cosmic-ray mutates only mutants lying wholly within lines changed since `--changed-from`, in tracked
source files, on a copy of the unit. `git add` a new file to have it mutated locally.

- **Tests per file.** A coverage run records which tests execute each changed line, and only those
  tests judge that file's mutants. A changed line executed outside any test (at import time, such as
  a `def` line or a module constant) widens the file to the whole suite. A file no test reaches
  reports every mutant `NoCoverage` without running it.
- **Baseline.** The selected tests must pass before mutation, or the run fails.
- **Outcomes.** Failing tests or a collection error → `Killed`; all passing → `Survived`; over
  `max(60 s, 20 × baseline)` → `Timeout`. Any other pytest exit fails the run.
- **Score.** `(Killed + Timeout) / (Killed + Timeout + Survived + NoCoverage)`, against the level's
  threshold.
- **Syntax guard.** Every changed file must parse with parso first. Syntax parso does not support
  (PEP 758 `except A, B:`, PEP 701 f-strings nesting the same quote) fails the run with
  `environment`, naming the file and line, and no mutant runs.

The operator set keeps comparison, boolean, unary, number, and loop operators, and drops these:

| Dropped | Why |
|---|---|
| binary replacements across families: arithmetic (`Add Sub Mul Div FloorDiv Mod`), bitwise (`LShift RShift BitOr BitAnd BitXor`), and `Pow` with anything | `+ → <<` kills say little and dominate volume |
| `Lt LtE Gt GtE` ↔ `Is IsNot` | kills are type errors |
| `ExceptionReplacer`, `RemoveDecorator` | kills are import failures |
| `VariableReplacer`, `VariableInserter` | replacements drawn from `random`; reruns differ |

## Fail-on-base (`VER-FAIL-ON-BASE`)

For a change with a commit whose subject is typed `fix` (`fix:` or `fix(<scope>):`), the adapter
selects every test in a changed test file whose source text (decorators included) differs from the
test with the same node id at the base, or that has no such test there. It builds the base tree,
copies in the change's test files, and runs the selected tests against the base code. Each must fail
there. Follow the fail-first loop in `assure-testing`, with these Python rules:

- Any edit to a test's text selects it, including a refactor that still passes on the base. Keep
  unrelated test edits out of a fix commit's range.
- Reproduce the bug through code that existed at the base. A test that imports something the fix
  added fails to collect there, and counts as an error, not as a failure.
- Parameterized cases fold into one result per test: the test fails on base when any case fails.
- Pair every fix with a reproducing test. A fix that selects no test passes with a note, unless the
  manifest sets `require_fix_test: true`.
